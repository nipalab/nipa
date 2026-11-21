package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type issueServer struct {
	pb.UnimplementedNipaServiceServer
	resolveReq *pb.ResolveIssuesRequest
	searchReq  *pb.SearchIssuesRequest
	err        error
}

func (s *issueServer) ResolveIssues(_ context.Context, req *pb.ResolveIssuesRequest) (*pb.ResolveIssuesResponse, error) {
	s.resolveReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &pb.ResolveIssuesResponse{Issues: []*pb.IssueDetail{{
		Key: "GAME-1", Url: "https://jira/browse/GAME-1", State: "ok", Title: "New rig", Type: "Story",
		Status: "In Progress", StatusCategory: "in_progress", Assignee: "Ana",
	}}}, nil
}

func (s *issueServer) SearchIssues(_ context.Context, req *pb.SearchIssuesRequest) (*pb.SearchIssuesResponse, error) {
	s.searchReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &pb.SearchIssuesResponse{Issues: []*pb.IssueDetail{{Key: "GAME-2", State: "ok"}}, Supported: true, Unavailable: true}, nil
}

func TestClient_ResolveIssues(t *testing.T) {
	srv := &issueServer{}
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), startTestServer(t, srv)))

	got, err := c.ResolveIssues(context.Background(), "org", "proj", []string{"GAME-1"})
	require.NoError(t, err)
	require.Equal(t, []domain.Issue{{
		Key: "GAME-1", URL: "https://jira/browse/GAME-1", State: "ok", Title: "New rig", Type: "Story",
		Status: "In Progress", StatusCategory: "in_progress", Assignee: "Ana",
	}}, got)
	require.Equal(t, "proj", srv.resolveReq.GetContext().GetProject())
	require.Equal(t, []string{"GAME-1"}, srv.resolveReq.GetKeys())

	search, err := c.SearchIssues(context.Background(), "org", "proj", "rig", 7)
	require.NoError(t, err)
	require.True(t, search.Supported)
	require.True(t, search.Unavailable)
	require.Equal(t, "GAME-2", search.Issues[0].Key)
	require.Equal(t, "rig", srv.searchReq.GetQuery())
	require.Equal(t, int32(7), srv.searchReq.GetLimit())

	srv.err = status.Error(codes.FailedPrecondition, "off")
	_, err = c.ResolveIssues(context.Background(), "org", "proj", nil)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	_, err = c.SearchIssues(context.Background(), "org", "proj", "rig", 0)
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
}

func TestClient_IssuesUnimplemented(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), startTestServer(t, &fakeServer{})))
	_, err := c.ResolveIssues(context.Background(), "org", "proj", []string{"GAME-1"})
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)

	offline := NewClient(NewTransport(), &stubSession{})
	_, err = offline.ResolveIssues(context.Background(), "o", "p", nil)
	require.Error(t, err)
	_, err = offline.SearchIssues(context.Background(), "o", "p", "q", 0)
	require.Error(t, err)
}

func TestIssueKeyConversions(t *testing.T) {
	require.Nil(t, toIssueKeys(nil))
	entry := toServerCommitLogEntry(&pb.CommitLogEntry{IssueKeys: []*pb.IssueKey{{Key: "GAME-1", Url: "u"}}})
	require.Equal(t, []serverDomain.IssueKeyRef{{Key: "GAME-1", URL: "u"}}, entry.IssueKeys)
	mr := toClientMergeRequest(&pb.MergeRequestDetail{IssueKeys: []*pb.IssueKey{{Key: "GAME-2", Url: "v"}}})
	require.Equal(t, []serverDomain.IssueKeyRef{{Key: "GAME-2", URL: "v"}}, mr.IssueKeys)
}
