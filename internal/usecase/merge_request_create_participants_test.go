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

type txKey struct{}

type recordingTransactor struct{ calls int }

func (r *recordingTransactor) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	r.calls++
	return fn(context.WithValue(ctx, txKey{}, true))
}

func inTx(ctx context.Context) bool {
	v, _ := ctx.Value(txKey{}).(bool)
	return v
}

type participantsFixture struct {
	mr         *MergeRequest
	repo       *MockmergeRequestRepository
	reviewRepo *MockmergeRequestReviewRepository
	branchRepo *MockbranchRepository
	perm       *MockpermissionUsecase
	merger     *MockbranchMerger
	users      *MockuserLookup
	hooks      *MockhookMergeRequestGate
	tx         *recordingTransactor
}

func newParticipantsFixture(t *testing.T, withReview bool) *participantsFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &participantsFixture{
		repo:       NewMockmergeRequestRepository(ctrl),
		reviewRepo: NewMockmergeRequestReviewRepository(ctrl),
		branchRepo: NewMockbranchRepository(ctrl),
		perm:       NewMockpermissionUsecase(ctrl),
		merger:     NewMockbranchMerger(ctrl),
		users:      NewMockuserLookup(ctrl),
		hooks:      NewMockhookMergeRequestGate(ctrl),
		tx:         &recordingTransactor{},
	}
	node := newTestBranchNode(t)
	f.mr = NewMergeRequest(f.repo, f.branchRepo, f.perm, f.merger, node, f.tx).
		WithUsers(f.users).
		WithAccessChecker(stubAccessChecker{allowed: map[snow.ID]bool{8: true, 9: true}}).
		WithHooks(f.hooks)
	if withReview {
		f.mr.WithReview(NewMergeRequestReview(f.reviewRepo, f.repo, f.branchRepo, f.merger, f.perm, f.users, node))
	}
	return f
}

func (f *participantsFixture) expectUsers() {
	f.users.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id snow.ID) (*domain.User, error) {
			return &domain.User{ID: id}, nil
		}).AnyTimes()
}

func (f *participantsFixture) expectBranches() {
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)
	f.branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	f.branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	f.merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any()).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
}

func (f *participantsFixture) expectCreate(t *testing.T) {
	f.repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, in domain.MergeRequest) (*domain.MergeRequest, error) {
			require.True(t, inTx(ctx))
			in.Number = 5
			return &in, nil
		})
}

func participantsInput(reviewers, assignees []snow.ID) CreateMergeRequestInput {
	return CreateMergeRequestInput{
		Title: "Feature", SourceBranch: "feature", TargetBranch: "main",
		ReviewerIDs: reviewers, AssigneeIDs: assignees,
	}
}

func TestMergeRequest_Create_WithReviewersAndAssignees(t *testing.T) {
	f := newParticipantsFixture(t, true)
	f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	f.expectUsers()
	f.expectBranches()
	f.expectCreate(t)

	var order []string
	f.reviewRepo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, item domain.MergeRequestTimelineItem) (*domain.MergeRequestTimelineItem, error) {
			require.True(t, inTx(ctx))
			require.Equal(t, snow.ID(7), item.Actor.UserID)
			if item.Kind == domain.MergeRequestEventReviewRequested {
				order = append(order, item.Kind+":"+item.Subject.UserID.Base36())
			} else {
				order = append(order, item.Kind)
			}
			return &item, nil
		}).Times(3)
	var assigned []snow.ID
	f.repo.EXPECT().AddAssignee(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ int64, id snow.ID) error {
			require.True(t, inTx(ctx))
			assigned = append(assigned, id)
			return nil
		}).Times(2)
	var requested []snow.ID
	f.reviewRepo.EXPECT().CreateReviewRequest(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, req domain.MergeRequestReviewRequest) (*domain.MergeRequestReviewRequest, error) {
			require.True(t, inTx(ctx))
			require.Equal(t, snow.ID(7), req.RequestedBy.UserID)
			requested = append(requested, req.Reviewer.UserID)
			return &req, nil
		}).Times(2)
	f.repo.EXPECT().ListAssignees(gomock.Any(), snow.ID(1)).
		Return(map[int64][]domain.ReviewActor{5: {{UserID: 8}, {UserID: 9}}}, nil)
	f.hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRCreated, snow.ID(1), gomock.Any(), snow.ID(7)).DoAndReturn(
		func(_ context.Context, _ string, _ snow.ID, mr *domain.MergeRequest, _ snow.ID) error {
			require.Len(t, mr.Assignees, 2)
			return nil
		})

	created, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput([]snow.ID{8, 9, 8}, []snow.ID{9, 8, 9}))
	require.NoError(t, err)
	require.Len(t, created.Assignees, 2)
	require.Equal(t, 1, f.tx.calls)
	require.Equal(t, []snow.ID{9, 8}, assigned)
	require.Equal(t, []snow.ID{8, 9}, requested)
	require.Equal(t, []string{
		domain.MergeRequestEventOpened,
		domain.MergeRequestEventReviewRequested + ":" + snow.ID(8).Base36(),
		domain.MergeRequestEventReviewRequested + ":" + snow.ID(9).Base36(),
	}, order)
}

func TestMergeRequest_Create_AssigneesOnlyWithoutReviewSeam(t *testing.T) {
	f := newParticipantsFixture(t, false)
	f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	f.expectUsers()
	f.expectBranches()
	f.expectCreate(t)
	f.repo.EXPECT().AddAssignee(gomock.Any(), gomock.Any(), snow.ID(8)).Return(nil)
	f.repo.EXPECT().ListAssignees(gomock.Any(), snow.ID(1)).Return(nil, errors.New("boom"))
	f.hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRCreated, snow.ID(1), gomock.Any(), snow.ID(7)).Return(nil)

	created, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput(nil, []snow.ID{8}))
	require.NoError(t, err)
	require.Empty(t, created.Assignees)
}

func TestMergeRequest_Create_ParticipantWriteFailuresRollBack(t *testing.T) {
	t.Run("assignee", func(t *testing.T) {
		f := newParticipantsFixture(t, true)
		f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		f.expectUsers()
		f.expectBranches()
		f.expectCreate(t)
		f.reviewRepo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(&domain.MergeRequestTimelineItem{}, nil)
		f.repo.EXPECT().AddAssignee(gomock.Any(), gomock.Any(), snow.ID(8)).Return(errors.New("boom"))

		_, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput([]snow.ID{9}, []snow.ID{8}))
		require.EqualError(t, err, "boom")
	})

	t.Run("review request", func(t *testing.T) {
		f := newParticipantsFixture(t, true)
		f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		f.expectUsers()
		f.expectBranches()
		f.expectCreate(t)
		f.reviewRepo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(&domain.MergeRequestTimelineItem{}, nil)
		f.reviewRepo.EXPECT().CreateReviewRequest(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

		_, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput([]snow.ID{9}, nil))
		require.EqualError(t, err, "boom")
	})

	t.Run("review requested event", func(t *testing.T) {
		f := newParticipantsFixture(t, true)
		f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		f.expectUsers()
		f.expectBranches()
		f.expectCreate(t)
		gomock.InOrder(
			f.reviewRepo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(&domain.MergeRequestTimelineItem{}, nil),
			f.reviewRepo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom")),
		)
		f.reviewRepo.EXPECT().CreateReviewRequest(gomock.Any(), gomock.Any()).Return(&domain.MergeRequestReviewRequest{}, nil)

		_, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput([]snow.ID{9}, nil))
		require.EqualError(t, err, "boom")
	})

	t.Run("opened event", func(t *testing.T) {
		f := newParticipantsFixture(t, true)
		f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		f.expectUsers()
		f.expectBranches()
		f.expectCreate(t)
		f.reviewRepo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))

		_, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput([]snow.ID{9}, nil))
		require.EqualError(t, err, "boom")
	})
}

func TestMergeRequest_Create_ParticipantsNeedWrite(t *testing.T) {
	f := newParticipantsFixture(t, true)
	f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false).Times(2)

	_, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput([]snow.ID{8}, nil))
	require.True(t, domain.IsErrorNoPermission(err))
	_, err = f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput(nil, []snow.ID{8}))
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestMergeRequest_Create_PlainNeedsOnlyRead(t *testing.T) {
	f := newParticipantsFixture(t, true)
	f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	f.expectBranches()
	f.expectCreate(t)
	f.reviewRepo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(&domain.MergeRequestTimelineItem{}, nil)
	f.hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRCreated, snow.ID(1), gomock.Any(), snow.ID(7)).Return(nil)

	_, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput(nil, nil))
	require.NoError(t, err)
}

func TestMergeRequest_Create_RejectsParticipants(t *testing.T) {
	cases := []struct {
		name      string
		review    bool
		reviewers []snow.ID
		assignees []snow.ID
		message   string
	}{
		{"self review", true, []snow.ID{8, 7}, nil, "you cannot request a review from yourself"},
		{"unknown reviewer", true, []snow.ID{404}, nil, "user " + snow.ID(404).Base36() + " not found"},
		{"unknown assignee", true, nil, []snow.ID{404}, "user " + snow.ID(404).Base36() + " not found"},
		{"reviewer without access", true, []snow.ID{10}, nil, "user " + snow.ID(10).Base36() + " not found"},
		{"assignee without access", true, []snow.ID{8}, []snow.ID{10}, "user " + snow.ID(10).Base36() + " not found"},
		{"no review seam", false, []snow.ID{8}, nil, "review requests are not enabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newParticipantsFixture(t, tc.review)
			f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
			f.users.EXPECT().GetByID(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, id snow.ID) (*domain.User, error) {
					if id == 404 {
						return nil, domain.NewErrorRecordNotFound()
					}
					return &domain.User{ID: id}, nil
				}).AnyTimes()

			_, err := f.mr.Create(permissionCtx(7), snow.ID(1), participantsInput(tc.reviewers, tc.assignees))
			requireUserError(t, err)
			require.Contains(t, err.Error(), tc.message)
			require.Zero(t, f.tx.calls)
		})
	}
}
