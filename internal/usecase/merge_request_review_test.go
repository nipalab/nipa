package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func newTestMergeRequestReview(t *testing.T) (*MergeRequestReview, *MockmergeRequestReviewRepository, *MockmergeRequestRepository, *MockbranchRepository, *MockpermissionUsecase, *MockbranchMerger, *MockuserLookup) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockmergeRequestReviewRepository(ctrl)
	mrRepo := NewMockmergeRequestRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	merger := NewMockbranchMerger(ctrl)
	users := NewMockuserLookup(ctrl)
	return NewMergeRequestReview(repo, mrRepo, branchRepo, merger, perm, users, newTestBranchNode(t)), repo, mrRepo, branchRepo, perm, merger, users
}

// expectLoad wires a read-permission check and the merge request lookup every
// method performs first.
func expectLoad(perm *MockpermissionUsecase, mrRepo *MockmergeRequestRepository) {
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
}

// textFileDiff is a modified file whose second line changed, so old line 2 and
// new line 2 are real diff anchors.
func textFileDiff() []diff.FileDiff {
	return []diff.FileDiff{{
		Change: diff.Change{Path: "main.go", Status: diff.Modified},
		Old:    []byte("package main\n\nfunc main() {}\n"),
		New:    []byte("package main\n\nfunc main() { run() }\n"),
	}}
}

func expectDiff(branchRepo *MockbranchRepository, merger *MockbranchMerger, files []diff.FileDiff) {
	base, head := snow.ID(12), snow.ID(11)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &base}, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1),
		MergeRef{CommitID: &base}, MergeRef{CommitID: &head}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &base}, nil)
	merger.EXPECT().TreeDiffBetween(gomock.Any(), snow.ID(1), &base, head).Return(files, nil)
}

func TestMergeRequestReview_SubmitReview(t *testing.T) {
	review, repo, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil)
	repo.EXPECT().UpsertReview(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in domain.MergeRequestReview) (*domain.MergeRequestReview, error) {
			require.Equal(t, int64(5), in.MergeRequestID)
			require.Equal(t, domain.MergeRequestReviewApproved, in.State)
			require.Equal(t, "lgtm", in.Body)
			require.Equal(t, snow.ID(11), in.HeadCommitID)
			require.Equal(t, snow.ID(9), in.Reviewer.UserID)
			return &in, nil
		})
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			require.Equal(t, domain.MergeRequestEventReviewSubmitted, event.Kind)
			require.Equal(t, int64(5), event.MergeRequestID)
			require.Equal(t, "lgtm", event.Body)
			return &event, nil
		})
	repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(9)).
		Return(domain.NewErrorNotFound("no request"))

	got, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewApproved, " lgtm ", nil)
	require.NoError(t, err)
	require.Equal(t, domain.MergeRequestReviewApproved, got.State)
	require.False(t, got.Stale)
}

func TestMergeRequestReview_SubmitReview_Rejections(t *testing.T) {
	t.Run("self review", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

		_, err := review.SubmitReview(permissionCtx(7), snow.ID(1), 5, domain.MergeRequestReviewApproved, "", nil)
		requireUserError(t, err)
	})

	t.Run("self comment is allowed through validation", func(t *testing.T) {
		review, repo, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
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
		repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(7)).Return(nil)

		got, err := review.SubmitReview(permissionCtx(7), snow.ID(1), 5, domain.MergeRequestReviewCommented, "note", nil)
		require.NoError(t, err)
		require.Equal(t, domain.MergeRequestReviewCommented, got.State)
	})

	t.Run("invalid state", func(t *testing.T) {
		review, _, _, _, _, _, _ := newTestMergeRequestReview(t)
		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, "yolo", "body", nil)
		requireUserError(t, err)
	})

	t.Run("empty review", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewCommented, "  ", nil)
		requireUserError(t, err)
	})

	t.Run("closed merge request", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		closed := openMergeRequest()
		closed.Status = domain.MergeRequestMerged
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewApproved, "lgtm", nil)
		var domErr *domain.Error
		require.ErrorAs(t, err, &domErr)
		require.Equal(t, 409, domErr.Code)
	})

	t.Run("no permission", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)

		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewApproved, "lgtm", nil)
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("empty source branch", func(t *testing.T) {
		review, _, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1}, nil)

		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewApproved, "lgtm", nil)
		requireUserError(t, err)
	})
}

func TestMergeRequestReview_SubmitReview_WithComments(t *testing.T) {
	review, repo, mrRepo, branchRepo, perm, merger, _ := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
	repo.EXPECT().UpsertReview(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in domain.MergeRequestReview) (*domain.MergeRequestReview, error) {
			return &in, nil
		})

	inline := 2
	repo.EXPECT().CreateThread(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, thread domain.MergeRequestThread) (*domain.MergeRequestThread, error) {
			require.Equal(t, "main.go", thread.FilePath)
			require.Nil(t, thread.OldLine)
			require.Equal(t, &inline, thread.NewLine)
			require.Equal(t, snowPtr(11), thread.HeadCommitID)
			require.NotZero(t, thread.ReviewID)
			return &thread, nil
		})
	repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, c domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
			require.Equal(t, "nit: naming", c.Body)
			return &c, nil
		})
	repo.EXPECT().CreateThread(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, thread domain.MergeRequestThread) (*domain.MergeRequestThread, error) {
			require.Equal(t, "", thread.FilePath)
			require.Nil(t, thread.NewLine)
			require.NotNil(t, thread.ReviewID, "a comment submitted with a review belongs to it")
			return &thread, nil
		})
	repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, c domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
			require.Equal(t, "overall fine", c.Body)
			return &c, nil
		})
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			return &event, nil
		})
	repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(9)).Return(nil)
	expectDiff(branchRepo, merger, textFileDiff())

	_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewChangesRequested, "please address",
		[]ThreadComment{
			{FilePath: "main.go", NewLine: &inline, Body: " nit: naming "},
			{Body: "overall fine"},
		})
	require.NoError(t, err)
}

func TestMergeRequestReview_Anchor(t *testing.T) {
	inline := 2

	t.Run("rejects a line the diff does not show", func(t *testing.T) {
		review, repo, mrRepo, branchRepo, perm, merger, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		expectLoad(perm, mrRepo)
		repo.EXPECT().UpsertReview(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, in domain.MergeRequestReview) (*domain.MergeRequestReview, error) {
				return &in, nil
			})
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
		expectDiff(branchRepo, merger, textFileDiff())
		beyond := 99

		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewCommented, "", []ThreadComment{
			{FilePath: "main.go", NewLine: &beyond, Body: "nope"},
		})
		requireUserError(t, err)
	})

	t.Run("rejects a file outside the diff", func(t *testing.T) {
		review, repo, mrRepo, branchRepo, perm, merger, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		expectLoad(perm, mrRepo)
		repo.EXPECT().UpsertReview(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, in domain.MergeRequestReview) (*domain.MergeRequestReview, error) {
				return &in, nil
			})
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
		expectDiff(branchRepo, merger, textFileDiff())

		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewCommented, "", []ThreadComment{
			{FilePath: "other.go", NewLine: &inline, Body: "nope"},
		})
		require.Error(t, err)
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("a removed line anchors on the old side", func(t *testing.T) {
		review, repo, mrRepo, branchRepo, perm, merger, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		expectLoad(perm, mrRepo)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
		repo.EXPECT().UpsertReview(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, in domain.MergeRequestReview) (*domain.MergeRequestReview, error) {
				return &in, nil
			})
		repo.EXPECT().CreateThread(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, thread domain.MergeRequestThread) (*domain.MergeRequestThread, error) {
				require.Equal(t, &inline, thread.OldLine)
				require.Nil(t, thread.NewLine)
				return &thread, nil
			})
		repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, c domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
				return &c, nil
			})
		repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
				return &event, nil
			})
		repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(9)).Return(nil)
		expectDiff(branchRepo, merger, textFileDiff())

		_, err := review.SubmitReview(permissionCtx(9), snow.ID(1), 5, domain.MergeRequestReviewCommented, "", []ThreadComment{
			{FilePath: "main.go", OldLine: &inline, Body: "this line is gone"},
		})
		require.NoError(t, err)
	})

	t.Run("a line comment without a file is rejected", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		expectLoad(perm, mrRepo)

		_, err := review.AddComment(permissionCtx(9), snow.ID(1), 5, ThreadComment{NewLine: &inline, Body: "orphan"})
		requireUserError(t, err)
	})

	t.Run("a non-positive line is rejected", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		expectLoad(perm, mrRepo)
		zero := 0

		_, err := review.AddComment(permissionCtx(9), snow.ID(1), 5, ThreadComment{FilePath: "main.go", NewLine: &zero, Body: "nope"})
		requireUserError(t, err)
	})

	t.Run("an old-only comment on a renamed file keeps the old path", func(t *testing.T) {
		review, repo, mrRepo, branchRepo, perm, merger, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		expectLoad(perm, mrRepo)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
		repo.EXPECT().CreateThread(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, thread domain.MergeRequestThread) (*domain.MergeRequestThread, error) {
				require.Equal(t, "old.go", thread.FilePath)
				return &thread, nil
			})
		repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, c domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
				return &c, nil
			})
		files := []diff.FileDiff{{
			Change: diff.Change{Path: "new.go", Status: diff.Renamed,
				Old: diff.Entry{Path: "old.go", Hash: domain.Hash{1}},
				New: diff.Entry{Path: "new.go", Hash: domain.Hash{2}}},
			Old: []byte("package main\n\nfunc main() {}\n"),
			New: []byte("package main\n\nfunc main() { run() }\n"),
		}}
		expectDiff(branchRepo, merger, files)

		_, err := review.AddComment(permissionCtx(9), snow.ID(1), 5, ThreadComment{FilePath: "old.go", OldLine: &inline, Body: "here"})
		require.NoError(t, err)
	})
}

func TestMergeRequestReview_Reviews_Stale(t *testing.T) {
	review, repo, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil)
	repo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return([]*domain.MergeRequestReview{
		{ID: 1, HeadCommitID: 11, State: domain.MergeRequestReviewApproved, Reviewer: domain.ReviewActor{UserID: 9}},
		{ID: 2, HeadCommitID: 10, State: domain.MergeRequestReviewApproved, Reviewer: domain.ReviewActor{UserID: 8}},
		{ID: 3, HeadCommitID: 10, State: domain.MergeRequestReviewCommented, Reviewer: domain.ReviewActor{UserID: 7}},
	}, nil)

	reviews, err := review.Reviews(permissionCtx(9), snow.ID(1), 5)
	require.NoError(t, err)
	require.Len(t, reviews, 3)
	require.False(t, reviews[0].Stale)
	require.True(t, reviews[1].Stale)
	require.True(t, reviews[2].Stale, "a comment on an older head is outdated too")
}

func TestMergeRequestReview_ReviewState(t *testing.T) {
	newReview := func(id snow.ID, user snow.ID, state string, head int64) *domain.MergeRequestReview {
		return &domain.MergeRequestReview{ID: id, HeadCommitID: snow.ID(head), State: state,
			Reviewer: domain.ReviewActor{UserID: user}}
	}

	t.Run("counts live decisions and outstanding reviewers", func(t *testing.T) {
		review, repo, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
		repo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return([]*domain.MergeRequestReview{
			newReview(1, 9, domain.MergeRequestReviewApproved, 11),
			newReview(2, 8, domain.MergeRequestReviewChangesRequested, 11),
			newReview(3, 7, domain.MergeRequestReviewCommented, 11),
		}, nil)
		repo.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return([]*domain.MergeRequestReviewRequest{
			{Reviewer: domain.ReviewActor{UserID: 9}},
			{Reviewer: domain.ReviewActor{UserID: 6}},
		}, nil)

		state, err := review.ReviewState(permissionCtx(9), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, snow.ID(11), state.HeadCommitID)
		require.Equal(t, 1, state.Approvals)
		require.Equal(t, 1, state.ChangesRequested)
		require.Equal(t, []snow.ID{6}, state.OutstandingReviewers, "a decided reviewer is no longer outstanding")
	})

	t.Run("reports dismissed and stale approvals", func(t *testing.T) {
		review, repo, mrRepo, branchRepo, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil).AnyTimes()
		now := time.Now()
		repo.EXPECT().ListReviews(gomock.Any(), int64(5)).Return([]*domain.MergeRequestReview{
			newReview(1, 9, domain.MergeRequestReviewApproved, 10),
			func() *domain.MergeRequestReview {
				r := newReview(2, 8, domain.MergeRequestReviewApproved, 11)
				r.DismissedAt = &now
				return r
			}(),
		}, nil)
		repo.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)

		state, err := review.ReviewState(permissionCtx(9), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, 0, state.Approvals)
		require.Equal(t, 2, state.DismissedApprovals)
	})
}

func TestMergeRequestReview_AttachSummaries(t *testing.T) {
	review, repo, _, _, _, _, _ := newTestMergeRequestReview(t)
	repo.EXPECT().ReviewSummaries(gomock.Any(), snow.ID(1)).Return(map[int64]*domain.MergeRequestReviewState{
		5: {Approvals: 2, HeadCommitID: 11},
	}, nil)
	withReview := &domain.MergeRequest{Number: 5}
	without := &domain.MergeRequest{Number: 6}

	require.NoError(t, review.AttachSummaries(context.Background(), snow.ID(1),
		[]*domain.MergeRequest{withReview, without}))
	require.Equal(t, 2, withReview.Review.Approvals)
	require.Nil(t, without.Review, "a merge request with no live decision has no summary")
}

func TestMergeRequestReview_WithdrawReview(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	repo.EXPECT().GetReview(gomock.Any(), int64(5), snow.ID(1)).
		Return(&domain.MergeRequestReview{ID: 1, Reviewer: domain.ReviewActor{UserID: 9}}, nil)
	repo.EXPECT().DeleteReview(gomock.Any(), int64(5), snow.ID(1)).Return(nil)

	require.NoError(t, review.WithdrawReview(permissionCtx(9), snow.ID(1), 5, snow.ID(1)))
}

func TestMergeRequestReview_WithdrawReview_OtherReviewer(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	repo.EXPECT().GetReview(gomock.Any(), int64(5), snow.ID(1)).
		Return(&domain.MergeRequestReview{ID: 1, Reviewer: domain.ReviewActor{UserID: 8}}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	require.True(t, domain.IsErrorNoPermission(review.WithdrawReview(permissionCtx(9), snow.ID(1), 5, snow.ID(1))))
}

func TestMergeRequestReview_DismissReview(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetReview(gomock.Any(), int64(5), snow.ID(1)).
		Return(&domain.MergeRequestReview{ID: 1, Reviewer: domain.ReviewActor{UserID: 8}}, nil)
	repo.EXPECT().DismissReview(gomock.Any(), int64(5), snow.ID(1), snow.ID(9), "manual", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int64, _ snow.ID, _ snow.ID, _ string, _ time.Time) error {
			return nil
		})
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			require.Equal(t, domain.MergeRequestEventReviewDismissed, event.Kind)
			require.NotNil(t, event.Subject)
			require.Equal(t, snow.ID(8), event.Subject.UserID)
			return &event, nil
		})

	dismissed, err := review.DismissReview(permissionCtx(9), snow.ID(1), 5, snow.ID(1))
	require.NoError(t, err)
	require.True(t, dismissed.Stale)
	require.NotNil(t, dismissed.DismissedAt)
	require.Equal(t, "manual", dismissed.DismissedReason)
	require.NotNil(t, dismissed.DismissedBy)
	require.Equal(t, snow.ID(9), dismissed.DismissedBy.UserID)
}

func TestMergeRequestReview_AddComment_AndReply(t *testing.T) {
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
	var persistedCommentID snow.ID
	repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, c domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
			persistedCommentID = c.ID
			return &c, nil
		})

	thread, err := review.AddComment(permissionCtx(9), snow.ID(1), 5, ThreadComment{Body: " first "})
	require.NoError(t, err)
	require.Len(t, thread.Comments, 1)
	require.Equal(t, "first", thread.Comments[0].Body)
	require.Equal(t, snow.ID(9), thread.Comments[0].User.UserID)
	require.Equal(t, persistedCommentID, thread.Comments[0].ID, "the returned comment must be the persisted one")

	repo.EXPECT().GetThread(gomock.Any(), int64(5), thread.ID).Return(thread, nil)
	repo.EXPECT().CreateComment(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, c domain.MergeRequestComment) (*domain.MergeRequestComment, error) {
			require.Equal(t, "second", c.Body)
			return &c, nil
		})

	got, err := review.Reply(permissionCtx(9), snow.ID(1), 5, thread.ID, " second ")
	require.NoError(t, err)
	require.Equal(t, "second", got.Body)
}

func TestMergeRequestReview_Reply_Validation(t *testing.T) {
	review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)

	_, err := review.Reply(permissionCtx(9), snow.ID(1), 5, snow.ID(1), "   ")
	requireUserError(t, err)
}

func TestMergeRequestReview_UpdateComment_AuthorOnly(t *testing.T) {
	review, repo, mrRepo, _, permUC, _, _ := newTestMergeRequestReview(t)
	expectLoad(permUC, mrRepo)
	repo.EXPECT().GetThread(gomock.Any(), int64(5), snow.ID(1)).Return(&domain.MergeRequestThread{ID: 1}, nil)
	repo.EXPECT().GetComment(gomock.Any(), snow.ID(1), snow.ID(2)).
		Return(&domain.MergeRequestComment{ID: 2, User: domain.ReviewActor{UserID: 8}}, nil)

	_, err := review.UpdateComment(permissionCtx(9), snow.ID(1), 5, snow.ID(1), snow.ID(2), "mine now")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestMergeRequestReview_UpdateComment(t *testing.T) {
	review, repo, mrRepo, _, permUC, _, _ := newTestMergeRequestReview(t)
	expectLoad(permUC, mrRepo)
	repo.EXPECT().GetThread(gomock.Any(), int64(5), snow.ID(1)).Return(&domain.MergeRequestThread{ID: 1}, nil)
	repo.EXPECT().GetComment(gomock.Any(), snow.ID(1), snow.ID(2)).
		Return(&domain.MergeRequestComment{ID: 2, User: domain.ReviewActor{UserID: 9}}, nil)
	repo.EXPECT().UpdateComment(gomock.Any(), snow.ID(1), snow.ID(2), "fixed").
		Return(&domain.MergeRequestComment{ID: 2, Body: "fixed"}, nil)

	got, err := review.UpdateComment(permissionCtx(9), snow.ID(1), 5, snow.ID(1), snow.ID(2), " fixed ")
	require.NoError(t, err)
	require.Equal(t, "fixed", got.Body)
}

func TestMergeRequestReview_DeleteComment_AdminOverride(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	expectLoad(perm, mrRepo)
	repo.EXPECT().GetThread(gomock.Any(), int64(5), snow.ID(1)).Return(&domain.MergeRequestThread{ID: 1}, nil)
	repo.EXPECT().GetComment(gomock.Any(), snow.ID(1), snow.ID(2)).
		Return(&domain.MergeRequestComment{ID: 2, User: domain.ReviewActor{UserID: 8}}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().DeleteComment(gomock.Any(), snow.ID(1), snow.ID(2)).Return(nil)

	require.NoError(t, review.DeleteComment(permissionCtx(9), snow.ID(1), 5, snow.ID(1), snow.ID(2)))
}

func TestMergeRequestReview_Threads_Outdated(t *testing.T) {
	review, repo, mrRepo, branchRepo, permUC, _, _ := newTestMergeRequestReview(t)
	expectLoad(permUC, mrRepo)
	repo.EXPECT().ListThreads(gomock.Any(), int64(5), nil).Return([]*domain.MergeRequestThread{
		{ID: 1, FilePath: "main.go", HeadCommitID: snowPtr(11)},
		{ID: 2, FilePath: "main.go", HeadCommitID: snowPtr(10)},
		{ID: 3, FilePath: "main.go"},
		{ID: 4, HeadCommitID: snowPtr(10)},
	}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: snowPtr(11)}, nil)

	threads, err := review.Threads(permissionCtx(9), snow.ID(1), 5, nil)
	require.NoError(t, err)
	require.False(t, threads[0].Outdated)
	require.True(t, threads[1].Outdated)
	require.False(t, threads[2].Outdated, "a thread without a head commit cannot be outdated")
	require.False(t, threads[3].Outdated, "a top-level conversation is not about the diff")
}

func TestMergeRequestReview_SetThreadResolved(t *testing.T) {
	review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).Times(2)
	expectLoad(perm, mrRepo)
	expectLoad(perm, mrRepo)
	repo.EXPECT().GetThread(gomock.Any(), int64(5), snow.ID(1)).Return(&domain.MergeRequestThread{ID: 1}, nil).Times(2)
	repo.EXPECT().SetThreadResolved(gomock.Any(), int64(5), snow.ID(1), true, snowPtr(9), gomock.Any()).
		Return(&domain.MergeRequestThread{ID: 1, Resolved: true}, nil)
	repo.EXPECT().SetThreadResolved(gomock.Any(), int64(5), snow.ID(1), false, nil, nil).
		Return(&domain.MergeRequestThread{ID: 1, Resolved: false}, nil)

	resolved, err := review.SetThreadResolved(permissionCtx(9), snow.ID(1), 5, snow.ID(1), true)
	require.NoError(t, err)
	require.True(t, resolved.Resolved)

	reopened, err := review.SetThreadResolved(permissionCtx(9), snow.ID(1), 5, snow.ID(1), false)
	require.NoError(t, err)
	require.False(t, reopened.Resolved)
}

func TestMergeRequestReview_DeleteThread_AuthorOrAdmin(t *testing.T) {
	t.Run("author", func(t *testing.T) {
		review, repo, mrRepo, _, permUC, _, _ := newTestMergeRequestReview(t)
		expectLoad(permUC, mrRepo)
		repo.EXPECT().GetThread(gomock.Any(), int64(5), snow.ID(1)).
			Return(&domain.MergeRequestThread{ID: 1, CreatedBy: domain.ReviewActor{UserID: 9}}, nil)
		repo.EXPECT().DeleteThread(gomock.Any(), int64(5), snow.ID(1)).Return(nil)

		require.NoError(t, review.DeleteThread(permissionCtx(9), snow.ID(1), 5, snow.ID(1)))
	})

	t.Run("someone else", func(t *testing.T) {
		review, repo, mrRepo, _, permUC, _, _ := newTestMergeRequestReview(t)
		expectLoad(permUC, mrRepo)
		repo.EXPECT().GetThread(gomock.Any(), int64(5), snow.ID(1)).
			Return(&domain.MergeRequestThread{ID: 1, CreatedBy: domain.ReviewActor{UserID: 8}}, nil)
		permUC.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

		require.True(t, domain.IsErrorNoPermission(review.DeleteThread(permissionCtx(9), snow.ID(1), 5, snow.ID(1))))
	})
}

func TestMergeRequestReview_RequestReview(t *testing.T) {
	review, repo, mrRepo, _, perm, _, users := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)
	users.EXPECT().GetByID(gomock.Any(), snow.ID(8)).Return(&domain.User{ID: 8, Name: "Rev"}, nil)
	repo.EXPECT().CreateReviewRequest(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req domain.MergeRequestReviewRequest) (*domain.MergeRequestReviewRequest, error) {
			require.Equal(t, int64(5), req.MergeRequestID)
			require.Equal(t, snow.ID(8), req.Reviewer.UserID)
			require.Equal(t, snow.ID(9), req.RequestedBy.UserID)
			return &req, nil
		})
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			require.Equal(t, domain.MergeRequestEventReviewRequested, event.Kind)
			require.NotNil(t, event.Subject, "the request must name who was asked")
			require.Equal(t, snow.ID(8), event.Subject.UserID)
			return &event, nil
		})

	got, err := review.RequestReview(permissionCtx(9), snow.ID(1), 5, snow.ID(8))
	require.NoError(t, err)
	require.Equal(t, snow.ID(8), got.Reviewer.UserID)
}

func TestMergeRequestReview_RequestReview_SelfRequestRejected(t *testing.T) {
	review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)

	_, err := review.RequestReview(permissionCtx(9), snow.ID(1), 5, snow.ID(9))
	requireUserError(t, err)
}

func TestMergeRequestReview_RequestReview_UnknownReviewerRejected(t *testing.T) {
	review, _, mrRepo, _, perm, _, users := newTestMergeRequestReview(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	expectLoad(perm, mrRepo)
	users.EXPECT().GetByID(gomock.Any(), snow.ID(8)).Return(nil, domain.NewErrorRecordNotFound())

	_, err := review.RequestReview(permissionCtx(9), snow.ID(1), 5, snow.ID(8))
	requireUserError(t, err)
}

func TestMergeRequestReview_RemoveReviewRequest(t *testing.T) {
	t.Run("reviewer may withdraw their own", func(t *testing.T) {
		review, repo, mrRepo, _, permUC, _, _ := newTestMergeRequestReview(t)
		expectLoad(permUC, mrRepo)
		repo.EXPECT().DeleteReviewRequest(gomock.Any(), int64(5), snow.ID(8)).Return(nil)
		repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
				require.Equal(t, domain.MergeRequestEventReviewUnrequested, event.Kind)
				require.Equal(t, snow.ID(8), event.Subject.UserID)
				return &event, nil
			})

		require.NoError(t, review.RemoveReviewRequest(permissionCtx(8), snow.ID(1), 5, snow.ID(8)))
	})

	t.Run("a third party may not", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		expectLoad(perm, mrRepo)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)

		require.True(t, domain.IsErrorNoPermission(review.RemoveReviewRequest(permissionCtx(9), snow.ID(1), 5, snow.ID(8))))
	})
}

func TestMergeRequestReview_NoteBranchPush(t *testing.T) {
	review, repo, _, _, _, _, _ := newTestMergeRequestReview(t)
	repo.EXPECT().ListOpenBySourceBranch(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return([]*domain.MergeRequest{{ID: 5, Number: 5}, {ID: 6, Number: 6}}, nil)
	repo.EXPECT().DismissStaleReviews(gomock.Any(), gomock.Any(), snow.ID(11), snow.ID(9),
		domain.MergeRequestDismissedNewCommits, gomock.Any()).Return(nil).Times(2)
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, event domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			require.Equal(t, domain.MergeRequestEventPushed, event.Kind)
			require.Equal(t, snowPtr(11), event.CommitID)
			require.NotEmpty(t, event.CommitHash)
			return &event, nil
		}).Times(2)

	require.NoError(t, review.NoteBranchPush(context.Background(), snow.ID(1), snow.ID(3), snow.ID(11), snow.ID(9), "cafe"))
}

func TestMergeRequestReview_NoteBranchPush_NoOpenRequests(t *testing.T) {
	review, repo, _, _, _, _, _ := newTestMergeRequestReview(t)
	repo.EXPECT().ListOpenBySourceBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil, nil)

	require.NoError(t, review.NoteBranchPush(context.Background(), snow.ID(1), snow.ID(3), snow.ID(11), snow.ID(9), "cafe"))
}

func TestMergeRequestReview_Load_Rejections(t *testing.T) {
	t.Run("invalid number", func(t *testing.T) {
		review, _, _, _, _, _, _ := newTestMergeRequestReview(t)
		_, err := review.Reviews(permissionCtx(9), snow.ID(1), 0)
		requireUserError(t, err)
	})

	t.Run("no read permission", func(t *testing.T) {
		review, _, _, _, perm, _, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)

		_, err := review.Reviews(permissionCtx(9), snow.ID(1), 5)
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("unknown merge request", func(t *testing.T) {
		review, _, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).
			Return(nil, domain.NewErrorNotFound("merge request not found"))

		_, err := review.Reviews(permissionCtx(9), snow.ID(1), 5)
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("missing thread", func(t *testing.T) {
		review, repo, mrRepo, _, perm, _, _ := newTestMergeRequestReview(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		expectLoad(perm, mrRepo)
		repo.EXPECT().GetThread(gomock.Any(), int64(5), snow.ID(1)).
			Return(nil, domain.NewErrorNotFound("thread not found"))

		_, err := review.Reply(permissionCtx(9), snow.ID(1), 5, snow.ID(1), "hi")
		require.True(t, domain.IsErrorNotFound(err))
	})
}
