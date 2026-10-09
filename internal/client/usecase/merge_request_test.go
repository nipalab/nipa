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
	draft                                            bool
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
	updateDraft       *bool
	updateResult      *domain.MergeRequest
	updateErr         error

	listStatus string
	listLimit  int
	listOpts   domain.ListMergeRequestOptions
	listResult []*domain.MergeRequest
	listErr    error

	closeNumber int64
	closeResult *domain.MergeRequest
	closeErr    error

	mergeNumber       int64
	mergeStrategy     string
	mergeDeleteSource bool

	checkName        string
	checkState       string
	checkURL         string
	reportCheck      *domain.MergeRequestCheck
	reportErr        error
	checksResult     []*domain.MergeRequestCheck
	checksErr        error
	markMergedNumber int64
	markMergedResult *domain.MergeRequest
	markMergedErr    error
	assigneeIDs      []string
	assigneeResult   *domain.MergeRequest
	assigneeErr      error
	mergeResult      *domain.MergeRequest
	mergeability     *domain.Mergeability
	mergeErr         error

	getResult       *domain.MergeRequest
	getMergeability *domain.Mergeability
	getErr          error
	getNumber       int64

	reopenResult *domain.MergeRequest
	reopenErr    error
	reopenNumber int64

	checkResult *domain.Mergeability
	checkErr    error
	checkNumber int64

	diffResult []*domain.MergeRequestDiffFile
	diffErr    error
	diffNumber int64

	commitsResult []*serverDomain.CommitLogEntry
	commitsErr    error
	commitsNumber int64

	reviewsResult []*domain.MergeRequestReview
	reviewsErr    error
	reviewsNumber int64

	submitResult *domain.MergeRequestReview
	submitState  string
	submitBody   string
	submitErr    error
	submitNumber int64

	threadsResult []*domain.MergeRequestThread
	threadsErr    error
	threadsNumber int64

	commentResult  *domain.MergeRequestThread
	commentFile    string
	commentNewLine *int64
	commentOldLine *int64
	commentErr     error
	commentNumber  int64
	commentBody    string

	replyResult   *domain.MergeRequestComment
	replyThreadID string
	replyErr      error
	replyNumber   int64
	replyBody     string

	resolveResult   *domain.MergeRequestThread
	resolveThreadID string
	resolveResolved bool
	resolveErr      error
	resolveNumber   int64

	timelineResult []*domain.MergeRequestTimelineItem
	timelineErr    error
	timelineNumber int64

	requestsResult []*domain.MergeRequestReviewRequest
	requestsErr    error
	requestsNumber int64

	requestResult    *domain.MergeRequestReviewRequest
	requestUserID    string
	requestErr       error
	removeRequestErr error
	requestNumber    int64

	defaultBranch    *serverDomain.Branch
	defaultBranchErr error
}

func (s *stubMRClient) Connect(_ context.Context, host string) error {
	s.connectHost = host
	return s.connectErr
}

func (s *stubMRClient) CreateMergeRequest(_ context.Context, org, project, title, description, sourceBranch, targetBranch string, draft bool) (*domain.MergeRequest, error) {
	s.createCalls = append(s.createCalls, stubMRCreateCall{
		org: org, project: project, title: title, description: description,
		source: sourceBranch, target: targetBranch, draft: draft,
	})
	return s.createResult, s.createErr
}

func (s *stubMRClient) UpdateMergeRequest(_ context.Context, _, _ string, number int64, title, description string, draft *bool) (*domain.MergeRequest, error) {
	s.updateNumber, s.updateTitle, s.updateDescription = number, title, description
	s.updateDraft = draft
	return s.updateResult, s.updateErr
}

func (s *stubMRClient) ListMergeRequests(_ context.Context, _, _ string, opts domain.ListMergeRequestOptions) ([]*domain.MergeRequest, int64, error) {
	s.listStatus, s.listLimit, s.listOpts = opts.Status, opts.Limit, opts
	return s.listResult, 7, s.listErr
}

func (s *stubMRClient) MergeMergeRequest(_ context.Context, _, _ string, number int64, strategy string, deleteSource bool) (*domain.MergeRequest, *domain.Mergeability, error) {
	s.mergeNumber = number
	s.mergeStrategy = strategy
	s.mergeDeleteSource = deleteSource
	return s.mergeResult, s.mergeability, s.mergeErr
}

func (s *stubMRClient) CloseMergeRequest(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, error) {
	s.closeNumber = number
	return s.closeResult, s.closeErr
}

func (s *stubMRClient) MarkMergeRequestMerged(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, error) {
	s.markMergedNumber = number
	return s.markMergedResult, s.markMergedErr
}

func (s *stubMRClient) SetMergeRequestAssignees(_ context.Context, _, _ string, _ int64, userIDs []string) (*domain.MergeRequest, error) {
	s.assigneeIDs = userIDs
	return s.assigneeResult, s.assigneeErr
}

func (s *stubMRClient) GetMergeRequest(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, *domain.Mergeability, error) {
	s.getNumber = number
	return s.getResult, s.getMergeability, s.getErr
}

func (s *stubMRClient) ReopenMergeRequest(_ context.Context, _, _ string, number int64) (*domain.MergeRequest, error) {
	s.reopenNumber = number
	return s.reopenResult, s.reopenErr
}

func (s *stubMRClient) CheckMergeRequest(_ context.Context, _, _ string, number int64) (*domain.Mergeability, error) {
	s.checkNumber = number
	return s.checkResult, s.checkErr
}

func (s *stubMRClient) ListMergeRequestCommits(_ context.Context, _, _ string, number int64) ([]*serverDomain.CommitLogEntry, error) {
	s.commitsNumber = number
	return s.commitsResult, s.commitsErr
}

func (s *stubMRClient) GetMergeRequestDiff(_ context.Context, _, _ string, number int64) ([]*domain.MergeRequestDiffFile, error) {
	s.diffNumber = number
	return s.diffResult, s.diffErr
}

func (s *stubMRClient) ListMergeRequestReviews(_ context.Context, _, _ string, number int64) ([]*domain.MergeRequestReview, error) {
	s.reviewsNumber = number
	return s.reviewsResult, s.reviewsErr
}

func (s *stubMRClient) ReportMergeRequestCheck(_ context.Context, _, _ string, _ int64, name, state, detailsURL string) (*domain.MergeRequestCheck, error) {
	s.checkName, s.checkState, s.checkURL = name, state, detailsURL
	return s.reportCheck, s.reportErr
}

func (s *stubMRClient) ListMergeRequestChecks(_ context.Context, _, _ string, _ int64) ([]*domain.MergeRequestCheck, error) {
	return s.checksResult, s.checksErr
}

func (s *stubMRClient) SubmitMergeRequestReview(_ context.Context, _, _ string, number int64, state, body string) (*domain.MergeRequestReview, error) {
	s.submitNumber, s.submitState, s.submitBody = number, state, body
	return s.submitResult, s.submitErr
}

func (s *stubMRClient) ListMergeRequestThreads(_ context.Context, _, _ string, number int64) ([]*domain.MergeRequestThread, error) {
	s.threadsNumber = number
	return s.threadsResult, s.threadsErr
}

func (s *stubMRClient) AddMergeRequestComment(_ context.Context, _, _ string, number int64, filePath string, oldLine, newLine *int64, body string) (*domain.MergeRequestThread, error) {
	s.commentNumber, s.commentFile, s.commentOldLine, s.commentNewLine, s.commentBody = number, filePath, oldLine, newLine, body
	return s.commentResult, s.commentErr
}

func (s *stubMRClient) ReplyMergeRequestThread(_ context.Context, _, _ string, number int64, threadID, body string) (*domain.MergeRequestComment, error) {
	s.replyNumber, s.replyThreadID, s.replyBody = number, threadID, body
	return s.replyResult, s.replyErr
}

func (s *stubMRClient) ResolveMergeRequestThread(_ context.Context, _, _ string, number int64, threadID string, resolved bool) (*domain.MergeRequestThread, error) {
	s.resolveNumber, s.resolveThreadID, s.resolveResolved = number, threadID, resolved
	return s.resolveResult, s.resolveErr
}

func (s *stubMRClient) ListMergeRequestTimeline(_ context.Context, _, _ string, number int64) ([]*domain.MergeRequestTimelineItem, error) {
	s.timelineNumber = number
	return s.timelineResult, s.timelineErr
}

func (s *stubMRClient) ListMergeRequestReviewRequests(_ context.Context, _, _ string, number int64) ([]*domain.MergeRequestReviewRequest, error) {
	s.requestsNumber = number
	return s.requestsResult, s.requestsErr
}

func (s *stubMRClient) RequestMergeRequestReview(_ context.Context, _, _ string, number int64, userID string) (*domain.MergeRequestReviewRequest, error) {
	s.requestNumber, s.requestUserID = number, userID
	return s.requestResult, s.requestErr
}

func (s *stubMRClient) RemoveMergeRequestReviewRequest(_ context.Context, _, _ string, number int64, userID string) error {
	s.requestNumber, s.requestUserID = number, userID
	return s.removeRequestErr
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

	_, err := mr.Update(context.Background(), t.TempDir(), "!!!", "New", "", nil)
	require.Contains(t, err.Error(), "invalid merge request number")

	_, err = mr.Update(context.Background(), t.TempDir(), "1", "  ", "", nil)
	require.Contains(t, err.Error(), "pass --title, --description or --draft")

	updated, err := mr.Update(context.Background(), t.TempDir(), "1", " New ", "", nil)
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

	_, _, err := mr.List(context.Background(), t.TempDir(), domain.ListMergeRequestOptions{Status: "bogus"})
	require.Contains(t, err.Error(), "status must be one of")

	requests, cursor, err := mr.List(context.Background(), t.TempDir(), domain.ListMergeRequestOptions{})
	require.NoError(t, err)
	require.Len(t, requests, 1)
	require.Equal(t, 50, client.listLimit)
	require.Equal(t, int64(7), cursor)

	_, _, err = mr.List(context.Background(), t.TempDir(), domain.ListMergeRequestOptions{
		Status:       domain.MergeRequestClosed,
		Author:       "user1",
		SourceBranch: " feature ",
		TargetBranch: "main",
		After:        42,
		Limit:        5,
	})
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, client.listStatus)
	require.Equal(t, 5, client.listLimit)
	require.Equal(t, "user1", client.listOpts.Author)
	require.Equal(t, "feature", client.listOpts.SourceBranch)
	require.Equal(t, int64(42), client.listOpts.After)

	_, _, err = mr.List(context.Background(), t.TempDir(), domain.ListMergeRequestOptions{After: -1})
	require.Contains(t, err.Error(), "cannot be negative")
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

	_, _, err := mr.Merge(context.Background(), t.TempDir(), "nope!", "", false)
	require.Contains(t, err.Error(), "invalid merge request number")

	merged, info, err := mr.Merge(context.Background(), t.TempDir(), "1", "", false)
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

	_, _, err := newTestMergeRequest(t, local, client).List(context.Background(), t.TempDir(), domain.ListMergeRequestOptions{})
	require.ErrorIs(t, err, wantErr)
}

func newReviewTestFixture(t *testing.T, client *stubMRClient) *MergeRequest {
	t.Helper()
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "feature"},
	}
	return newTestMergeRequest(t, local, client)
}

func TestMergeRequest_View(t *testing.T) {
	client := &stubMRClient{
		getResult:       &domain.MergeRequest{Number: 7},
		getMergeability: &domain.Mergeability{Status: "mergeable"},
	}
	mr := newReviewTestFixture(t, client)

	got, info, err := mr.View(context.Background(), t.TempDir(), "7")
	require.NoError(t, err)
	require.Equal(t, int64(7), got.Number)
	require.Equal(t, "mergeable", info.Status)
	require.Equal(t, int64(7), client.getNumber)

	_, _, err = mr.View(context.Background(), t.TempDir(), "nope")
	require.Contains(t, err.Error(), "invalid merge request number")
}

func TestMergeRequest_Review(t *testing.T) {
	client := &stubMRClient{submitResult: &domain.MergeRequestReview{State: domain.MergeRequestReviewApproved}}
	mr := newReviewTestFixture(t, client)

	_, err := mr.Review(context.Background(), t.TempDir(), "7", "bogus", "x")
	require.Contains(t, err.Error(), "state must be one of")

	_, err = mr.Review(context.Background(), t.TempDir(), "7", domain.MergeRequestReviewApproved, "  ")
	require.Contains(t, err.Error(), "needs a message")

	review, err := mr.Review(context.Background(), t.TempDir(), "7", " approved ", " lgtm ")
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestReviewApproved, review.State)
	require.Equal(t, "approved", client.submitState)
	require.Equal(t, "lgtm", client.submitBody)
}

func TestMergeRequest_ThreadsAndComment(t *testing.T) {
	client := &stubMRClient{
		threadsResult: []*domain.MergeRequestThread{{ID: "t1"}},
		commentResult: &domain.MergeRequestThread{ID: "t2"},
	}
	mr := newReviewTestFixture(t, client)

	threads, err := mr.Threads(context.Background(), t.TempDir(), "7")
	require.NoError(t, err)
	require.Len(t, threads, 1)

	_, err = mr.Comment(context.Background(), t.TempDir(), "7", "", nil, nil, "  ")
	require.Contains(t, err.Error(), "needs a message")

	newLine := int64(2)
	thread, err := mr.Comment(context.Background(), t.TempDir(), "7", " code.txt ", nil, &newLine, " note ")
	require.NoError(t, err)
	require.Equal(t, "t2", thread.ID)
	require.Equal(t, "code.txt", client.commentFile)
	require.Equal(t, "note", client.commentBody)
	require.Equal(t, &newLine, client.commentNewLine)
}

func TestMergeRequest_ReplyResolveAndRequests(t *testing.T) {
	client := &stubMRClient{
		replyResult:   &domain.MergeRequestComment{ID: "c1", ThreadID: "t1"},
		resolveResult: &domain.MergeRequestThread{ID: "t1", Resolved: true},
		requestResult: &domain.MergeRequestReviewRequest{Reviewer: domain.ReviewActor{Name: "Rev"}},
	}
	mr := newReviewTestFixture(t, client)

	comment, err := mr.Reply(context.Background(), t.TempDir(), "7", "t1", " done ")
	require.NoError(t, err)
	require.Equal(t, "c1", comment.ID)
	require.Equal(t, "done", client.replyBody)

	_, err = mr.Reply(context.Background(), t.TempDir(), "7", "t1", "")
	require.Contains(t, err.Error(), "needs a message")

	_, err = mr.Reply(context.Background(), t.TempDir(), "7", "  ", "done")
	require.Contains(t, err.Error(), "a thread id is required")

	_, err = mr.Resolve(context.Background(), t.TempDir(), "7", " ", true)
	require.Contains(t, err.Error(), "a thread id is required")

	err = mr.RemoveReviewRequest(context.Background(), t.TempDir(), "7", " ")
	require.Contains(t, err.Error(), "user id is required")

	thread, err := mr.Resolve(context.Background(), t.TempDir(), "7", "t1", false)
	require.NoError(t, err)
	require.True(t, thread.Resolved)
	require.False(t, client.resolveResolved)

	request, err := mr.RequestReview(context.Background(), t.TempDir(), "7", " rev ")
	require.NoError(t, err)
	require.Equal(t, "Rev", request.Reviewer.Name)
	require.Equal(t, "rev", client.requestUserID)

	require.NoError(t, mr.RemoveReviewRequest(context.Background(), t.TempDir(), "7", "rev"))
	require.Equal(t, "rev", client.requestUserID)

	_, err = mr.RequestReview(context.Background(), t.TempDir(), "7", " ")
	require.Contains(t, err.Error(), "user id is required")
}

func TestMergeRequest_Check(t *testing.T) {
	client := &stubMRClient{checkResult: &domain.Mergeability{Status: "mergeable", BlockedBy: "insufficient_approvals"}}
	mr := newReviewTestFixture(t, client)

	info, err := mr.Check(context.Background(), t.TempDir(), "7")
	require.NoError(t, err)
	require.Equal(t, "mergeable", info.Status)
	require.Equal(t, "insufficient_approvals", info.BlockedBy)
	require.Equal(t, int64(7), client.checkNumber)

	_, err = mr.Check(context.Background(), t.TempDir(), "zero")
	require.Contains(t, err.Error(), "invalid merge request number")
}

func TestMergeRequest_OperationsPropagateClientErrors(t *testing.T) {
	wantErr := &domain.Error{Code: 404, Message: "merge request 7 not found"}
	client := &stubMRClient{
		getErr:           wantErr,
		reopenErr:        wantErr,
		checkErr:         wantErr,
		diffErr:          wantErr,
		commitsErr:       wantErr,
		reviewsErr:       wantErr,
		submitErr:        wantErr,
		threadsErr:       wantErr,
		commentErr:       wantErr,
		replyErr:         wantErr,
		resolveErr:       wantErr,
		timelineErr:      wantErr,
		requestsErr:      wantErr,
		requestErr:       wantErr,
		removeRequestErr: wantErr,
		mergeErr:         wantErr,
		closeErr:         wantErr,
		updateErr:        wantErr,
		createErr:        wantErr,
		listErr:          wantErr,
	}
	mr := newReviewTestFixture(t, client)
	ctx := context.Background()

	_, _, err := mr.View(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Reopen(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Check(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Diff(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Commits(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Reviews(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Review(ctx, t.TempDir(), "7", domain.MergeRequestReviewApproved, "lgtm")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Threads(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Comment(ctx, t.TempDir(), "7", "a.txt", nil, nil, "note")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Reply(ctx, t.TempDir(), "7", "t1", "done")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Resolve(ctx, t.TempDir(), "7", "t1", true)
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Timeline(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.ReviewRequests(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.RequestReview(ctx, t.TempDir(), "7", "user1")
	require.ErrorIs(t, err, wantErr)

	err = mr.RemoveReviewRequest(ctx, t.TempDir(), "7", "user1")
	require.ErrorIs(t, err, wantErr)

	_, _, err = mr.Merge(ctx, t.TempDir(), "7", "", false)
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Close(ctx, t.TempDir(), "7")
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Update(ctx, t.TempDir(), "7", "New", "", nil)
	require.ErrorIs(t, err, wantErr)

	_, err = mr.Create(ctx, t.TempDir(), CreateMergeRequestOptions{Title: "T", Source: "feature", Target: "main"})
	require.ErrorIs(t, err, wantErr)

	_, _, err = mr.List(ctx, t.TempDir(), domain.ListMergeRequestOptions{})
	require.ErrorIs(t, err, wantErr)
}

func TestMergeRequest_InvalidNumberErrors(t *testing.T) {
	client := &stubMRClient{}
	mr := newReviewTestFixture(t, client)
	dir := t.TempDir()
	ctx := context.Background()

	calls := map[string]func() error{
		"View":    func() error { _, _, err := mr.View(ctx, dir, "bad"); return err },
		"Reopen":  func() error { _, err := mr.Reopen(ctx, dir, "bad"); return err },
		"Check":   func() error { _, err := mr.Check(ctx, dir, "bad"); return err },
		"Diff":    func() error { _, err := mr.Diff(ctx, dir, "bad"); return err },
		"Commits": func() error { _, err := mr.Commits(ctx, dir, "bad"); return err },
		"Reviews": func() error { _, err := mr.Reviews(ctx, dir, "bad"); return err },
		"Review": func() error {
			_, err := mr.Review(ctx, dir, "bad", domain.MergeRequestReviewApproved, "lgtm")
			return err
		},
		"Threads":             func() error { _, err := mr.Threads(ctx, dir, "bad"); return err },
		"Comment":             func() error { _, err := mr.Comment(ctx, dir, "bad", "a.txt", nil, nil, "note"); return err },
		"Reply":               func() error { _, err := mr.Reply(ctx, dir, "bad", "t1", "done"); return err },
		"Resolve":             func() error { _, err := mr.Resolve(ctx, dir, "bad", "t1", true); return err },
		"Timeline":            func() error { _, err := mr.Timeline(ctx, dir, "bad"); return err },
		"ReviewRequests":      func() error { _, err := mr.ReviewRequests(ctx, dir, "bad"); return err },
		"RequestReview":       func() error { _, err := mr.RequestReview(ctx, dir, "bad", "user1"); return err },
		"RemoveReviewRequest": func() error { return mr.RemoveReviewRequest(ctx, dir, "bad", "user1") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, call(), "invalid merge request number")
		})
	}
}

func TestMergeRequest_ConnectErrorForEveryOperation(t *testing.T) {
	client := &stubMRClient{connectErr: errors.New("dial failed")}
	mr := newReviewTestFixture(t, client)
	dir := t.TempDir()
	ctx := context.Background()

	calls := map[string]func() error{
		"View":    func() error { _, _, err := mr.View(ctx, dir, "7"); return err },
		"Reopen":  func() error { _, err := mr.Reopen(ctx, dir, "7"); return err },
		"Check":   func() error { _, err := mr.Check(ctx, dir, "7"); return err },
		"Diff":    func() error { _, err := mr.Diff(ctx, dir, "7"); return err },
		"Commits": func() error { _, err := mr.Commits(ctx, dir, "7"); return err },
		"Reviews": func() error { _, err := mr.Reviews(ctx, dir, "7"); return err },
		"Review": func() error {
			_, err := mr.Review(ctx, dir, "7", domain.MergeRequestReviewApproved, "lgtm")
			return err
		},
		"Threads":             func() error { _, err := mr.Threads(ctx, dir, "7"); return err },
		"Comment":             func() error { _, err := mr.Comment(ctx, dir, "7", "a.txt", nil, nil, "note"); return err },
		"Reply":               func() error { _, err := mr.Reply(ctx, dir, "7", "t1", "done"); return err },
		"Resolve":             func() error { _, err := mr.Resolve(ctx, dir, "7", "t1", true); return err },
		"Timeline":            func() error { _, err := mr.Timeline(ctx, dir, "7"); return err },
		"ReviewRequests":      func() error { _, err := mr.ReviewRequests(ctx, dir, "7"); return err },
		"RequestReview":       func() error { _, err := mr.RequestReview(ctx, dir, "7", "user1"); return err },
		"RemoveReviewRequest": func() error { return mr.RemoveReviewRequest(ctx, dir, "7", "user1") },
		"Close":               func() error { _, err := mr.Close(ctx, dir, "7"); return err },
		"Merge":               func() error { _, _, err := mr.Merge(ctx, dir, "7", "", false); return err },
		"Update":              func() error { _, err := mr.Update(ctx, dir, "7", "New", "", nil); return err },
		"Create": func() error {
			_, err := mr.Create(ctx, dir, CreateMergeRequestOptions{Title: "T", Source: "feature", Target: "main"})
			return err
		},
		"List": func() error { _, _, err := mr.List(ctx, dir, domain.ListMergeRequestOptions{}); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			require.ErrorContains(t, call(), "dial failed")
		})
	}
}

func TestMergeRequest_ConnectErrorPaths(t *testing.T) {
	client := &stubMRClient{}
	ctx := context.Background()

	initErr := errors.New("init failed")
	initLocal := &stubLocalRepo{initErr: initErr}
	_, _, err := newTestMergeRequest(t, initLocal, client).List(ctx, t.TempDir(), domain.ListMergeRequestOptions{})
	require.ErrorIs(t, err, initErr)

	configErr := errors.New("config missing")
	configLocal := &stubLocalRepo{configLoadErr: configErr}
	_, _, err = newTestMergeRequest(t, configLocal, client).List(ctx, t.TempDir(), domain.ListMergeRequestOptions{})
	require.ErrorIs(t, err, configErr)

	badURL := &stubLocalRepo{loadConfig: &domain.Config{Url: "ftp://example.com/org/project"}}
	_, _, err = newTestMergeRequest(t, badURL, client).List(ctx, t.TempDir(), domain.ListMergeRequestOptions{})
	require.Contains(t, err.Error(), "invalid URL scheme")
}
