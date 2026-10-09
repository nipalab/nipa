package cli

import (
	"context"
	"errors"
	"testing"
	"time"

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
	createDraft                                                                          bool
	createResult                                                                         *domain.MergeRequest
	createErr                                                                            error

	updateOrg, updateProject, updateTitle, updateDescription string
	updateNumber                                             int64
	updateDraft                                              *bool
	updateResult                                             *domain.MergeRequest
	updateErr                                                error

	listOrg, listProject, listStatus string
	listLimit                        int
	listResult                       []*domain.MergeRequest
	listCursor                       int64
	listOpts                         domain.ListMergeRequestOptions
	listErr                          error

	mergeNumber       int64
	mergeStrategy     string
	mergeDeleteSource bool

	checkName      string
	checkState     string
	checkURL       string
	reportCheck    *domain.MergeRequestCheck
	reportErr      error
	checksResult   []*domain.MergeRequestCheck
	checksErr      error
	assigneeIDs    []string
	assigneeResult *domain.MergeRequest
	assigneeErr    error
	mergeResult    *domain.MergeRequest
	mergeability   *domain.Mergeability
	mergeErr       error

	closeNumber int64
	closeResult *domain.MergeRequest
	closeErr    error

	getResult       *domain.MergeRequest
	getMergeability *domain.Mergeability
	getErr          error

	reopenResult *domain.MergeRequest
	reopenErr    error

	checkResult *domain.Mergeability
	checkErr    error

	diffResult []*domain.MergeRequestDiffFile
	diffErr    error

	commitsResult []*serverDomain.CommitLogEntry
	commitsErr    error

	reviewsResult []*domain.MergeRequestReview
	reviewsErr    error

	submitResult *domain.MergeRequestReview
	submitState  string
	submitBody   string
	submitErr    error

	threadsResult []*domain.MergeRequestThread
	threadsErr    error

	commentResult  *domain.MergeRequestThread
	commentFile    string
	commentNewLine *int64
	commentOldLine *int64
	commentBody    string
	commentErr     error

	replyResult *domain.MergeRequestComment
	replyBody   string
	replyErr    error

	resolveResult   *domain.MergeRequestThread
	resolveResolved bool
	resolveErr      error

	timelineResult []*domain.MergeRequestTimelineItem
	timelineErr    error

	requestsResult []*domain.MergeRequestReviewRequest
	requestsErr    error

	requestResult    *domain.MergeRequestReviewRequest
	requestUserID    string
	requestErr       error
	removeRequestErr error

	defaultBranch    *serverDomain.Branch
	defaultBranchErr error
}

func (f *fakeMRClient) Connect(ctx context.Context, host string) error {
	if f.connect != nil {
		return f.connect(ctx, host)
	}
	return f.connectErr
}

func (f *fakeMRClient) CreateMergeRequest(_ context.Context, org, project, title, description, sourceBranch, targetBranch string, draft bool) (*domain.MergeRequest, error) {
	f.createOrg, f.createProject = org, project
	f.createTitle, f.createDescription = title, description
	f.createSource, f.createTarget = sourceBranch, targetBranch
	f.createDraft = draft
	return f.createResult, f.createErr
}

func (f *fakeMRClient) UpdateMergeRequest(_ context.Context, org, project string, number int64, title, description string, draft *bool) (*domain.MergeRequest, error) {
	f.updateOrg, f.updateProject = org, project
	f.updateNumber, f.updateTitle, f.updateDescription = number, title, description
	f.updateDraft = draft
	return f.updateResult, f.updateErr
}

func (f *fakeMRClient) ListMergeRequests(_ context.Context, org, project string, opts domain.ListMergeRequestOptions) ([]*domain.MergeRequest, int64, error) {
	f.listOrg, f.listProject = org, project
	f.listStatus, f.listLimit, f.listOpts = opts.Status, opts.Limit, opts
	return f.listResult, f.listCursor, f.listErr
}

func (f *fakeMRClient) MergeMergeRequest(_ context.Context, _, _ string, number int64, strategy string, deleteSource bool) (*domain.MergeRequest, *domain.Mergeability, error) {
	f.mergeNumber = number
	f.mergeStrategy = strategy
	f.mergeDeleteSource = deleteSource
	return f.mergeResult, f.mergeability, f.mergeErr
}

func (f *fakeMRClient) CloseMergeRequest(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, error) {
	f.closeNumber = number
	return f.closeResult, f.closeErr
}

func (f *fakeMRClient) SetMergeRequestAssignees(_ context.Context, _, _ string, _ int64, userIDs []string) (*domain.MergeRequest, error) {
	f.assigneeIDs = userIDs
	return f.assigneeResult, f.assigneeErr
}

func (f *fakeMRClient) GetDefaultBranch(_ context.Context, _, _ string) (*serverDomain.Branch, error) {
	return f.defaultBranch, f.defaultBranchErr
}

func (f *fakeMRClient) GetMergeRequest(_ context.Context, _, _ string, _ int64) (*domain.MergeRequest, *domain.Mergeability, error) {
	return f.getResult, f.getMergeability, f.getErr
}

func (f *fakeMRClient) ReopenMergeRequest(_ context.Context, _, _ string, _ int64) (*domain.MergeRequest, error) {
	return f.reopenResult, f.reopenErr
}

func (f *fakeMRClient) CheckMergeRequest(_ context.Context, _, _ string, _ int64) (*domain.Mergeability, error) {
	return f.checkResult, f.checkErr
}

func (f *fakeMRClient) ListMergeRequestCommits(_ context.Context, _, _ string, _ int64) ([]*serverDomain.CommitLogEntry, error) {
	return f.commitsResult, f.commitsErr
}

func (f *fakeMRClient) GetMergeRequestDiff(_ context.Context, _, _ string, _ int64) ([]*domain.MergeRequestDiffFile, error) {
	return f.diffResult, f.diffErr
}

func (f *fakeMRClient) ListMergeRequestReviews(_ context.Context, _, _ string, _ int64) ([]*domain.MergeRequestReview, error) {
	return f.reviewsResult, f.reviewsErr
}

func (f *fakeMRClient) ReportMergeRequestCheck(_ context.Context, _, _ string, _ int64, name, state, detailsURL string) (*domain.MergeRequestCheck, error) {
	f.checkName, f.checkState, f.checkURL = name, state, detailsURL
	return f.reportCheck, f.reportErr
}

func (f *fakeMRClient) ListMergeRequestChecks(_ context.Context, _, _ string, _ int64) ([]*domain.MergeRequestCheck, error) {
	return f.checksResult, f.checksErr
}

func (f *fakeMRClient) SubmitMergeRequestReview(_ context.Context, _, _ string, _ int64, state, body string) (*domain.MergeRequestReview, error) {
	f.submitState, f.submitBody = state, body
	return f.submitResult, f.submitErr
}

func (f *fakeMRClient) ListMergeRequestThreads(_ context.Context, _, _ string, _ int64) ([]*domain.MergeRequestThread, error) {
	return f.threadsResult, f.threadsErr
}

func (f *fakeMRClient) AddMergeRequestComment(_ context.Context, _, _ string, _ int64, filePath string, oldLine, newLine *int64, body string) (*domain.MergeRequestThread, error) {
	f.commentFile, f.commentOldLine, f.commentNewLine, f.commentBody = filePath, oldLine, newLine, body
	return f.commentResult, f.commentErr
}

func (f *fakeMRClient) ReplyMergeRequestThread(_ context.Context, _, _ string, _ int64, _, body string) (*domain.MergeRequestComment, error) {
	f.replyBody = body
	return f.replyResult, f.replyErr
}

func (f *fakeMRClient) ResolveMergeRequestThread(_ context.Context, _, _ string, _ int64, _ string, resolved bool) (*domain.MergeRequestThread, error) {
	f.resolveResolved = resolved
	return f.resolveResult, f.resolveErr
}

func (f *fakeMRClient) ListMergeRequestTimeline(_ context.Context, _, _ string, _ int64) ([]*domain.MergeRequestTimelineItem, error) {
	return f.timelineResult, f.timelineErr
}

func (f *fakeMRClient) ListMergeRequestReviewRequests(_ context.Context, _, _ string, _ int64) ([]*domain.MergeRequestReviewRequest, error) {
	return f.requestsResult, f.requestsErr
}

func (f *fakeMRClient) RequestMergeRequestReview(_ context.Context, _, _ string, _ int64, userID string) (*domain.MergeRequestReviewRequest, error) {
	f.requestUserID = userID
	return f.requestResult, f.requestErr
}

func (f *fakeMRClient) RemoveMergeRequestReviewRequest(_ context.Context, _, _ string, _ int64, userID string) error {
	f.requestUserID = userID
	return f.removeRequestErr
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
		createResult:  &domain.MergeRequest{Number: 1, SourceBranch: "feature", TargetBranch: "main", Title: "Add b"},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "create", "--title", "Add b", "-d", "body")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request #1 opened: feature -> main (Add b)")
	require.Equal(t, "org", client.createOrg)
	require.Equal(t, "project", client.createProject)
	require.Equal(t, "Add b", client.createTitle)
	require.Equal(t, "body", client.createDescription)
	require.Equal(t, "feature", client.createSource)
	require.Equal(t, "main", client.createTarget)
}

func TestSetupMrCreateCmd_ExplicitTarget(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{createResult: &domain.MergeRequest{Number: 1}}
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

func TestSetupMrCreateCmd_Draft(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		defaultBranch: &serverDomain.Branch{Name: "main"},
		createResult:  &domain.MergeRequest{Number: 1, SourceBranch: "feature", TargetBranch: "main", Title: "Add b", Draft: true},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "create", "--title", "Add b", "--draft")
	require.NoError(t, err)
	require.True(t, client.createDraft)
	require.Contains(t, out, "[draft]")
}

func TestSetupMrReadyCmd(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{updateResult: &domain.MergeRequest{Number: 2}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "ready", "2")
	require.NoError(t, err)
	require.Contains(t, out, "ready for review")
	require.NotNil(t, client.updateDraft)
	require.False(t, *client.updateDraft)
}

func TestSetupMrReadyCmd_Errors(t *testing.T) {
	cli := newMRCli(t, &fakeMRClient{})
	_, err := runCmdInDir(t, t.TempDir(), cli.setupMrCmd(), "ready", "1")
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")

	wantErr := errors.New("boom")
	root := setupRepo(t, "feature")
	cli = newMRCli(t, &fakeMRClient{updateErr: wantErr})
	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "ready", "1")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupMrUpdateCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{updateResult: &domain.MergeRequest{Number: 1, Title: "Renamed"}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "update", "1", "--title", "Renamed")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request #1 updated: Renamed")
	require.Equal(t, int64(1), client.updateNumber)
	require.Equal(t, "Renamed", client.updateTitle)
}

func TestSetupMrUpdateCmd_MissingFields(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "update", "1")
	require.Contains(t, err.Error(), "pass --title, --description or --draft")
}

func TestSetupMrUpdateCmd_Draft(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{updateResult: &domain.MergeRequest{Number: 1, Title: "Renamed"}}
	cli := newMRCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "update", "1", "--draft")
	require.NoError(t, err)
	require.NotNil(t, client.updateDraft)
	require.True(t, *client.updateDraft)

	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "update", "1", "--draft=false")
	require.NoError(t, err)
	require.NotNil(t, client.updateDraft)
	require.False(t, *client.updateDraft)
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
	client := &fakeMRClient{
		listResult: []*domain.MergeRequest{
			{Number: 1, Status: domain.MergeRequestOpen, SourceBranch: "feature", TargetBranch: "main", Title: "Add b"},
			{Number: 2, Status: domain.MergeRequestClosed, SourceBranch: "fix", TargetBranch: "main", Title: "Fix a"},
		},
		listCursor: 2,
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "list",
		"--status", "open", "--limit", "5", "--author", "user1",
		"--source", "feature", "--target", "main", "--after", "9")
	require.NoError(t, err)
	require.Contains(t, out, "#")
	require.Contains(t, out, "feature -> main")
	require.Contains(t, out, "Add b")
	require.Contains(t, out, "Fix a")
	require.Contains(t, out, "more results: --after 2")
	require.Equal(t, domain.MergeRequestOpen, client.listStatus)
	require.Equal(t, 5, client.listLimit)
	require.Equal(t, "user1", client.listOpts.Author)
	require.Equal(t, "feature", client.listOpts.SourceBranch)
	require.Equal(t, "main", client.listOpts.TargetBranch)
	require.Equal(t, int64(9), client.listOpts.After)

	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "list", "--json")
	require.NoError(t, err)
	require.Contains(t, out, `"next_cursor":"2"`)
}

func TestSetupMrListCmd_DraftFilter(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{listResult: []*domain.MergeRequest{
		{Number: 3, Status: domain.MergeRequestOpen, Draft: true, SourceBranch: "feature", TargetBranch: "main", Title: "WIP"},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "list", "--status", "draft")
	require.NoError(t, err)
	require.Contains(t, out, "draft")
	require.Contains(t, out, "WIP")
	require.Equal(t, domain.MergeRequestOpen, client.listStatus)
	require.NotNil(t, client.listOpts.Draft)
	require.True(t, *client.listOpts.Draft)
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
	client := &fakeMRClient{closeResult: &domain.MergeRequest{Number: 1, Status: domain.MergeRequestClosed}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "close", "1")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request #1 closed.")
	require.Equal(t, int64(1), client.closeNumber)
}

func TestSetupMrCloseCmd_Error(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{closeErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "close", "1")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupMrMergeCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		mergeResult:  &domain.MergeRequest{Number: 1, SourceBranch: "feature", TargetBranch: "main", Status: domain.MergeRequestMerged},
		mergeability: &domain.Mergeability{Status: "mergeable"},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "merge", "1")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request #1 merged: feature -> main (mergeable)")
	require.Equal(t, int64(1), client.mergeNumber)
}

func TestSetupMrMergeCmd_Error(t *testing.T) {
	wantErr := &domain.Error{Code: 409, Message: "source branch is behind the target; update it first"}
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{mergeErr: wantErr})

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "merge", "1")
	require.ErrorIs(t, err, wantErr)
}

func TestMr_NotARepo(t *testing.T) {
	cli := newMRCli(t, &fakeMRClient{})

	_, err := runCmdInDir(t, t.TempDir(), cli.setupMrCmd(), "list")
	require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
}

func TestSetupMrViewCmd_Success(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		getResult: &domain.MergeRequest{
			Number: 7, Title: "Change code", Status: domain.MergeRequestOpen,
			SourceBranch: "feature", TargetBranch: "main", CreatedBy: "alice",
			Review: &domain.MergeRequestReviewState{Approvals: 1, OutstandingReviewers: []string{"u2"}},
		},
		getMergeability: &domain.Mergeability{Status: "mergeable", BlockedBy: "changes_requested"},
		reviewsResult: []*domain.MergeRequestReview{
			{Reviewer: domain.ReviewActor{Name: "Rev"}, State: domain.MergeRequestReviewChangesRequested},
			{Reviewer: domain.ReviewActor{Name: "Bob"}, State: domain.MergeRequestReviewApproved, Stale: true},
		},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "view", "7")
	require.NoError(t, err)
	require.Contains(t, out, "#7 Change code")
	require.Contains(t, out, "status: open · mergeable (blocked by changes_requested)")
	require.Contains(t, out, "branches: feature -> main")
	require.Contains(t, out, "approvals: 1 · changes requested: 0 · outstanding reviewers: 1")
	require.Contains(t, out, "Rev changes_requested")
	require.Contains(t, out, "Bob approved (stale)")
}

func TestSetupMrViewCmd_Draft(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		getResult: &domain.MergeRequest{Number: 8, Title: "WIP", Status: domain.MergeRequestOpen, Draft: true},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "view", "8")
	require.NoError(t, err)
	require.Contains(t, out, "status: open (draft)")
}

func TestSetupMrViewCmd_JSON(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		getResult:       &domain.MergeRequest{Number: 7, Title: "Change code", Status: domain.MergeRequestOpen},
		getMergeability: &domain.Mergeability{Status: "mergeable"},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "view", "7", "--json")
	require.NoError(t, err)
	require.Contains(t, out, `"merge_request"`)
	require.Contains(t, out, `"mergeability"`)
	require.Contains(t, out, `"mergeable"`)
}

func TestSetupMrReviewCmd_Decisions(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{submitResult: &domain.MergeRequestReview{State: domain.MergeRequestReviewApproved}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "review", "7", "--approve", "-m", "lgtm")
	require.NoError(t, err)
	require.Contains(t, out, "reviewed: approved")
	require.Equal(t, domain.MergeRequestReviewApproved, client.submitState)
	require.Equal(t, "lgtm", client.submitBody)

	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "review", "7", "--request-changes", "-m", "fix")
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestReviewChangesRequested, client.submitState)

	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "review", "7", "-m", "note")
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestReviewCommented, client.submitState)

	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "review", "7", "--approve", "--request-changes", "-m", "x")
	require.Contains(t, err.Error(), "not both")

	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "review", "7", "--approve")
	require.Contains(t, err.Error(), "needs a message")
}

func TestSetupMrCommentsCmd_Rows(t *testing.T) {
	root := setupRepo(t, "feature")
	newLine := int64(2)
	client := &fakeMRClient{threadsResult: []*domain.MergeRequestThread{
		{
			ID: "t1", FilePath: "code.txt", NewLine: &newLine,
			CreatedBy: domain.ReviewActor{Name: "Alice"},
			Comments:  []*domain.MergeRequestComment{{User: domain.ReviewActor{Name: "Rev"}, Body: "rename this"}},
		},
		{ID: "t2", Resolved: true, CreatedBy: domain.ReviewActor{Name: "Bob"}},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "comments", "7")
	require.NoError(t, err)
	require.Contains(t, out, "thread t1 (code.txt:2, open)")
	require.Contains(t, out, "Rev: rename this")
	require.Contains(t, out, "thread t2 (top-level, resolved)")
}

func TestSetupMrCommentCmd_Anchoring(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{commentResult: &domain.MergeRequestThread{ID: "t9"}}
	cli := newMRCli(t, client)

	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "comment", "7", "-m", "x", "--new-line", "2")
	require.Contains(t, err.Error(), "pass --file")

	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "comment", "7", "-m", "x", "--file", "code.txt")
	require.Contains(t, err.Error(), "pass --new-line or --old-line")

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "comment", "7", "-m", " note ", "--file", "code.txt", "--new-line", "2")
	require.NoError(t, err)
	require.Contains(t, out, "thread t9 opened")
	require.Equal(t, "code.txt", client.commentFile)
	require.Equal(t, "note", client.commentBody)
	require.NotNil(t, client.commentNewLine)
	require.Nil(t, client.commentOldLine)

	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "comment", "7", "-m", "x", "--file", "code.txt", "--old-line", "3")
	require.NoError(t, err)
	require.NotNil(t, client.commentOldLine)
	require.Equal(t, int64(3), *client.commentOldLine)
	require.Nil(t, client.commentNewLine)
	require.Contains(t, out, "thread t9 opened")
}

func TestSetupMrReplyAndResolve(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		replyResult:   &domain.MergeRequestComment{ID: "c1", ThreadID: "t1"},
		resolveResult: &domain.MergeRequestThread{ID: "t1", Resolved: true},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "reply", "7", "t1", "-m", "done")
	require.NoError(t, err)
	require.Contains(t, out, "comment c1 added to thread t1")
	require.Equal(t, "done", client.replyBody)

	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "resolve", "7", "t1")
	require.NoError(t, err)
	require.Contains(t, out, "thread t1 resolved")
	require.True(t, client.resolveResolved)

	client.resolveResult = &domain.MergeRequestThread{ID: "t1", Resolved: false}
	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "resolve", "7", "t1", "--unresolve")
	require.NoError(t, err)
	require.Contains(t, out, "thread t1 reopened")
	require.False(t, client.resolveResolved)
}

func TestSetupMrRequestsAndRequestReview(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		requestsResult: []*domain.MergeRequestReviewRequest{{
			ID:          "r1",
			Reviewer:    domain.ReviewActor{Name: "Rev"},
			RequestedBy: domain.ReviewActor{Name: "Alice"},
		}},
		requestResult: &domain.MergeRequestReviewRequest{Reviewer: domain.ReviewActor{Name: "Rev"}},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "requests", "7")
	require.NoError(t, err)
	require.Contains(t, out, "requested from Rev by Alice")

	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "request-review", "7", "rev-id")
	require.NoError(t, err)
	require.Contains(t, out, "review requested from Rev")
	require.Equal(t, "rev-id", client.requestUserID)

	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "unrequest-review", "7", "rev-id")
	require.NoError(t, err)
	require.Contains(t, out, "review request for rev-id removed")
}

func TestSetupMrDiffCmd(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{diffResult: []*domain.MergeRequestDiffFile{
		{Path: "a.txt", Status: "M", Additions: 1, Deletions: 1, Patch: []string{"@@ -1 +1 @@", "-one", "+two"}},
		{Path: "new.txt", OldPath: "old.txt", Status: "R", Additions: 0, Deletions: 0},
		{Path: "b.bin", Status: "A", Binary: true},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "diff", "7")
	require.NoError(t, err)
	require.Contains(t, out, "M a.txt (+1 -1)")
	require.Contains(t, out, "+two")
	require.Contains(t, out, "R old.txt -> new.txt (+0 -0)")
	require.Contains(t, out, "binary file")
}

func TestSetupMrReopenCmd(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{reopenResult: &domain.MergeRequest{Number: 7}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "reopen", "7")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request #7 reopened.")
}

func TestSetupMrTimelineCmd(t *testing.T) {
	root := setupRepo(t, "feature")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	client := &fakeMRClient{timelineResult: []*domain.MergeRequestTimelineItem{
		{Kind: "opened", Actor: domain.ReviewActor{Name: "Alice"}, CreatedAt: at},
		{Kind: "review_requested", Actor: domain.ReviewActor{Name: "Alice"}, Subject: &domain.ReviewActor{Name: "Rev"}, CreatedAt: at},
		{Kind: "pushed", Actor: domain.ReviewActor{Name: "Alice"}, Body: "one commit", CreatedAt: at},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "timeline", "7")
	require.NoError(t, err)
	require.Contains(t, out, "opened  Alice")
	require.Contains(t, out, "review_requested  Alice -> Rev")
	require.Contains(t, out, "pushed  Alice: one commit")
}

func TestMrReviewCommands_ErrorPaths(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "feature")
	client := &fakeMRClient{
		getErr: wantErr, reopenErr: wantErr, threadsErr: wantErr, replyErr: wantErr,
		resolveErr: wantErr, timelineErr: wantErr, requestsErr: wantErr,
		requestErr: wantErr, removeRequestErr: wantErr, diffErr: wantErr, commentErr: wantErr,
	}
	cli := newMRCli(t, client)

	calls := map[string][]string{
		"view":             {"view", "7"},
		"reopen":           {"reopen", "7"},
		"comments":         {"comments", "7"},
		"comment":          {"comment", "7", "-m", "x"},
		"reply":            {"reply", "7", "t1", "-m", "x"},
		"resolve":          {"resolve", "7", "t1"},
		"timeline":         {"timeline", "7"},
		"requests":         {"requests", "7"},
		"request-review":   {"request-review", "7", "u1"},
		"unrequest-review": {"unrequest-review", "7", "u1"},
		"diff":             {"diff", "7"},
	}
	for name, args := range calls {
		t.Run(name, func(t *testing.T) {
			_, err := runCmdInDir(t, root, cli.setupMrCmd(), args...)
			require.ErrorIs(t, err, wantErr)
		})
	}
}

func TestSetupMrViewCmd_ReviewsAndCommitsError(t *testing.T) {
	wantErr := errors.New("boom")
	root := setupRepo(t, "feature")

	reviewsErr := &fakeMRClient{
		getResult:  &domain.MergeRequest{Number: 7, Status: domain.MergeRequestOpen},
		reviewsErr: wantErr,
	}
	cli := newMRCli(t, reviewsErr)
	_, err := runCmdInDir(t, root, cli.setupMrCmd(), "view", "7")
	require.ErrorIs(t, err, wantErr)

	commitsErr := &fakeMRClient{
		getResult:  &domain.MergeRequest{Number: 7, Status: domain.MergeRequestOpen},
		commitsErr: wantErr,
	}
	cli = newMRCli(t, commitsErr)
	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "view", "7")
	require.ErrorIs(t, err, wantErr)
}

func TestSetupMrViewCmd_DescriptionAndDismissedReview(t *testing.T) {
	root := setupRepo(t, "feature")
	dismissedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	client := &fakeMRClient{
		getResult: &domain.MergeRequest{
			Number: 7, Title: "T", Status: domain.MergeRequestOpen, Description: "the body",
		},
		reviewsResult: []*domain.MergeRequestReview{{
			Reviewer:        domain.ReviewActor{UserID: "u9"},
			State:           domain.MergeRequestReviewApproved,
			DismissedAt:     &dismissedAt,
			DismissedReason: "new_commits",
		}},
	}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "view", "7")
	require.NoError(t, err)
	require.Contains(t, out, "u9 approved (dismissed: new_commits)")
	require.Contains(t, out, "the body")
}

func TestMrReviewCommands_EmptyOutput(t *testing.T) {
	root := setupRepo(t, "feature")
	cli := newMRCli(t, &fakeMRClient{})

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"comments", []string{"comments", "7"}, "no comments"},
		{"timeline", []string{"timeline", "7"}, "no activity"},
		{"requests", []string{"requests", "7"}, "no review requests"},
		{"diff", []string{"diff", "7"}, "no changes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCmdInDir(t, root, cli.setupMrCmd(), tc.args...)
			require.NoError(t, err)
			require.Contains(t, out, tc.want)
		})
	}
}

func TestMrReviewCommands_JSON(t *testing.T) {
	root := setupRepo(t, "feature")
	newLine := int64(2)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	client := &fakeMRClient{
		threadsResult:  []*domain.MergeRequestThread{{ID: "t1", NewLine: &newLine}},
		timelineResult: []*domain.MergeRequestTimelineItem{{Kind: "opened", CreatedAt: at}},
		requestsResult: []*domain.MergeRequestReviewRequest{{ID: "r1"}},
		diffResult:     []*domain.MergeRequestDiffFile{{Path: "a.txt", Status: "M"}},
	}
	cli := newMRCli(t, client)

	cases := []struct {
		name string
		args []string
		key  string
	}{
		{"comments", []string{"comments", "7", "--json"}, `"threads"`},
		{"timeline", []string{"timeline", "7", "--json"}, `"timeline"`},
		{"requests", []string{"requests", "7", "--json"}, `"review_requests"`},
		{"diff", []string{"diff", "7", "--json"}, `"files"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCmdInDir(t, root, cli.setupMrCmd(), tc.args...)
			require.NoError(t, err)
			require.Contains(t, out, tc.key)
		})
	}
}

func TestMrReviewCommands_NotARepo(t *testing.T) {
	cli := newMRCli(t, &fakeMRClient{})
	dir := t.TempDir()

	calls := [][]string{
		{"view", "7"},
		{"reopen", "7"},
		{"review", "7", "-m", "x"},
		{"comments", "7"},
		{"comment", "7", "-m", "x"},
		{"reply", "7", "t1", "-m", "x"},
		{"resolve", "7", "t1"},
		{"timeline", "7"},
		{"requests", "7"},
		{"request-review", "7", "u1"},
		{"unrequest-review", "7", "u1"},
		{"diff", "7"},
		{"assign", "7", "8"},
		{"checks", "7"},
		{"check", "7", "--name", "build", "--state", "success"},
	}
	for _, args := range calls {
		t.Run(args[0], func(t *testing.T) {
			_, err := runCmdInDir(t, dir, cli.setupMrCmd(), args...)
			require.EqualError(t, err, "not a nipa repository (or any of the parent directories)")
		})
	}
}

func TestThreadHeader(t *testing.T) {
	oldLine := int64(4)
	require.Equal(t, "thread t4 (top-level, open)", threadHeader(&domain.MergeRequestThread{ID: "t4"}))
	require.Equal(t,
		"thread t3 (code.txt(old):4, resolved, outdated)",
		threadHeader(&domain.MergeRequestThread{ID: "t3", FilePath: "code.txt", OldLine: &oldLine, Resolved: true, Outdated: true}),
	)
	require.Equal(t, "u9", actorLabel(domain.ReviewActor{UserID: "u9"}))
	require.Equal(t, "Rev", actorLabel(domain.ReviewActor{UserID: "u9", Name: "Rev"}))
}

func TestSetupMrAssignCmd(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{assigneeResult: &domain.MergeRequest{
		Number: 2, Assignees: []domain.ReviewActor{{UserID: "8", Name: "Bob"}},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "assign", "2", " 8 ", "9")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request #2 assigned to Bob.")
	require.Equal(t, []string{"8", "9"}, client.assigneeIDs)

	client = &fakeMRClient{assigneeResult: &domain.MergeRequest{Number: 2}}
	cli = newMRCli(t, client)
	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "assign", "2")
	require.NoError(t, err)
	require.Contains(t, out, "Merge request #2 has no assignees.")

	client = &fakeMRClient{assigneeErr: errors.New("boom")}
	cli = newMRCli(t, client)
	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "assign", "2", "8")
	require.EqualError(t, err, "boom")
}

func TestSetupMrChecksCmd(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{checksResult: []*domain.MergeRequestCheck{
		{Name: "build", State: "success", Reporter: domain.ReviewActor{UserID: "8", Name: "Bob"}},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "checks", "7")
	require.NoError(t, err)
	require.Contains(t, out, "build")
	require.Contains(t, out, "Bob")

	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "checks", "7", "--json")
	require.NoError(t, err)
	require.Contains(t, out, `"checks"`)

	client = &fakeMRClient{}
	cli = newMRCli(t, client)
	out, err = runCmdInDir(t, root, cli.setupMrCmd(), "checks", "7")
	require.NoError(t, err)
	require.Contains(t, out, "no status checks")

	client = &fakeMRClient{checksErr: errors.New("boom")}
	cli = newMRCli(t, client)
	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "checks", "7")
	require.EqualError(t, err, "boom")
}

func TestSetupMrCheckCmd(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{reportCheck: &domain.MergeRequestCheck{Name: "build", State: "success"}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "check", "7", "--name", " build ", "--state", "success", "--url", "https://ci/x")
	require.NoError(t, err)
	require.Contains(t, out, `Check "build" reported as success.`)
	require.Equal(t, "build", client.checkName)
	require.Equal(t, "success", client.checkState)
	require.Equal(t, "https://ci/x", client.checkURL)

	client = &fakeMRClient{reportErr: errors.New("boom")}
	cli = newMRCli(t, client)
	_, err = runCmdInDir(t, root, cli.setupMrCmd(), "check", "7", "--name", "build", "--state", "success")
	require.EqualError(t, err, "boom")
}

func TestSetupMrViewCmd_Assignees(t *testing.T) {
	root := setupRepo(t, "feature")
	client := &fakeMRClient{getResult: &domain.MergeRequest{
		Number: 7, Status: domain.MergeRequestOpen,
		Assignees: []domain.ReviewActor{{UserID: "8", Name: "Bob"}},
	}}
	cli := newMRCli(t, client)

	out, err := runCmdInDir(t, root, cli.setupMrCmd(), "view", "7")
	require.NoError(t, err)
	require.Contains(t, out, "assignees: Bob")
}
