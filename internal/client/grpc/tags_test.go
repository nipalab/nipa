package grpc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func (f *fakeServer) CreateTag(_ context.Context, req *pb.CreateTagRequest) (*pb.CreateTagResponse, error) {
	f.createTagReq = req
	if f.createTagErr != nil {
		return nil, f.createTagErr
	}
	return &pb.CreateTagResponse{Tag: f.createTag}, nil
}

func (f *fakeServer) ListTags(_ context.Context, req *pb.ListTagsRequest) (*pb.ListTagsResponse, error) {
	f.lastListTagsReq = req
	if f.listTagsErr != nil {
		return nil, f.listTagsErr
	}
	start := 0
	if req.GetLastId() != "" {
		for i, tag := range f.allTags {
			if tag.GetId() == req.GetLastId() {
				start = i + 1
				break
			}
		}
	}
	if start >= len(f.allTags) {
		return &pb.ListTagsResponse{}, nil
	}
	end := start + int(req.GetLimit())
	if end > len(f.allTags) {
		end = len(f.allTags)
	}
	return &pb.ListTagsResponse{Tags: f.allTags[start:end]}, nil
}

func (f *fakeServer) DeleteTag(_ context.Context, req *pb.DeleteTagRequest) (*pb.DeleteTagResponse, error) {
	f.lastDeleteTagReq = req
	if f.deleteTagErr != nil {
		return nil, f.deleteTagErr
	}
	return &pb.DeleteTagResponse{}, nil
}

func TestClient_CreateTag_Success(t *testing.T) {
	created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	fs := &fakeServer{
		createTag: &pb.Tag{
			Id:        snow.ID(9).Base36(),
			Name:      "v1.0.0",
			CommitId:  snow.ID(7).Base36(),
			Message:   "first release",
			UserId:    snow.ID(3).Base36(),
			CreatedAt: timestamppb.New(created),
		},
	}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.CreateTag(context.Background(), "default", "sample", "v1.0.0", "first release", "release", "", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, snow.ID(9).Base36(), got.ID)
	require.Equal(t, "v1.0.0", got.Name)
	require.Equal(t, snow.ID(7).Base36(), got.CommitID)
	require.Equal(t, "first release", got.Message)
	require.Equal(t, snow.ID(3).Base36(), got.UserID)
	require.Equal(t, created, got.CreatedAt)

	require.NotNil(t, fs.createTagReq)
	require.Equal(t, "v1.0.0", fs.createTagReq.GetName())
	require.Equal(t, "first release", fs.createTagReq.GetMessage())
	require.Equal(t, "release", fs.createTagReq.GetFromBranch())
	require.Empty(t, fs.createTagReq.GetFromCommitId())
	require.Empty(t, fs.createTagReq.GetFromCommitHash())
	require.Equal(t, "default", fs.createTagReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.createTagReq.GetContext().GetProject())
}

func TestClient_CreateTag_Conflict(t *testing.T) {
	addr := startTestServer(t, &fakeServer{createTagErr: status.Error(codes.FailedPrecondition, `tag "v1.0.0" already exists`)})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.CreateTag(context.Background(), "default", "sample", "v1.0.0", "", "main", "", "")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, `tag "v1.0.0" already exists`, domErr.Message)
}

func TestClient_CreateTag_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.CreateTag(context.Background(), "default", "sample", "v1.0.0", "", "main", "", "")
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestClient_ListTags_Success(t *testing.T) {
	fs := &fakeServer{allTags: []*pb.Tag{
		{Id: snow.ID(2).Base36(), Name: "v1.0.1", CommitId: snow.ID(8).Base36()},
		{Id: snow.ID(1).Base36(), Name: "v1.0.0", CommitId: snow.ID(7).Base36(), Message: "release"},
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	tags, err := c.ListTags(context.Background(), "default", "sample")
	require.NoError(t, err)
	require.Len(t, tags, 2)
	require.Equal(t, "v1.0.1", tags[0].Name)
	require.Equal(t, "v1.0.0", tags[1].Name)
	require.Equal(t, "release", tags[1].Message)
	require.Equal(t, "default", fs.lastListTagsReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.lastListTagsReq.GetContext().GetProject())
}

func TestClient_ListTags_Paginated(t *testing.T) {
	const total = 101
	all := make([]*pb.Tag, 0, total)
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		all = append(all, &pb.Tag{
			Id:        snow.ID(1000 - i).Base36(),
			Name:      fmt.Sprintf("v%d", i),
			CommitId:  snow.ID(7).Base36(),
			CreatedAt: timestamppb.New(base.Add(-time.Duration(i) * time.Minute)),
		})
	}
	fs := &fakeServer{allTags: all}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	tags, err := c.ListTags(context.Background(), "default", "sample")
	require.NoError(t, err)
	require.Len(t, tags, total, "the client must follow the keyset cursor across pages")
	require.Equal(t, "v100", tags[total-1].Name)
	require.Equal(t, all[99].GetId(), fs.lastListTagsReq.GetLastId())
	require.Equal(t, all[99].GetCreatedAt().AsTime(), fs.lastListTagsReq.GetLastCreatedAt().AsTime())
}

func TestClient_ListTags_Error(t *testing.T) {
	addr := startTestServer(t, &fakeServer{listTagsErr: status.Error(codes.Internal, "boom")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.ListTags(context.Background(), "default", "sample")
	require.Error(t, err)
}

func TestClient_DeleteTag_Success(t *testing.T) {
	fs := &fakeServer{}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	require.NoError(t, c.DeleteTag(context.Background(), "default", "sample", "v1.0.0"))
	require.NotNil(t, fs.lastDeleteTagReq)
	require.Equal(t, "v1.0.0", fs.lastDeleteTagReq.GetName())
}

func TestClient_DeleteTag_NotFound(t *testing.T) {
	addr := startTestServer(t, &fakeServer{deleteTagErr: status.Error(codes.NotFound, `tag "missing" not found`)})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	err := c.DeleteTag(context.Background(), "default", "sample", "missing")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}
