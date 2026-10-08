package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestPush_EmitsWebhookEvent(t *testing.T) {
	uc, _, pushRepo, ctx := newReviewPushFixture(t)

	var applied ApplyPushRequest
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req ApplyPushRequest) error {
			applied = req
			return nil
		})
	hooks := NewMockhookPushGate(gomock.NewController(t))
	hooks.EXPECT().EmitPush(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event PushEvent) error {
			require.Equal(t, snow.ID(1), event.ProjectID)
			require.Equal(t, snow.ID(7), event.Actor)
			require.Equal(t, "main", event.Branch.Name)
			require.Equal(t, snow.ID(9), event.Before.ID)
			require.Equal(t, applied.CommitID, event.AfterID)
			require.Equal(t, "msg", event.Message)
			require.Len(t, event.Files, 1)
			require.Equal(t, "a.txt", event.Files[0].Path)
			require.Empty(t, event.HeadBinary)
			return nil
		})

	ch, fh := chunkAndFileHash(t, "hello")
	_, err := uc.WithHooks(hooks).Push(ctx, snow.ID(1), "main", "", "msg",
		[]*domain.PushFile{{Path: "a.txt", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}},
		nil, "", snow.ID(9).Base36())
	require.NoError(t, err)
}

// a delivery failure must not turn a stored commit into a failed push
func TestPush_WebhookHookFailureIsNotFatal(t *testing.T) {
	uc, _, pushRepo, ctx := newReviewPushFixture(t)
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil)
	hooks := NewMockhookPushGate(gomock.NewController(t))
	hooks.EXPECT().EmitPush(gomock.Any(), gomock.Any()).Return(errors.New("dispatcher down"))

	ch, fh := chunkAndFileHash(t, "hello")
	result, err := uc.WithHooks(hooks).Push(ctx, snow.ID(1), "main", "", "msg",
		[]*domain.PushFile{{Path: "a.txt", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}},
		nil, "", snow.ID(9).Base36())
	require.NoError(t, err)
	require.NotZero(t, result.CommitID)
}

func TestBranch_CreateEmitsWebhookEvent(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(7)
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 1, nil, snow.ID(0)).
		Return([]*domain.Branch{{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}}, nil)
	repo.EXPECT().CreateBranch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, branch domain.Branch) (*domain.Branch, error) {
			created := branch
			return &created, nil
		})
	hooks := NewMockhookBranchGate(gomock.NewController(t))
	hooks.EXPECT().EmitBranch(gomock.Any(), domain.WebhookEventBranchCreated, snow.ID(1), gomock.Any(), snow.ID(7)).DoAndReturn(
		func(_ context.Context, event string, _ snow.ID, branch *domain.Branch, _ snow.ID) error {
			require.Equal(t, "feature", branch.Name)
			require.NotNil(t, branch.CommitID)
			require.Equal(t, head, *branch.CommitID)
			return nil
		})

	created, err := uc.WithHooks(hooks).CreateBranch(ctx, snow.ID(1), "feature", BranchForkPoint{BranchName: "main"})
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
}

func TestBranch_DeleteEmitsWebhookEvent(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(7)
	branch := &domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch, {ID: 1, ProjectID: 1, Name: "main", IsDefault: true}}, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(7)).Return(false, nil)
	repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)
	hooks := NewMockhookBranchGate(gomock.NewController(t))
	hooks.EXPECT().EmitBranch(gomock.Any(), domain.WebhookEventBranchDeleted, snow.ID(1), branch, snow.ID(7)).Return(nil)

	require.NoError(t, uc.WithHooks(hooks).Delete(ctx, snow.ID(1), "feature"))
}

func TestMergeRequest_CreateEmitsWebhookEvent(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	ctx := permissionCtx(7)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.MergeRequest) (*domain.MergeRequest, error) {
			return &created, nil
		})
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRCreated, snow.ID(1), gomock.Any(), snow.ID(7)).DoAndReturn(
		func(_ context.Context, _ string, _ snow.ID, event *domain.MergeRequest, _ snow.ID) error {
			require.Equal(t, "Feature", event.Title)
			require.Equal(t, "feature", event.SourceBranch)
			return nil
		})

	_, err := mr.WithHooks(hooks).Create(ctx, snow.ID(1), "Feature", "", "feature", "main", false)
	require.NoError(t, err)
}

func TestMergeRequest_UpdateEmitsWebhookEvent(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	ctx := permissionCtx(7)
	updated := openMergeRequest()
	updated.Title = "New title"

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	repo.EXPECT().Update(gomock.Any(), snow.ID(1), int64(5), "New title", "body").Return(updated, nil)
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRUpdated, snow.ID(1), updated, snow.ID(7)).Return(nil)

	got, err := mr.WithHooks(hooks).Update(ctx, snow.ID(1), 5, "New title", "body")
	require.NoError(t, err)
	require.Equal(t, "New title", got.Title)
}

func TestMergeRequest_CloseEmitsWebhookEvent(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	ctx := permissionCtx(7)
	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestClosed, nil).Return(nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRClosed, snow.ID(1), closed, snow.ID(7)).Return(nil)

	got, err := mr.WithHooks(hooks).Close(ctx, snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, got.Status)
}

func TestMergeRequest_ReopenEmitsWebhookEvent(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	ctx := permissionCtx(7)
	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed
	reopened := openMergeRequest()

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestOpen, nil).Return(nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(reopened, nil)
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRReopened, snow.ID(1), reopened, snow.ID(7)).Return(nil)

	got, err := mr.WithHooks(hooks).Reopen(ctx, snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestOpen, got.Status)
}

func TestMergeRequest_MergeEmitsWebhookEvent(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).Times(2)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().MergeForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature", gomock.Any()).
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}, nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestMerged, &sourceHead).Return(nil)
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRMerged, snow.ID(1), gomock.Any(), snow.ID(7)).Return(nil)

	merged, _, err := mr.WithHooks(hooks).Merge(permissionCtx(7), snow.ID(1), 5, "", false)
	require.NoError(t, err)
	require.Equal(t, openMergeRequest().ID, merged.ID)
}

// a delivery failure must not turn a closed merge request into a failed close
func TestMergeRequest_WebhookHookFailureIsNotFatal(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	ctx := permissionCtx(7)
	closed := openMergeRequest()
	closed.Status = domain.MergeRequestClosed

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestClosed, nil).Return(nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("dispatcher down"))

	got, err := mr.WithHooks(hooks).Close(ctx, snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestClosed, got.Status)
}

func TestMergeRequestReview_SubmitEmitsWebhookEvent(t *testing.T) {
	review, repo, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil)
	repo.EXPECT().UpsertReview(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in domain.MergeRequestReview) (*domain.MergeRequestReview, error) {
			return &in, nil
		})
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			return &event, nil
		})
	repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(9)).
		Return(domain.NewErrorNotFound("no request"))
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRReviewSubmitted, snow.ID(1), gomock.Any(), snow.ID(9)).Return(nil)

	_, err := review.WithHooks(hooks).SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewApproved, "lgtm", nil)
	require.NoError(t, err)
}

func TestMergeRequestReview_DismissEmitsWebhookEvent(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetReview(gomock.Any(), int64(5), snow.ID(1)).
		Return(&domain.MergeRequestReview{ID: 1, Reviewer: domain.ReviewActor{UserID: 8}}, nil)
	repo.EXPECT().DismissReview(gomock.Any(), int64(5), snow.ID(1), snow.ID(9), "manual", gomock.Any()).Return(nil)
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			return &event, nil
		})
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRReviewDismissed, snow.ID(1), gomock.Any(), snow.ID(9)).Return(nil)

	_, err := review.WithHooks(hooks).DismissReview(permissionCtx(9), snow.ID(1), 5, snow.ID(1))
	require.NoError(t, err)
}

func TestMergeRequestReview_RequestEmitsWebhookEvent(t *testing.T) {
	review, repo, mrRepo, _, perm, _, users := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)
	users.EXPECT().GetByID(gomock.Any(), snow.ID(8)).Return(&domain.User{ID: 8, Name: "Rev"}, nil)
	repo.EXPECT().CreateReviewRequest(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req domain.MergeRequestReviewRequest) (*domain.MergeRequestReviewRequest, error) {
			return &req, nil
		})
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			return &event, nil
		})
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRReviewRequested, snow.ID(1), gomock.Any(), snow.ID(9)).Return(nil)

	_, err := review.WithHooks(hooks).RequestReview(permissionCtx(9), snow.ID(1), 5, snow.ID(8))
	require.NoError(t, err)
}

func TestMergeRequestReview_UnrequestEmitsWebhookEvent(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(8)).Return(nil)
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			return &event, nil
		})
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRReviewUnrequested, snow.ID(1), gomock.Any(), snow.ID(8)).Return(nil)

	require.NoError(t, review.WithHooks(hooks).RemoveReviewRequest(permissionCtx(8), snow.ID(1), 5, snow.ID(8)))
}

func TestMergeRequestReview_CommentsEmitWebhookEvent(t *testing.T) {
	review, repo, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).Times(2)
	expectLoad(perm, mrRepo)
	expectLoad(perm, mrRepo)
	repo.EXPECT().CreateThread(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, thread domain.MergeRequestThread) (*domain.MergeRequestThread, error) {
			return &thread, nil
		})
	repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, c domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
			return &c, nil
		}).Times(2)
	repo.EXPECT().GetThread(gomock.Any(), int64(5), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ int64, _ snow.ID) (*domain.MergeRequestThread, error) {
			return &domain.MergeRequestThread{ID: 1, MergeRequestID: 5}, nil
		})
	hooks := NewMockhookMergeRequestGate(gomock.NewController(t))
	hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRCommentCreated, snow.ID(1), gomock.Any(), snow.ID(9)).Return(nil).Times(2)

	review = review.WithHooks(hooks)
	thread, err := review.AddComment(permissionCtx(9), snow.ID(1), 5, ThreadComment{Body: "first"})
	require.NoError(t, err)
	_, err = review.Reply(permissionCtx(9), snow.ID(1), 5, thread.ID, "second")
	require.NoError(t, err)
}

func TestMergeRequestReview_ReplyCommentError(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetThread(gomock.Any(), int64(5), gomock.Any()).
		Return(&domain.MergeRequestThread{ID: 1, MergeRequestID: 5}, nil)
	repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

	_, err := review.Reply(permissionCtx(9), snow.ID(1), 5, snow.ID(1), "x")
	require.ErrorContains(t, err, "boom")
}

func TestMergeRequestReview_UnrequestEventError(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(8)).Return(nil)
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

	err := review.RemoveReviewRequest(permissionCtx(8), snow.ID(1), 5, snow.ID(8))
	require.ErrorContains(t, err, "boom")
}
