package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newTestMergeRequestCheck(t *testing.T) (*MergeRequestCheck, *MockmergeRequestCheckRepository, *MockmergeRequestRepository, *MockbranchRepository, *MockpermissionUsecase) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockmergeRequestCheckRepository(ctrl)
	mrRepo := NewMockmergeRequestRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	uc := NewMergeRequestCheck(repo, mrRepo, branchRepo, perm, newTestBranchNode(t))
	return uc, repo, mrRepo, branchRepo, perm
}

func TestMergeRequestCheck_Report(t *testing.T) {
	uc, repo, mrRepo, branchRepo, perm := newTestMergeRequestCheck(t)
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(branchWithHead(3, head), nil)
	repo.EXPECT().Upsert(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, check domain.MergeRequestCheck) (*domain.MergeRequestCheck, error) {
			require.Equal(t, int64(5), check.MergeRequestID)
			require.Equal(t, head, check.HeadCommitID)
			require.Equal(t, "build", check.Name)
			require.Equal(t, domain.MergeRequestCheckSuccess, check.State)
			require.Equal(t, "https://ci.example/run/1", check.DetailsURL)
			require.Equal(t, snow.ID(7), check.Reporter.UserID)
			return &check, nil
		})

	check, err := uc.Report(permissionCtx(7), snow.ID(1), 5, " build ", "success", "https://ci.example/run/1")
	require.NoError(t, err)
	require.Equal(t, "build", check.Name)
}

func TestMergeRequestCheck_Report_Validation(t *testing.T) {
	t.Run("needs write access", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("rejects an empty name", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "  ", "success", "")
		requireUserError(t, err)
	})

	t.Run("rejects an unknown state", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "exploded", "")
		requireUserError(t, err)
	})

	t.Run("rejects a missing source head", func(t *testing.T) {
		uc, _, mrRepo, branchRepo, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1}, nil)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.True(t, domain.IsErrorConflict(err))
	})
}

func TestMergeRequestCheck_List(t *testing.T) {
	uc, repo, mrRepo, branchRepo, perm := newTestMergeRequestCheck(t)
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(branchWithHead(3, head), nil)
	repo.EXPECT().List(gomock.Any(), int64(5), head).Return([]*domain.MergeRequestCheck{{Name: "build"}}, nil)

	checks, err := uc.List(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Len(t, checks, 1)
}

func TestMergeRequestCheck_Report_Additional(t *testing.T) {
	t.Run("requires a claim", func(t *testing.T) {
		uc, _, _, _, _ := newTestMergeRequestCheck(t)
		_, err := uc.Report(context.Background(), snow.ID(1), 5, "build", "success", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("rejects an oversized name", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, strings.Repeat("a", 129), "success", "")
		requireUserError(t, err)
	})

	t.Run("propagates a load error", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("propagates a source head error", func(t *testing.T) {
		uc, _, mrRepo, branchRepo, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, errors.New("boom"))
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.EqualError(t, err, "boom")
	})

	t.Run("propagates an upsert error", func(t *testing.T) {
		uc, repo, mrRepo, branchRepo, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(branchWithHead(3, snow.ID(11)), nil)
		repo.EXPECT().Upsert(gomock.Any(), gomock.Any()).Return(nil, errors.New("boom"))
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.EqualError(t, err, "boom")
	})

	t.Run("emits a hook", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := NewMockmergeRequestCheckRepository(ctrl)
		mrRepo := NewMockmergeRequestRepository(ctrl)
		branchRepo := NewMockbranchRepository(ctrl)
		perm := NewMockpermissionUsecase(ctrl)
		hooks := NewMockhookMergeRequestGate(ctrl)
		uc := NewMergeRequestCheck(repo, mrRepo, branchRepo, perm, newTestBranchNode(t)).WithHooks(hooks)
		require.NotNil(t, uc)

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).Times(2)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).Times(2)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).Times(2)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(branchWithHead(3, snow.ID(11)), nil).Times(2)
		repo.EXPECT().Upsert(gomock.Any(), gomock.Any()).
			Return(&domain.MergeRequestCheck{ID: snow.ID(9)}, nil).Times(2)
		hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRCheckReported, snow.ID(1), openMergeRequest(), snow.ID(7)).
			Return(nil)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.NoError(t, err)

		hooks.EXPECT().EmitMergeRequest(gomock.Any(), domain.WebhookEventMRCheckReported, snow.ID(1), openMergeRequest(), snow.ID(7)).
			Return(errors.New("boom"))
		_, err = uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.NoError(t, err)
	})
}

func TestMergeRequestCheck_List_Additional(t *testing.T) {
	t.Run("propagates a load error", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := uc.List(permissionCtx(7), snow.ID(1), 5)
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("returns empty when the source branch is gone", func(t *testing.T) {
		uc, repo, mrRepo, branchRepo, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorNotFound("gone"))
		checks, err := uc.List(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Empty(t, checks)
		require.False(t, repo == nil)
	})

	t.Run("propagates a source head error", func(t *testing.T) {
		uc, _, mrRepo, branchRepo, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, errors.New("boom"))
		_, err := uc.List(permissionCtx(7), snow.ID(1), 5)
		require.EqualError(t, err, "boom")
	})
}

func TestMergeRequestCheck_BlockedBy_Additional(t *testing.T) {
	t.Run("propagates a required checks error", func(t *testing.T) {
		uc, _, _, branchRepo, _ := newTestMergeRequestCheck(t)
		target := &domain.Branch{ID: 2, RequireStatusChecks: true}
		branchRepo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(2)).Return(nil, errors.New("boom"))
		_, err := uc.BlockedBy(context.Background(), snow.ID(1), openMergeRequest(), target, snow.ID(11))
		require.EqualError(t, err, "boom")
	})

	t.Run("is a no-op without required checks", func(t *testing.T) {
		uc, _, _, branchRepo, _ := newTestMergeRequestCheck(t)
		target := &domain.Branch{ID: 2, RequireStatusChecks: true}
		branchRepo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(2)).Return([]string{}, nil)
		blocked, err := uc.BlockedBy(context.Background(), snow.ID(1), openMergeRequest(), target, snow.ID(11))
		require.NoError(t, err)
		require.Empty(t, blocked)
	})

	t.Run("propagates a checks list error", func(t *testing.T) {
		uc, repo, _, branchRepo, _ := newTestMergeRequestCheck(t)
		target := &domain.Branch{ID: 2, RequireStatusChecks: true}
		branchRepo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(2)).Return([]string{"build"}, nil)
		repo.EXPECT().List(gomock.Any(), int64(5), snow.ID(11)).Return(nil, errors.New("boom"))
		_, err := uc.BlockedBy(context.Background(), snow.ID(1), openMergeRequest(), target, snow.ID(11))
		require.EqualError(t, err, "boom")
	})
}

func TestMergeRequestCheck_Load(t *testing.T) {
	t.Run("rejects a non-positive number", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 0, "build", "success", "")
		requireUserError(t, err)
	})

	t.Run("requires read access", func(t *testing.T) {
		uc, _, _, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("maps a missing request to a not found error", func(t *testing.T) {
		uc, _, mrRepo, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(nil, domain.NewErrorNotFound("gone"))
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("propagates a generic get error", func(t *testing.T) {
		uc, _, mrRepo, _, perm := newTestMergeRequestCheck(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		mrRepo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(nil, errors.New("boom"))
		_, err := uc.Report(permissionCtx(7), snow.ID(1), 5, "build", "success", "")
		require.EqualError(t, err, "boom")
	})
}

func TestMergeRequestCheck_BlockedBy(t *testing.T) {
	uc, repo, _, branchRepo, _ := newTestMergeRequestCheck(t)
	target := &domain.Branch{ID: 2, ProjectID: 1, RequireStatusChecks: true}
	head := snow.ID(11)
	mr := openMergeRequest()

	branchRepo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(2)).Return([]string{"build"}, nil)
	repo.EXPECT().List(gomock.Any(), int64(5), head).Return([]*domain.MergeRequestCheck{
		{Name: "build", State: domain.MergeRequestCheckFailed},
	}, nil)
	blocked, err := uc.BlockedBy(context.Background(), snow.ID(1), mr, target, head)
	require.NoError(t, err)
	require.Equal(t, domain.MergeabilityBlockedChecks, blocked)

	branchRepo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(2)).Return([]string{"build"}, nil)
	repo.EXPECT().List(gomock.Any(), int64(5), head).Return([]*domain.MergeRequestCheck{
		{Name: "build", State: domain.MergeRequestCheckSuccess},
	}, nil)
	blocked, err = uc.BlockedBy(context.Background(), snow.ID(1), mr, target, head)
	require.NoError(t, err)
	require.Empty(t, blocked)

	// checks are not required: the gate is a no-op
	blocked, err = uc.BlockedBy(context.Background(), snow.ID(1), mr, &domain.Branch{ID: 2}, head)
	require.NoError(t, err)
	require.Empty(t, blocked)
}
