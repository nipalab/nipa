package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestMergeRequest_WithChecks(t *testing.T) {
	mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
	checks := NewMergeRequestCheck(
		NewMockmergeRequestCheckRepository(gomock.NewController(t)),
		repo, branchRepo, perm, newTestBranchNode(t),
	)
	require.Same(t, checks, mr.WithChecks(checks).checks)
}

func TestMergeRequest_Create_WIPTitleDrafts(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in domain.MergeRequest) (*domain.MergeRequest, error) {
			require.True(t, in.Draft)
			return &in, nil
		})

	created, err := mr.Create(permissionCtx(7), snow.ID(1), "WIP: feature", "", "feature", "main", false)
	require.NoError(t, err)
	require.True(t, created.Draft)
}

func TestMergeRequest_List_Errors(t *testing.T) {
	t.Run("list error", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().List(gomock.Any(), snow.ID(1), gomock.Any()).Return(nil, errors.New("boom"))
		_, err := mr.List(permissionCtx(7), snow.ID(1), domain.MergeRequestListOptions{})
		require.EqualError(t, err, "boom")
	})

	t.Run("assignee attach error", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().List(gomock.Any(), snow.ID(1), gomock.Any()).Return([]*domain.MergeRequest{openMergeRequest()}, nil)
		repo.EXPECT().ListAssignees(gomock.Any(), snow.ID(1)).Return(nil, errors.New("boom"))
		_, err := mr.List(permissionCtx(7), snow.ID(1), domain.MergeRequestListOptions{})
		require.EqualError(t, err, "boom")
	})
}

func TestMergeRequest_Get_AttachAssigneesError(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	repo.EXPECT().ListAssignees(gomock.Any(), snow.ID(1)).Return(nil, errors.New("boom"))

	_, err := mr.Get(permissionCtx(7), snow.ID(1), 5)
	require.EqualError(t, err, "boom")
}

func TestMergeRequest_SetAssignees_Errors(t *testing.T) {
	t.Run("load error", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(nil, domain.NewErrorRecordNotFound())
		_, err := mr.SetAssignees(permissionCtx(7), snow.ID(1), 5, nil)
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("clear error", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().ClearAssignees(gomock.Any(), int64(5)).Return(errors.New("boom"))
		_, err := mr.SetAssignees(permissionCtx(7), snow.ID(1), 5, []snow.ID{8})
		require.EqualError(t, err, "boom")
	})

	t.Run("add error", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		repo.EXPECT().ClearAssignees(gomock.Any(), int64(5)).Return(nil)
		repo.EXPECT().AddAssignee(gomock.Any(), int64(5), snow.ID(8)).Return(errors.New("boom"))
		_, err := mr.SetAssignees(permissionCtx(7), snow.ID(1), 5, []snow.ID{8})
		require.EqualError(t, err, "boom")
	})

	t.Run("reload error", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).Times(2)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		gomock.InOrder(
			repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil),
			repo.EXPECT().ClearAssignees(gomock.Any(), int64(5)).Return(nil),
			repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(nil, errors.New("boom")),
		)
		_, err := mr.SetAssignees(permissionCtx(7), snow.ID(1), 5, nil)
		require.EqualError(t, err, "boom")
	})
}

func TestMergeRequest_Update_WIPSyncErrors(t *testing.T) {
	t.Run("drafting fails", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		repo.EXPECT().Update(gomock.Any(), snow.ID(1), int64(5), "WIP: x", "").
			Return(&domain.MergeRequest{ID: 5, Number: 5, ProjectID: 1, Status: domain.MergeRequestOpen, Title: "WIP: x"}, nil)
		repo.EXPECT().SetDraft(gomock.Any(), snow.ID(1), int64(5), true).Return(nil, errors.New("boom"))
		_, err := mr.Update(permissionCtx(7), snow.ID(1), 5, "WIP: x", "")
		require.EqualError(t, err, "boom")
	})

	t.Run("marking ready fails", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		before := openMergeRequest()
		before.Title = "WIP: x"
		before.Draft = true
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(before, nil)
		repo.EXPECT().Update(gomock.Any(), snow.ID(1), int64(5), "x", "").
			Return(&domain.MergeRequest{ID: 5, Number: 5, ProjectID: 1, Status: domain.MergeRequestOpen, Title: "x", Draft: true}, nil)
		repo.EXPECT().SetDraft(gomock.Any(), snow.ID(1), int64(5), false).Return(nil, errors.New("boom"))
		_, err := mr.Update(permissionCtx(7), snow.ID(1), 5, "x", "")
		require.EqualError(t, err, "boom")
	})
}

func newTestReviewUsecase(t *testing.T) (*MergeRequestReview, *MockmergeRequestReviewRepository, *MockmergeRequestRepository, *MockbranchRepository, *MockpermissionUsecase) {
	t.Helper()

	ctrl := gomock.NewController(t)
	reviewRepo := NewMockmergeRequestReviewRepository(ctrl)
	mrRepo := NewMockmergeRequestRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	review := NewMergeRequestReview(reviewRepo, mrRepo, branchRepo, nil, perm, nil, newTestBranchNode(t))
	return review, reviewRepo, mrRepo, branchRepo, perm
}

func TestMergeRequestReview_ApprovedReviewers(t *testing.T) {
	review, reviewRepo, mrRepo, branchRepo, perm := newTestReviewUsecase(t)
	head := snow.ID(11)
	dismissed := time.Unix(100, 0).UTC()

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	reviewRepo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return([]*domain.MergeRequestReview{
		{Reviewer: domain.ReviewActor{UserID: 8}, State: domain.MergeRequestReviewApproved, HeadCommitID: head},
		{Reviewer: domain.ReviewActor{UserID: 9}, State: domain.MergeRequestReviewApproved, HeadCommitID: snow.ID(99)},
		{Reviewer: domain.ReviewActor{UserID: 10}, State: domain.MergeRequestReviewChangesRequested, HeadCommitID: head},
		{Reviewer: domain.ReviewActor{UserID: 11}, State: domain.MergeRequestReviewApproved, HeadCommitID: head, DismissedAt: &dismissed},
	}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, head), nil)

	approved, err := review.ApprovedReviewers(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.True(t, approved[8])
	require.False(t, approved[9], "an approval for an older head is stale")
	require.False(t, approved[10])
	require.False(t, approved[11], "a dismissed approval does not count")
}

func TestMergeRequestReview_ApprovedReviewers_Errors(t *testing.T) {
	t.Run("needs read access", func(t *testing.T) {
		review, _, _, _, perm := newTestReviewUsecase(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := review.ApprovedReviewers(permissionCtx(7), snow.ID(1), 5)
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("load error", func(t *testing.T) {
		review, _, mrRepo, _, perm := newTestReviewUsecase(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(nil, errors.New("boom"))
		_, err := review.ApprovedReviewers(permissionCtx(7), snow.ID(1), 5)
		require.EqualError(t, err, "boom")
	})

	t.Run("reviews error", func(t *testing.T) {
		review, reviewRepo, mrRepo, _, perm := newTestReviewUsecase(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		reviewRepo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return(nil, errors.New("boom"))
		_, err := review.ApprovedReviewers(permissionCtx(7), snow.ID(1), 5)
		require.EqualError(t, err, "boom")
	})
}

func TestBlockedMergeError(t *testing.T) {
	require.Contains(t, blockedMergeError(domain.MergeabilityBlockedChangesRequested).Error(), "change requests")
	require.Contains(t, blockedMergeError(domain.MergeabilityBlockedDraft).Error(), "draft")
	require.Contains(t, blockedMergeError(domain.MergeabilityBlockedRequiredReviewers).Error(), "required reviewers")
	require.Contains(t, blockedMergeError(domain.MergeabilityBlockedChecks).Error(), "status checks")
	require.Contains(t, blockedMergeError("something-else").Error(), "approvals")
}

// newReviewGateFixture builds a merge request usecase with real review and
// check usecases over mocks, without the permissive AnyTimes defaults of the
// shared fixture so the policy branches can be steered.
func newReviewGateFixture(t *testing.T) (*MergeRequest, *MockmergeRequestRepository, *MockbranchRepository, *MockpermissionUsecase, *MockbranchMerger, *MockmergeRequestReviewRepository, *MockmergeRequestCheckRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockmergeRequestRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	merger := NewMockbranchMerger(ctrl)
	reviewRepo := NewMockmergeRequestReviewRepository(ctrl)
	checkRepo := NewMockmergeRequestCheckRepository(ctrl)

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	mr := NewMergeRequest(repo, branchRepo, perm, merger, node, noopTransactor{})
	mr = mr.WithReview(NewMergeRequestReview(reviewRepo, repo, branchRepo, merger, perm, nil, node))
	mr = mr.WithChecks(NewMergeRequestCheck(checkRepo, repo, branchRepo, perm, node))
	return mr, repo, branchRepo, perm, merger, reviewRepo, checkRepo
}

func TestMergeRequest_Check_RequiredReviewersError(t *testing.T) {
	mr, repo, branchRepo, perm, merger, reviewRepo, _ := newReviewGateFixture(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)
	target := branchWithHead(2, targetHead)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	reviewRepo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return(nil, nil)
	reviewRepo.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	branchRepo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(2)).Return(nil, errors.New("boom"))

	_, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
	require.EqualError(t, err, "boom")
}

func TestMergeRequest_Check_ApprovedReviewersError(t *testing.T) {
	mr, repo, branchRepo, perm, merger, reviewRepo, _ := newReviewGateFixture(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)
	target := branchWithHead(2, targetHead)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	reviewRepo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return(nil, nil)
	reviewRepo.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	branchRepo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(2)).Return([]domain.ReviewActor{{UserID: 8}}, nil)
	reviewRepo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return(nil, errors.New("boom"))

	_, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
	require.EqualError(t, err, "boom")
}

func TestMergeRequest_Check_RequiredReviewerMissing(t *testing.T) {
	mr, repo, branchRepo, perm, merger, reviewRepo, _ := newReviewGateFixture(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)
	target := branchWithHead(2, targetHead)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	reviewRepo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return(nil, nil)
	reviewRepo.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	branchRepo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(2)).Return([]domain.ReviewActor{{UserID: 8}}, nil)
	reviewRepo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return([]*domain.MergeRequestReview{
		{Reviewer: domain.ReviewActor{UserID: 9}, State: domain.MergeRequestReviewApproved, HeadCommitID: sourceHead},
	}, nil)

	info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeabilityBlockedRequiredReviewers, info.BlockedBy)
}

func newChecksGateFixture(t *testing.T) (*MergeRequest, *MockmergeRequestRepository, *MockbranchRepository, *MockpermissionUsecase, *MockbranchMerger, *MockmergeRequestCheckRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockmergeRequestRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	merger := NewMockbranchMerger(ctrl)
	checkRepo := NewMockmergeRequestCheckRepository(ctrl)

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	mr := NewMergeRequest(repo, branchRepo, perm, merger, node, noopTransactor{})
	mr = mr.WithChecks(NewMergeRequestCheck(checkRepo, repo, branchRepo, perm, node))
	return mr, repo, branchRepo, perm, merger, checkRepo
}

func TestMergeRequest_Check_RequiredChecks(t *testing.T) {
	t.Run("required checks error", func(t *testing.T) {
		mr, repo, branchRepo, perm, merger, _ := newChecksGateFixture(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		target := branchWithHead(2, targetHead)
		target.RequireStatusChecks = true

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
			Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
		branchRepo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(2)).Return(nil, errors.New("boom"))

		_, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.EqualError(t, err, "boom")
	})

	t.Run("missing check blocks", func(t *testing.T) {
		mr, repo, branchRepo, perm, merger, checkRepo := newChecksGateFixture(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		target := branchWithHead(2, targetHead)
		target.RequireStatusChecks = true

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(target, nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
			Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
		branchRepo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(2)).Return([]string{"build"}, nil)
		checkRepo.EXPECT().List(gomock.Any(), int64(5), sourceHead).Return(nil, nil)

		info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, domain.MergeabilityBlockedChecks, info.BlockedBy)
	})
}
