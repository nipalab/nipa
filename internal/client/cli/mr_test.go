package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type fakeMRClient struct {
	connect    func(ctx context.Context, host string) error
	connectErr error

	createOrg, createProject, createTitle, createDescription, createSource, createTarget string
	createResult                                                                         *domain.MergeRequest
	createErr                                                                            error

	updateOrg, updateProject, updateID, updateTitle, updateDescription string
	updateResult                                                       *domain.MergeRequest
	updateErr                                                          error

	listOrg, listProject, listStatus string
	listLimit                        int
	listResult                       []*domain.MergeRequest
	listErr                          error

	mergeID      string
	mergeResult  *domain.MergeRequest
	mergeability *domain.Mergeability
	mergeErr     error

	closeID     string
	closeResult *domain.MergeRequest
	closeErr    error

	defaultBranch    *serverDomain.Branch
	defaultBranchErr error
}

func (f *fakeMRClient) Connect(ctx context.Context, host string) error {
	if f.connect != nil {
		return f.connect(ctx, host)
	}
	return f.connectErr
}

func (f *fakeMRClient) CreateMergeRequest(_ context.Context, org, project, title, description, sourceBranch, targetBranch string) (*domain.MergeRequest, error) {
	f.createOrg, f.createProject = org, project
	f.createTitle, f.createDescription = title, description
	f.createSource, f.createTarget = sourceBranch, targetBranch
	return f.createResult, f.createErr
}

func (f *fakeMRClient) UpdateMergeRequest(_ context.Context, org, project, id, title, description string) (*domain.MergeRequest, error) {
	f.updateOrg, f.updateProject = org, project
	f.updateID, f.updateTitle, f.updateDescription = id, title, description
	return f.updateResult, f.updateErr
}

func (f *fakeMRClient) ListMergeRequests(_ context.Context, org, project, status string, limit int) ([]*domain.MergeRequest, error) {
	f.listOrg, f.listProject = org, project
	f.listStatus, f.listLimit = status, limit
	return f.listResult, f.listErr
}

func (f *fakeMRClient) MergeMergeRequest(_ context.Context, _, _, id string) (*domain.MergeRequest, *domain.Mergeability, error) {
	f.mergeID = id
	return f.mergeResult, f.mergeability, f.mergeErr
}

func (f *fakeMRClient) CloseMergeRequest(_ context.Context, _, _, id string) (*domain.MergeRequest, error) {
	f.closeID = id
	return f.closeResult, f.closeErr
}

func (f *fakeMRClient) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return f.defaultBranch, f.defaultBranchErr
}

func newMRCli(t *testing.T, client *fakeMRClient) *Cli {
	t.Helper()

	auth := usecase.NewAuth(fakeExecutor{}, &fakeStorage{token: signTestJWT(t)}, &fakeInput{})
	return NewCli(&fakeUsecaseContainer{mr: usecase.NewMergeRequest(auth, client, localrepo.NewLocalRepo())}, &fakeConnector{})
}

func TestSetupMrCreateCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		createResult:  &domain.MergeRequest{ID: "abc", SourceBranch: "feature", TargetBranch: "main", Title: "Add b"},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "create", "--title", "Add b", "-d", "body")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request abc opened: feature -> main (Add b)")
	require.Equal(t, "org", client.createOrg)
	require.Equal(t, "project", client.createProject)
	require.Equal(t, "Add b", client.createTitle)
	require.Equal(t, "body", client.createDescription)
	require.Equal(t, "feature", client.createSource)
	require.Equal(t, "main", client.createTarget)
}

func TestSetupMrCreateCmd_ExplicitTarget(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{createResult: &domain.MergeRequest{ID: "abc"}}
	cli := newMRCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "create", "--title", "Add b", "--source", "feature", "--target", "release")
	require.NoError(t, err)
	require.Equal(t, "feature", client.createSource)
	require.Equal(t, "release", client.createTarget)
}

func TestSetupMrCreateCmd_Validation(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "create")
	require.EqualError(t, err, "a title is required")
}

func TestSetupMrCreateCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		createErr:     wantErr,
	})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "create", "--title", "Add b")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupMrUpdateCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{updateResult: &domain.MergeRequest{ID: "abc", Title: "Renamed"}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "update", "abc", "--title", "Renamed")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request abc updated: Renamed")
	require.Equal(t, "abc", client.updateID)
	require.Equal(t, "Renamed", client.updateTitle)
}

func TestSetupMrUpdateCmd_MissingFields(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "update", "abc")
	require.Contains(t, err.Error(), "pass --title or --description")
}

func TestSetupMrListCmd_Empty(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{})

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "list")
	require.NoError(t, err)
	require.Contains(t, out, "no merge requests")
}

func TestSetupMrListCmd_Rows(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{listResult: []*domain.MergeRequest{
		{ID: "abc", Status: domain.MergeRequestOpen, SourceBranch: "feature", TargetBranch: "main", Title: "Add b"},
		{ID: "def", Status: domain.MergeRequestClosed, SourceBranch: "fix", TargetBranch: "main", Title: "Fix a"},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "list", "--status", "open", "--limit", "5")
	require.NoError(t, err)
	require.Contains(t, out, "ID")
	require.Contains(t, out, "feature -> main")
	require.Contains(t, out, "Add b")
	require.Contains(t, out, "Fix a")
	require.Equal(t, domain.MergeRequestOpen, client.listStatus)
	require.Equal(t, 5, client.listLimit)
}

func TestSetupMrListCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{listErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "list")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupMrCloseCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{closeResult: &domain.MergeRequest{ID: "abc", Status: domain.MergeRequestClosed}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "close", "abc")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request abc closed.")
	require.Equal(t, "abc", client.closeID)
}

func TestSetupMrCloseCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{closeErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "close", "abc")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupMrMergeCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		mergeResult:  &domain.MergeRequest{ID: "abc", SourceBranch: "feature", TargetBranch: "main", Status: domain.MergeRequestMerged},
		mergeability: &domain.Mergeability{Status: "mergeable"},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "merge", "abc")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request abc merged: feature -> main (mergeable)")
	require.Equal(t, "abc", client.mergeID)
}

func TestSetupMrMergeCmd_Error(t *testing.T) {
	wantErr := &domain.Error{Code: 409, Message: "source branch is behind the target; update it first"}
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{mergeErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "merge", "abc")
	require.ErrorIs(t, err, wantErr)
}

func TestMr_NotARepo(t *testing.T) {
	cli := newMRCli(t, &fakeMRClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupMrCmd(), "list")
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
}
