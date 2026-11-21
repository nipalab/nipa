package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type issueDaemon struct {
	*fakeDaemon
	issues  []*pb.IssueDetail
	search  *pb.SearchIssuesResponse
	resolve *pb.ResolveIssuesRequest
	query   *pb.SearchIssuesRequest
	err     error
}

func (d *issueDaemon) ProxyResolveIssues(_ context.Context, req *daemonpb.ProxyResolveIssuesRequest, _ ...grpc.CallOption) (*daemonpb.ProxyResolveIssuesResponse, error) {
	d.resolve = req.GetRequest()
	return &daemonpb.ProxyResolveIssuesResponse{Response: &pb.ResolveIssuesResponse{Issues: d.issues}}, d.err
}

func (d *issueDaemon) ProxySearchIssues(_ context.Context, req *daemonpb.ProxySearchIssuesRequest, _ ...grpc.CallOption) (*daemonpb.ProxySearchIssuesResponse, error) {
	d.query = req.GetRequest()
	return &daemonpb.ProxySearchIssuesResponse{Response: d.search}, d.err
}

func newIssues(t *testing.T) (*Service, *issueDaemon) {
	t.Helper()
	d := &issueDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_SearchIssues(t *testing.T) {
	svc, d := newIssues(t)
	ctx := context.Background()

	empty, err := svc.SearchIssues(ctx, "/r", "  ")
	require.NoError(t, err)
	require.True(t, empty.Configured)
	require.Nil(t, d.query)

	d.search = &pb.SearchIssuesResponse{Supported: true, Unavailable: true, Issues: []*pb.IssueDetail{{Key: "GAME-1", Url: "u", State: "ok", Title: "Rig", Type: "Story", Status: "To Do", StatusCategory: "todo", Assignee: "Ana"}}}
	got, err := svc.SearchIssues(ctx, "/r", " rig ")
	require.NoError(t, err)
	require.Equal(t, &IssueSearch{Configured: true, Supported: true, Unavailable: true, Issues: []Issue{{Key: "GAME-1", URL: "u", State: "ok", Title: "Rig", Type: "Story", Status: "To Do", StatusCategory: "todo", Assignee: "Ana"}}}, got)
	require.Equal(t, "rig", d.query.GetQuery())
	require.Equal(t, int32(issueSearchLimit), d.query.GetLimit())

	for _, code := range []codes.Code{codes.NotFound, codes.FailedPrecondition, codes.Unimplemented} {
		d.err = status.Error(code, "off")
		got, err = svc.SearchIssues(ctx, "/r", "rig")
		require.NoError(t, err)
		require.False(t, got.Configured)
		require.Empty(t, got.Issues)
	}

	d.err = status.Error(codes.PermissionDenied, "no access")
	_, err = svc.SearchIssues(ctx, "/r", "rig")
	require.EqualError(t, err, "no access")
}

func TestService_IssueBranch(t *testing.T) {
	svc, d := newIssues(t)
	ctx := context.Background()

	d.issues = []*pb.IssueDetail{{Key: "GAME-7", State: "ok", Title: "New hero rig"}}
	got, err := svc.IssueBranch(ctx, "/r", " game-7 ", "")
	require.NoError(t, err)
	require.Equal(t, "GAME-7-new-hero-rig", got.Name)
	require.Equal(t, "New hero rig", got.Issue.Title)
	require.Equal(t, []string{"GAME-7"}, d.resolve.GetKeys())

	got, err = svc.IssueBranch(ctx, "/r", "GAME-7", "rig")
	require.NoError(t, err)
	require.Equal(t, "GAME-7-rig", got.Name)

	d.issues = []*pb.IssueDetail{{Key: "GAME-7", State: "unavailable"}}
	got, err = svc.IssueBranch(ctx, "/r", "GAME-7", "")
	require.NoError(t, err)
	require.Equal(t, "GAME-7", got.Name)
	require.Equal(t, "unavailable", got.Issue.State)

	d.issues = []*pb.IssueDetail{{Key: "GAME-7", State: "not_found"}}
	_, err = svc.IssueBranch(ctx, "/r", "GAME-7", "")
	require.ErrorContains(t, err, "was not found")

	d.issues = nil
	_, err = svc.IssueBranch(ctx, "/r", "OTHER-7", "")
	require.ErrorContains(t, err, "not an issue of this project")

	d.err = status.Error(codes.NotFound, "the project does not use an issue tracker")
	got, err = svc.IssueBranch(ctx, "/r", "GAME-7", "")
	require.NoError(t, err)
	require.Nil(t, got.Issue)
	require.Equal(t, "GAME-7", got.Name)

	d.err = errors.New("down")
	_, err = svc.IssueBranch(ctx, "/r", "GAME-7", "")
	require.Error(t, err)

	_, err = svc.IssueBranch(ctx, "/r", "hero", "")
	require.ErrorContains(t, err, "is not an issue key")
}

func TestIssueKeysOnCommitsAndMergeRequests(t *testing.T) {
	keys := []*pb.IssueKey{{Key: "GAME-1", Url: "u"}}
	require.Equal(t, []IssueKey{{Key: "GAME-1", URL: "u"}}, toCommit(&pb.CommitLogEntry{IssueKeys: keys}).Issues)
	require.Equal(t, []IssueKey{{Key: "GAME-1", URL: "u"}}, toMergeRequest(&pb.MergeRequestDetail{IssueKeys: keys}).Issues)
}

func TestService_ResolveIssues(t *testing.T) {
	svc, d := newIssues(t)
	ctx := context.Background()

	none, err := svc.ResolveIssues(ctx, "/r", nil)
	require.NoError(t, err)
	require.Empty(t, none)
	require.Nil(t, d.resolve)

	d.issues = []*pb.IssueDetail{{Key: "GAME-1", State: "ok", Title: "Rig", Status: "To Do"}}
	got, err := svc.ResolveIssues(ctx, "/r", []string{"GAME-1"})
	require.NoError(t, err)
	require.Equal(t, []Issue{{Key: "GAME-1", State: "ok", Title: "Rig", Status: "To Do"}}, got)
	require.Equal(t, []string{"GAME-1"}, d.resolve.GetKeys())

	d.err = status.Error(codes.FailedPrecondition, "off")
	got, err = svc.ResolveIssues(ctx, "/r", []string{"GAME-1"})
	require.NoError(t, err)
	require.Empty(t, got)

	d.err = errors.New("down")
	_, err = svc.ResolveIssues(ctx, "/r", []string{"GAME-1"})
	require.Error(t, err)
}
