package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func (f *fakeServer) GetCommit(_ context.Context, req *pb.GetCommitRequest) (*pb.GetCommitResponse, error) {
	f.lastGetCommitReq = req
	if f.getCommitErr != nil {
		return nil, f.getCommitErr
	}
	return f.getCommitResp, nil
}

func (f *fakeServer) WalkCommits(_ context.Context, req *pb.WalkCommitsRequest) (*pb.WalkCommitsResponse, error) {
	f.lastWalkCommitsReq = req
	if f.walkCommitsErr != nil {
		return nil, f.walkCommitsErr
	}
	return f.walkCommitsResp, nil
}

func TestClient_GetCommit_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	commitHash := serverDomain.Hash{0x01, 0x02}
	treeHash := serverDomain.Hash{0x0a, 0x0b}
	chunkHash := serverDomain.Hash{0x0c, 0x0d}
	parent := snow.ID(3).Base36()
	fs := &fakeServer{getCommitResp: &pb.GetCommitResponse{
		Commit: &pb.CommitDetail{
			CommitId:   snow.ID(7).Base36(),
			CommitHash: commitHash.String(),
			TreeHash:   treeHash.String(),
			Parent_1Id: &parent,
			Message:    "hello",
			CreatedAt:  timestamppb.New(now),
		},
		RootTree: &pb.TreeManifest{
			TreeHash: treeHash.String(),
			Files: []*pb.FileNode{{
				Path:        "a.txt",
				Mode:        pb.FileMode_FILE_MODE_READ_WRITE,
				SizeBytes:   3,
				ChunkHashes: []string{chunkHash.String()},
			}},
		},
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.GetCommit(context.Background(), "default", "sample", snow.ID(7).Base36())
	require.NoError(t, err)
	require.Equal(t, "7", fs.lastGetCommitReq.GetCommitId())
	require.Equal(t, "7", got.ID)
	require.Equal(t, commitHash.String(), got.Hash)
	require.Equal(t, treeHash.String(), got.TreeHash)
	require.Equal(t, "3", got.Parent1ID)
	require.Empty(t, got.Parent2ID)
	require.Equal(t, "hello", got.Message)
	require.True(t, now.Equal(got.CreatedAt))
	require.NotNil(t, got.Tree)
	require.Len(t, got.Tree.FileChildren, 1)
	require.Equal(t, "a.txt", got.Tree.FileChildren[0].Name)
	require.Equal(t, chunker.FileHash([]serverDomain.Hash{chunkHash}), got.Tree.FileChildren[0].Hash)
}

func TestClient_GetCommit_Error(t *testing.T) {
	fs := &fakeServer{getCommitErr: status.Error(codes.NotFound, "commit 7 not found")}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.GetCommit(context.Background(), "default", "sample", "7")
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_WalkCommits_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	p1, p2 := snow.ID(1).Base36(), snow.ID(2).Base36()
	fs := &fakeServer{walkCommitsResp: &pb.WalkCommitsResponse{
		Commits: []*pb.CommitWalkEntry{
			{CommitId: "2", CommitHash: serverDomain.Hash{0x02}.String(), Parent_1Id: &p1, Message: "two", CreatedAt: timestamppb.New(now)},
			{CommitId: "1", CommitHash: serverDomain.Hash{0x01}.String(), Message: "one", CreatedAt: timestamppb.New(now)},
		},
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.WalkCommits(context.Background(), "default", "sample", p2, p1, 10)
	require.NoError(t, err)
	require.Equal(t, p2, fs.lastWalkCommitsReq.GetStartCommitId())
	require.Equal(t, p1, fs.lastWalkCommitsReq.GetStopCommitId())
	require.Equal(t, int32(10), fs.lastWalkCommitsReq.GetLimit())
	require.Len(t, got, 2)
	require.Equal(t, "2", got[0].ID)
	require.Equal(t, p1, got[0].Parent1ID)
	require.Empty(t, got[0].Parent2ID)
	require.Equal(t, "two", got[0].Message)
	require.Equal(t, "1", got[1].ID)
	require.True(t, now.Equal(got[1].CreatedAt))
}

func TestClient_WalkCommits_NoStop(t *testing.T) {
	fs := &fakeServer{walkCommitsResp: &pb.WalkCommitsResponse{}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.WalkCommits(context.Background(), "default", "sample", "3", "", 5)
	require.NoError(t, err)
	require.Nil(t, fs.lastWalkCommitsReq.StopCommitId)
	require.Empty(t, got)
}

func TestClient_WalkCommits_Error(t *testing.T) {
	fs := &fakeServer{walkCommitsErr: status.Error(codes.FailedPrecondition, "branch has moved")}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.WalkCommits(context.Background(), "default", "sample", "3", "", 5)
	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
}
