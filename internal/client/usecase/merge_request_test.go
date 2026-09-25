package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type stubMRCreateCall struct {
	org, project, title, description, source, target string
}

type stubMRClient struct {
	connectHost string
	connectErr  error

	createCalls  []stubMRCreateCall
	createResult *domain.MergeRequest
	createErr    error

	updateNumber      int64
	updateTitle       string
	updateDescription string
	updateResult      *domain.MergeRequest
	updateErr         error

	listStatus string
	listLimit  int
	listResult []*domain.MergeRequest
	listErr    error

	closeNumber int64
	closeResult *domain.MergeRequest
	closeErr    error

	mergeNumber  int64
	mergeResult  *domain.MergeRequest
	mergeability *domain.Mergeability
	mergeErr     error

	defaultBranch    *serverDomain.Branch
	defaultBranchErr error
}

func (s *stubMRClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubMRClient) CreateMergeRequest(_ context.Context, org, project, title, description, sourceBranch, targetBranch string) (*domain.MergeRequest, error) {
	s.createCalls = append(s.createCalls, stubMRCreateCall{
		org: org, project: project, title: title, description: description,
		source: sourceBranch, target: targetBranch,
	})
	return s.createResult, s.createErr
}

func (s *stubMRClient) UpdateMergeRequest(_ context.Context, _, _ string, number int64, title, description string) (*domain.MergeRequest, error) {
	s.updateNumber, s.updateTitle, s.updateDescription = number, title, description
	return s.updateResult, s.updateErr
}

func (s *stubMRClient) ListMergeRequests(_ context.Context, _, _, status string, limit int) ([]*domain.MergeRequest, error) {
	s.listStatus, s.listLimit = status, limit
	return s.listResult, s.listErr
}

func (s *stubMRClient) MergeMergeRequest(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, *domain.Mergeability, error) {
	s.mergeNumber = number
	return s.mergeResult, s.mergeability, s.mergeErr
}

func (s *stubMRClient) CloseMergeRequest(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, error) {
	s.closeNumber = number
	return s.closeResult, s.closeErr
}

func (s *stubMRClient) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return s.defaultBranch, s.defaultBranchErr
}

func newTestMergeRequest(t *testing.T, local mrLocalRepo, client mrClient) *MergeRequest {
	t.Helper()
	token := signTestToken(t, "secret")
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: token}}
	auth := NewAuth(nil, storage, nil)
	return NewMergeRequest(auth, client, local)
}

func TestMergeRequest_Create_Defaults(t *testing.T) {
	root := t.TempDir()
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		createResult:  &domain.MergeRequest{Number: 1, SourceBranch: "feature", TargetBranch: "main", Title: "T"},
	}
	mr, err := newTestMergeRequest(t, local, client).Create(context.Background(), root, CreateMergeRequestOptions{
		Title: " T ",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), mr.Number)
	require.Equal(t, root, local.initTarget)
	require.Equal(t, "example.com", client.connectHost)
	require.Len(t, client.createCalls, 1)
	call := client.createCalls[0]
	require.Equal(t, "org", call.org)
	require.Equal(t, "project", call.project)
	require.Equal(t, "T", call.title)
	require.Equal(t, "feature", call.source)
	require.Equal(t, "main", call.target)
}

func TestMergeRequest_Create_ExplicitTarget(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{createResult: &domain.MergeRequest{Number: 1}}
	_, err := newTestMergeRequest(t, local, client).Create(context.Background(), t.TempDir(), CreateMergeRequestOptions{
		Title:  "T",
		Source: "feature",
		Target: "release",
	})
	require.NoError(t, err)
	require.Len(t, client.createCalls, 1)
	require.Equal(t, "feature", client.createCalls[0].source)
	require.Equal(t, "release", client.createCalls[0].target)
}

func TestMergeRequest_Create_Validation(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{defaultBranch: &serverDomain.Branch{Name: "main"}}
	mr := newTestMergeRequest(t, local, client)

	_, err := mr.Create(context.Background(), t.TempDir(), CreateMergeRequestOptions{Title: "  "})
	require.EqualError(t, err, "a title is required")

	_, err = mr.Create(context.Background(), t.TempDir(), CreateMergeRequestOptions{
		Title: "T", Source: "main", Target: "main",
	})
	require.Contains(t, err.Error(), "must differ")
	require.Empty(t, client.createCalls)
}

func TestMergeRequest_Create_DefaultBranchError(t *testing.T) {
	wantErr := errors.New("no default branch")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{defaultBranchErr: wantErr}

	_, err := newTestMergeRequest(t, local, client).Create(context.Background(), t.TempDir(), CreateMergeRequestOptions{Title: "T"})
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Create_ClientError(t *testing.T) {
	wantErr := &domain.Error{Code: 409, Message: "branch is protected"}
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{defaultBranch: &serverDomain.Branch{Name: "main"}, createErr: wantErr}

	_, err := newTestMergeRequest(t, local, client).Create(context.Background(), t.TempDir(), CreateMergeRequestOptions{Title: "T"})
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_Update(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{updateResult: &domain.MergeRequest{Number: 1, Title: "New"}}
	mr := newTestMergeRequest(t, local, client)

	_, err := mr.Update(context.Background(), t.TempDir(), "!!!", "New", "")
	require.Contains(t, err.Error(), "invalid merge request number")

	_, err = mr.Update(context.Background(), t.TempDir(), "1", "  ", "")
	require.Contains(t, err.Error(), "pass --title or --description")

	updated, err := mr.Update(context.Background(), t.TempDir(), "1", " New ", "")
	require.NoError(t, err)
	require.Equal(t, "New", updated.Title)
	require.Equal(t, int64(1), client.updateNumber)
	require.Equal(t, " New ", client.updateTitle)
}

func TestMergeRequest_List(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{listResult: []*domain.MergeRequest{{Number: 1}}}
	mr := newTestMergeRequest(t, local, client)

	_, err := mr.List(context.Background(), t.TempDir(), "bogus", 0)
	require.Contains(t, err.Error(), "status must be one of")

	requests, err := mr.List(context.Background(), t.TempDir(), "", 0)
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, 50, client.listLimit)

	_, err = mr.List(context.Background(), t.TempDir(), domain.MergeRequestClosed, 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, client.listStatus)
	require.Equal(t, 5, client.listLimit)
}

func TestMergeRequest_Close(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{closeResult: &domain.MergeRequest{Number: 1, Status: domain.MergeRequestClosed}}
	mr := newTestMergeRequest(t, local, client)

	_, err := mr.Close(context.Background(), t.TempDir(), "")
	require.Contains(t, err.Error(), "a merge request number is required")

	closed, err := mr.Close(context.Background(), t.TempDir(), "1")
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, closed.Status)
	require.Equal(t, int64(1), client.closeNumber)
}

func TestMergeRequest_Merge(t *testing.T) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{
		mergeResult:  &domain.MergeRequest{Number: 1, Status: domain.MergeRequestMerged},
		mergeability: &domain.Mergeability{Status: "mergeable"},
	}
	mr := newTestMergeRequest(t, local, client)

	_, _, err := mr.Merge(context.Background(), t.TempDir(), "nope!")
	require.Contains(t, err.Error(), "invalid merge request number")

	merged, info, err := mr.Merge(context.Background(), t.TempDir(), "1")
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestMerged, merged.Status)
	require.Equal(t, "mergeable", info.Status)
	require.Equal(t, int64(1), client.mergeNumber)
}

func TestMergeRequest_ConnectError(t *testing.T) {
	wantErr := errors.New("dial failed")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	client := &stubMRClient{connectErr: wantErr}

	_, err := newTestMergeRequest(t, local, client).List(context.Background(), t.TempDir(), "", 0)
	require.ErrorIs(t, err, wantErr)
}
