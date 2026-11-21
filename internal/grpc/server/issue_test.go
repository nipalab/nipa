package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

const issueProject = snow.ID(42)

type issueFixture struct {
	repo *MockissueTrackerRepository
	perm *MockissueTrackerAccess
	jira *jiratest.Server
	uc   *usecase.IssueTracker
	box  interface {
		Seal(plain, aad []byte) ([]byte, error)
	}
}

func newIssueFixture(t *testing.T) *issueFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	box, err := usecase.NewIssueTrackerBox([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	jira := jiratest.New(jiratest.Options{})
	t.Cleanup(jira.Close)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	f := &issueFixture{repo: NewMockissueTrackerRepository(ctrl), perm: NewMockissueTrackerAccess(ctrl), jira: jira, box: box}
	f.uc = usecase.NewIssueTracker(f.repo, NewMockissueTrackerOrgs(ctrl), f.perm, box, node).
		WithFactory(func(kind string, cfg issuetracker.Config) (issuetracker.Tracker, error) {
			cfg.Transport = http.DefaultTransport
			return issuetracker.New(kind, cfg)
		})
	return f
}

// connected expects one lookup of the project's active settings and its
// connection.
func (f *issueFixture) connected(t *testing.T) {
	t.Helper()
	sealed, err := f.box.Seal([]byte("jira-test-token"), []byte("issue_tracker:"+snow.ID(9).Base36()))
	require.NoError(t, err)
	f.repo.EXPECT().GetProject(gomock.Any(), issueProject).Return(&domain.ProjectIssueTracker{
		ProjectID: issueProject, TrackerID: 9, Keys: []string{"GAME"}, IsActive: true,
	}, nil)
	f.repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(&domain.IssueTracker{
		ID: 9, OrgID: 1, Kind: "jira", Name: "Jira", BaseURL: f.jira.URL, AuthKind: issuetracker.AuthBasic,
		Account: "bot@example.com", SecretSealed: sealed, UpdatedAt: time.Unix(100, 0),
	}, nil)
}

func (f *issueFixture) reader(ok bool) {
	f.perm.EXPECT().HasProjectAccess(gomock.Any(), issueProject, domain.PermissionRead).Return(ok)
}

func (f *issueFixture) server() *nipaServer {
	return New(&mockUsecaseContainer{common: newTestCommon(), issues: f.uc})
}

func (f *issueFixture) links(kind string, ids []snow.ID, links ...domain.IssueLink) {
	f.repo.EXPECT().ListIssueLinksBySubjects(gomock.Any(), issueProject, kind, ids).Return(links, nil)
}

var issueCtx = &pb.ProjectContext{Org: "org", Project: "proj"}

func TestResolveIssues(t *testing.T) {
	f := newIssueFixture(t)
	f.jira.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "New rig", Type: "Story", Status: "In Progress", Assignee: "Ana"})
	srv := f.server()
	f.reader(true)
	f.connected(t)

	resp, err := srv.ResolveIssues(context.Background(), &pb.ResolveIssuesRequest{Context: issueCtx, Keys: []string{"game-1", "GAME-2", "OTHER-1"}})
	require.NoError(t, err)
	require.Len(t, resp.GetIssues(), 2)
	got := resp.GetIssues()[0]
	require.Equal(t, "GAME-1", got.GetKey())
	require.Equal(t, f.jira.URL+"/browse/GAME-1", got.GetUrl())
	require.Equal(t, usecase.IssueStateOK, got.GetState())
	require.Equal(t, "New rig", got.GetTitle())
	require.Equal(t, "Story", got.GetType())
	require.Equal(t, "In Progress", got.GetStatus())
	require.Equal(t, issuetracker.CategoryInProgress, got.GetStatusCategory())
	require.Equal(t, "Ana", got.GetAssignee())
	require.Equal(t, usecase.IssueStateNotFound, resp.GetIssues()[1].GetState())
	require.Empty(t, resp.GetIssues()[1].GetTitle())
}

func TestResolveIssues_Refusals(t *testing.T) {
	f := newIssueFixture(t)
	srv := f.server()

	_, err := srv.ResolveIssues(context.Background(), &pb.ResolveIssuesRequest{Context: &pb.ProjectContext{Org: "org", Project: "missing"}})
	require.Error(t, err)

	_, err = srv.ResolveIssues(context.Background(), &pb.ResolveIssuesRequest{Context: issueCtx, Keys: make([]string, usecase.MaxResolveIssueKeys+1)})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	f.reader(false)
	_, err = srv.ResolveIssues(context.Background(), &pb.ResolveIssuesRequest{Context: issueCtx, Keys: []string{"GAME-1"}})
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	f.reader(true)
	f.repo.EXPECT().GetProject(gomock.Any(), issueProject).Return(nil, domain.NewErrorNotFound("record not found"))
	_, err = srv.ResolveIssues(context.Background(), &pb.ResolveIssuesRequest{Context: issueCtx, Keys: []string{"GAME-1"}})
	require.Equal(t, codes.NotFound, status.Code(err))

	for _, tracker := range []*usecase.IssueTracker{nil, usecase.NewIssueTracker(nil, nil, nil, nil, nil)} {
		off := New(&mockUsecaseContainer{common: newTestCommon(), issues: tracker})
		_, err = off.ResolveIssues(context.Background(), &pb.ResolveIssuesRequest{Context: issueCtx, Keys: []string{"GAME-1"}})
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		_, err = off.SearchIssues(context.Background(), &pb.SearchIssuesRequest{Context: issueCtx, Query: "rig"})
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
	}
}

func TestSearchIssues(t *testing.T) {
	f := newIssueFixture(t)
	f.jira.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "New rig"})
	f.jira.AddIssue(jiratest.Issue{Key: "OTHER-2", Summary: "New rig elsewhere"})
	srv := f.server()
	f.reader(true)
	f.connected(t)

	resp, err := srv.SearchIssues(context.Background(), &pb.SearchIssuesRequest{Context: issueCtx, Query: "rig", Limit: 5})
	require.NoError(t, err)
	require.True(t, resp.GetSupported())
	require.False(t, resp.GetUnavailable())
	require.Len(t, resp.GetIssues(), 1)
	require.Equal(t, "GAME-1", resp.GetIssues()[0].GetKey())
	require.Equal(t, "New rig", resp.GetIssues()[0].GetTitle())

	f.jira.Fail(jiratest.Failure{Status: 500})
	f.reader(true)
	f.connected(t)
	resp, err = srv.SearchIssues(context.Background(), &pb.SearchIssuesRequest{Context: issueCtx, Query: "rig"})
	require.NoError(t, err)
	require.True(t, resp.GetUnavailable())
	require.Empty(t, resp.GetIssues())

	_, err = srv.SearchIssues(context.Background(), &pb.SearchIssuesRequest{Context: issueCtx, Query: " "})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = srv.SearchIssues(context.Background(), &pb.SearchIssuesRequest{Context: &pb.ProjectContext{Org: "nope", Project: "proj"}, Query: "rig"})
	require.Error(t, err)
}

func issueKey(f *issueFixture, key string) *pb.IssueKey {
	return &pb.IssueKey{Key: key, Url: f.jira.URL + "/browse/" + key}
}

func requireIssueKeys(t *testing.T, want []*pb.IssueKey, got []*pb.IssueKey) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].GetKey(), got[i].GetKey())
		require.Equal(t, want[i].GetUrl(), got[i].GetUrl())
	}
}

func TestIssueKeys_CommitLog(t *testing.T) {
	f := newIssueFixture(t)
	branch, perm, repo := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), issues: f.uc})
	head := snow.ID(99)
	perm.EXPECT().HasProjectAccess(gomock.Any(), issueProject, domain.PermissionRead).Return(true)
	repo.EXPECT().GetDefaultBranch(gomock.Any(), issueProject).
		Return(&domain.Branch{ID: 2, ProjectID: issueProject, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().CommitLog(gomock.Any(), issueProject, head, 50).Return([]*domain.CommitLogEntry{
		{Commit: domain.Commit{ID: 99, Message: "Fix GAME-1"}},
		{Commit: domain.Commit{ID: 98, Message: "plain"}},
	}, nil)
	f.connected(t)
	f.links(domain.IssueSubjectCommit, []snow.ID{99, 98},
		domain.IssueLink{SubjectID: 99, IssueKey: "GAME-1"},
		domain.IssueLink{SubjectID: 99, IssueKey: "OLD-3"},
	)

	resp, err := srv.GetCommitLog(context.Background(), &pb.GetCommitLogRequest{Context: issueCtx})
	require.NoError(t, err)
	require.Len(t, resp.GetCommits(), 2)
	requireIssueKeys(t, []*pb.IssueKey{issueKey(f, "GAME-1")}, resp.GetCommits()[0].GetIssueKeys())
	require.Empty(t, resp.GetCommits()[1].GetIssueKeys())
}

func TestIssueKeys_Branches(t *testing.T) {
	f := newIssueFixture(t)
	branch, perm, repo := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), issues: f.uc})
	perm.EXPECT().HasProjectAccess(gomock.Any(), issueProject, domain.PermissionRead).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), issueProject, 10, gomock.Any(), snow.ID(0)).Return([]*domain.Branch{
		{ID: 3, ProjectID: issueProject, Name: "GAME-7-rig"},
		{ID: 2, ProjectID: issueProject, Name: "main"},
	}, nil)
	f.connected(t)
	f.links(domain.IssueSubjectBranch, []snow.ID{3, 2}, domain.IssueLink{SubjectID: 3, IssueKey: "GAME-7"})

	resp, err := srv.GetListBranch(context.Background(), &pb.GetListBranchRequest{Context: issueCtx, Limit: 10})
	require.NoError(t, err)
	requireIssueKeys(t, []*pb.IssueKey{issueKey(f, "GAME-7")}, resp.GetBranches()[0].GetIssueKeys())
	require.Empty(t, resp.GetBranches()[1].GetIssueKeys())
}

func TestIssueKeys_MergeRequests(t *testing.T) {
	f := newIssueFixture(t)
	repo := &stubMergeRequestRepository{
		list: []*domain.MergeRequest{testMergeRequest(5, domain.MergeRequestOpen), testMergeRequest(4, domain.MergeRequestOpen)},
		onGet: func(int) (*domain.MergeRequest, error) {
			return testMergeRequest(5, domain.MergeRequestOpen), nil
		},
	}
	sourceHead, targetHead := snow.ID(11), snow.ID(12)
	srv, branchRepo, perm := newTestMergeRequestServer(t, repo, &stubBranchMerger{base: &usecase.MergeBaseInfo{MergeBaseCommitID: &targetHead}})
	srv.uc.(*mockUsecaseContainer).issues = f.uc
	perm.EXPECT().HasProjectAccess(gomock.Any(), issueProject, domain.PermissionRead).Return(true).AnyTimes()

	f.connected(t)
	f.links(domain.IssueSubjectMergeRequest, []snow.ID{5, 4}, domain.IssueLink{SubjectID: 4, IssueKey: "GAME-2"})
	list, err := srv.ListMergeRequests(context.Background(), &pb.ListMergeRequestsRequest{Context: issueCtx})
	require.NoError(t, err)
	require.Empty(t, list.GetMergeRequests()[0].GetIssueKeys())
	requireIssueKeys(t, []*pb.IssueKey{issueKey(f, "GAME-2")}, list.GetMergeRequests()[1].GetIssueKeys())

	branchRepo.EXPECT().GetBranchByName(gomock.Any(), issueProject, "feature").
		Return(&domain.Branch{ID: 3, ProjectID: issueProject, CommitID: &sourceHead}, nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), issueProject, "main").
		Return(&domain.Branch{ID: 2, ProjectID: issueProject, CommitID: &targetHead}, nil).AnyTimes()
	f.connected(t)
	f.links(domain.IssueSubjectMergeRequest, []snow.ID{5}, domain.IssueLink{SubjectID: 5, IssueKey: "GAME-1"}, domain.IssueLink{SubjectID: 5, IssueKey: "GAME-3"})
	got, err := srv.GetMergeRequest(context.Background(), &pb.GetMergeRequestRequest{Context: issueCtx, Number: 5})
	require.NoError(t, err)
	requireIssueKeys(t, []*pb.IssueKey{issueKey(f, "GAME-1"), issueKey(f, "GAME-3")}, got.GetMergeRequest().GetIssueKeys())

	branchRepo.EXPECT().CommitLogUntil(gomock.Any(), issueProject, sourceHead, targetHead, 250).
		Return([]*domain.CommitLogEntry{{Commit: domain.Commit{ID: sourceHead, Message: "GAME-1 rig"}}}, nil)
	f.connected(t)
	f.repo.EXPECT().ListIssueLinksBySubjects(gomock.Any(), issueProject, domain.IssueSubjectCommit, []snow.ID{sourceHead}).Return(nil, errors.New("db down"))
	commits, err := srv.ListMergeRequestCommits(context.Background(), &pb.ListMergeRequestCommitsRequest{Context: issueCtx, Number: 5})
	require.NoError(t, err)
	require.Len(t, commits.GetCommits(), 1)
	require.Empty(t, commits.GetCommits()[0].GetIssueKeys(), "a failed lookup only hides the keys")
}
