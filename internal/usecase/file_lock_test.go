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

func newTestFileLock(t *testing.T) (*FileLock, *MockfileLockRepository, *MockbranchRepository, *MockpermissionUsecase) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockfileLockRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return NewFileLock(repo, branchRepo, perm, node), repo, branchRepo, perm
}

func defaultBranchFixture() *domain.Branch {
	return &domain.Branch{ID: 2, ProjectID: 1, Name: "main", IsDefault: true}
}

func devBranchFixture() *domain.Branch {
	return &domain.Branch{ID: 3, ProjectID: 1, Name: "feature"}
}

func snowPtr(id snow.ID) *snow.ID {
	return &id
}

func int64Ptr(v int64) *int64 {
	return &v
}

func TestFileLock_Acquire(t *testing.T) {
	t.Run("global scope on the default branch", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		ctx := permissionCtx(7)

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, lock domain.FileLock) (*domain.FileLock, error) {
				require.Nil(t, lock.BranchID)
				require.Equal(t, "assets/orc.png", lock.Path)
				require.Equal(t, snow.ID(7), lock.HeldBy)
				require.Nil(t, lock.MergeRequestID)
				created := lock
				created.AcquiredAt = time.Unix(100, 0)
				return &created, nil
			},
		)

		lock, err := uc.Acquire(ctx, snow.ID(1), "  ", "assets/orc.png")
		require.NoError(t, err)
		require.Equal(t, "main", lock.Branch)
		require.Nil(t, lock.BranchID)
	})

	t.Run("branch scope on a development branch", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		ctx := permissionCtx(7)

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(devBranchFixture(), nil)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, lock domain.FileLock) (*domain.FileLock, error) {
				require.NotNil(t, lock.BranchID)
				require.Equal(t, snow.ID(3), *lock.BranchID)
				created := lock
				return &created, nil
			},
		)

		lock, err := uc.Acquire(ctx, snow.ID(1), "feature", "assets/orc.png")
		require.NoError(t, err)
		require.Equal(t, "feature", lock.Branch)
	})

	t.Run("conflict with another holder", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		ctx := permissionCtx(7)

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		held := &domain.FileLock{ID: 9, Path: "assets/orc.png", HeldBy: 8, HeldByName: "bob", AcquiredAt: time.Unix(50, 0)}
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{held}, nil)

		_, err := uc.Acquire(ctx, snow.ID(1), "", "assets/orc.png")
		require.True(t, domain.IsErrorConflict(err))
		require.Contains(t, err.Error(), "bob")
	})

	t.Run("idempotent for the same holder", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		ctx := permissionCtx(7)

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		held := &domain.FileLock{ID: 9, Path: "assets/orc.png", HeldBy: 7}
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{held}, nil)

		lock, err := uc.Acquire(ctx, snow.ID(1), "", "assets/orc.png")
		require.NoError(t, err)
		require.Equal(t, snow.ID(9), lock.ID)
	})

	t.Run("covered by a directory lock held by another user", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		ctx := permissionCtx(7)

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		held := &domain.FileLock{ID: 9, Path: "assets", HeldBy: 8, HeldByName: "bob"}
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{held}, nil)

		_, err := uc.Acquire(ctx, snow.ID(1), "", "assets/orc.png")
		require.True(t, domain.IsErrorConflict(err))
	})

	t.Run("requires write access", func(t *testing.T) {
		uc, _, _, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)
		_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "", "a.png")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("requires a path", func(t *testing.T) {
		uc, _, _, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "", "  ")
		requireUserError(t, err)
	})

	t.Run("branch not found", func(t *testing.T) {
		uc, _, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "ghost").Return(nil, domain.NewErrorRecordNotFound())
		_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "ghost", "a.png")
		require.True(t, domain.IsErrorNotFound(err))
	})
}

func TestFileLock_Release(t *testing.T) {
	t.Run("owner releases", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(&domain.FileLock{ID: 9, HeldBy: 7}, nil)
		repo.EXPECT().Delete(gomock.Any(), snow.ID(9)).Return(nil)

		require.NoError(t, uc.Release(permissionCtx(7), snow.ID(1), "", "a.png"))
	})

	t.Run("other user is denied", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(&domain.FileLock{ID: 9, HeldBy: 8}, nil)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

		err := uc.Release(permissionCtx(7), snow.ID(1), "", "a.png")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("project admin can steal", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(&domain.FileLock{ID: 9, HeldBy: 8}, nil)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Delete(gomock.Any(), snow.ID(9)).Return(nil)

		require.NoError(t, uc.Release(permissionCtx(7), snow.ID(1), "", "a.png"))
	})

	t.Run("missing lock", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(nil, domain.NewErrorRecordNotFound())

		err := uc.Release(permissionCtx(7), snow.ID(1), "", "a.png")
		require.True(t, domain.IsErrorNotFound(err))
	})
}

func TestFileLock_List(t *testing.T) {
	t.Run("lists locks with read access", func(t *testing.T) {
		uc, repo, _, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)

		locks, err := uc.List(permissionCtx(7), snow.ID(1))
		require.NoError(t, err)
		require.NotNil(t, locks)
	})

	t.Run("requires read access", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		uc := NewFileLock(NewMockfileLockRepository(ctrl), NewMockbranchRepository(ctrl), perm, nil)

		_, err := uc.List(permissionCtx(7), snow.ID(1))
		require.True(t, domain.IsErrorNoPermission(err))
	})
}

func TestFileLock_EnsureLocks(t *testing.T) {
	t.Run("held by caller passes", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		locks := []*domain.FileLock{
			{ID: 1, Path: "assets", HeldBy: 7},
			{ID: 2, Path: "sound/loop.wav", HeldBy: 7},
		}
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(locks, nil)

		err := uc.EnsureLocks(permissionCtx(7), snow.ID(1), defaultBranchFixture(), []string{"assets/orc.png", "sound/loop.wav"}, 7)
		require.NoError(t, err)
	})

	t.Run("missing lock is a conflict", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)

		err := uc.EnsureLocks(permissionCtx(7), snow.ID(1), defaultBranchFixture(), []string{"a.png"}, 7)
		require.True(t, domain.IsErrorConflict(err))
		require.Contains(t, err.Error(), "requires a lock")
	})

	t.Run("other holder is a conflict", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		held := &domain.FileLock{ID: 1, Path: "a.png", HeldBy: 8, HeldByName: "bob", MergeRequestNumber: int64Ptr(4)}
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{held}, nil)

		err := uc.EnsureLocks(permissionCtx(7), snow.ID(1), defaultBranchFixture(), []string{"a.png"}, 7)
		require.True(t, domain.IsErrorConflict(err))
		require.Contains(t, err.Error(), "bob")
		require.Contains(t, err.Error(), "#4")
	})

	t.Run("branch scoped lock does not cover the mainline", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		branchID := snow.ID(3)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{
			{ID: 1, Path: "a.png", BranchID: &branchID, HeldBy: 7},
		}, nil)

		err := uc.EnsureLocks(permissionCtx(7), snow.ID(1), defaultBranchFixture(), []string{"a.png"}, 7)
		require.True(t, domain.IsErrorConflict(err))
	})

	t.Run("no paths is a no-op", func(t *testing.T) {
		uc, _, _, _ := newTestFileLock(t)
		require.NoError(t, uc.EnsureLocks(permissionCtx(7), snow.ID(1), defaultBranchFixture(), nil, 7))
	})
}

func TestFileLock_ReleaseLanded(t *testing.T) {
	uc, repo, _, _ := newTestFileLock(t)
	prefixLock := &domain.FileLock{ID: 1, Path: "assets", HeldBy: 7}
	exactLock := &domain.FileLock{ID: 2, Path: "assets/orc.png", HeldBy: 7}
	mrLock := &domain.FileLock{ID: 3, Path: "assets/dragon.png", HeldBy: 7, MergeRequestID: snowPtr(9)}
	otherLock := &domain.FileLock{ID: 4, Path: "assets/boss.png", HeldBy: 8}
	repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(
		[]*domain.FileLock{prefixLock, exactLock, mrLock, otherLock}, nil)
	repo.EXPECT().Delete(gomock.Any(), snow.ID(2)).Return(nil)

	err := uc.ReleaseLanded(permissionCtx(7), snow.ID(1), defaultBranchFixture(),
		[]string{"assets/orc.png", "assets/dragon.png", "assets/boss.png"}, 7)
	require.NoError(t, err)
}

func TestFileLock_EnsureMergeRequestLocks(t *testing.T) {
	t.Run("free paths gain request-linked locks", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, lock domain.FileLock) (*domain.FileLock, error) {
				require.NotNil(t, lock.MergeRequestID)
				require.Equal(t, snow.ID(5), *lock.MergeRequestID)
				require.Equal(t, snow.ID(7), lock.HeldBy)
				require.Nil(t, lock.BranchID)
				return &lock, nil
			},
		)

		err := uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), []string{"a.png"}, 7, 7)
		require.NoError(t, err)
	})

	t.Run("author lock covers the path", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{
			{ID: 1, Path: "assets", HeldBy: 7},
		}, nil)

		err := uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), []string{"assets/orc.png"}, 7, 7)
		require.NoError(t, err)
	})

	t.Run("another holder conflicts", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{
			{ID: 1, Path: "a.png", HeldBy: 8, HeldByName: "bob"},
		}, nil)

		err := uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), []string{"a.png"}, 7, 7)
		require.True(t, domain.IsErrorConflict(err))
	})
}

func TestFileLock_ReleaseDelegates(t *testing.T) {
	uc, repo, _, _ := newTestFileLock(t)
	repo.EXPECT().DeleteByMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(nil)
	repo.EXPECT().DeleteByBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil)

	require.NoError(t, uc.ReleaseForMergeRequest(permissionCtx(7), snow.ID(1), 5))
	require.NoError(t, uc.ReleaseBranch(permissionCtx(7), snow.ID(1), 3))
}

func TestFileLock_Acquire_OwnNarrowerLockDoesNotBlockWiderRequest(t *testing.T) {
	uc, repo, branchRepo, perm := newTestFileLock(t)
	ctx := permissionCtx(7)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
	held := &domain.FileLock{ID: 9, Path: "art/hero.png", HeldBy: 7}
	repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{held}, nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, lock domain.FileLock) (*domain.FileLock, error) {
			require.Equal(t, "art", lock.Path)
			return &lock, nil
		},
	)

	lock, err := uc.Acquire(ctx, snow.ID(1), "", "art")
	require.NoError(t, err)
	require.Equal(t, "art", lock.Path)
}

func TestFileLock_Acquire_OwnDirectoryLockCoversRequest(t *testing.T) {
	uc, repo, branchRepo, perm := newTestFileLock(t)
	ctx := permissionCtx(7)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
	held := &domain.FileLock{ID: 9, Path: "art", HeldBy: 7}
	repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{held}, nil)

	lock, err := uc.Acquire(ctx, snow.ID(1), "", "art/hero.png")
	require.NoError(t, err)
	require.Equal(t, snow.ID(9), lock.ID)
}

func TestFileLock_Acquire_DefaultBranchMissing(t *testing.T) {
	uc, _, branchRepo, perm := newTestFileLock(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(nil, domain.NewErrorRecordNotFound())

	_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "", "a.png")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestFileLock_Acquire_BranchRepoError(t *testing.T) {
	uc, _, branchRepo, perm := newTestFileLock(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	wantErr := errors.New("db down")
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, wantErr)

	_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "feature", "a.png")
	require.ErrorIs(t, err, wantErr)
}

func TestFileLock_Acquire_InvalidPath(t *testing.T) {
	uc, _, _, perm := newTestFileLock(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "", "../../etc/passwd")
	requireUserError(t, err)
}

func TestFileLock_Acquire_CreateRaces(t *testing.T) {
	t.Run("identical path taken by another user", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errors.New("unique constraint"))
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).
			Return(&domain.FileLock{ID: 9, Path: "a.png", HeldBy: 8, HeldByName: "bob"}, nil)

		_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "", "a.png")
		require.True(t, domain.IsErrorConflict(err))
		require.Contains(t, err.Error(), "bob")
	})

	t.Run("identical path already held by the caller", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errors.New("unique constraint"))
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).
			Return(&domain.FileLock{ID: 9, Path: "a.png", HeldBy: 7}, nil)

		lock, err := uc.Acquire(permissionCtx(7), snow.ID(1), "", "a.png")
		require.NoError(t, err)
		require.Equal(t, snow.ID(9), lock.ID)
	})

	t.Run("create fails and lookup finds nothing", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		wantErr := errors.New("db down")
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, wantErr)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(nil, domain.NewErrorRecordNotFound())

		_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "", "a.png")
		require.ErrorIs(t, err, wantErr)
	})
}

func TestFileLock_Release_ErrorPaths(t *testing.T) {
	t.Run("needs a claim", func(t *testing.T) {
		uc, _, _, _ := newTestFileLock(t)
		err := uc.Release(context.Background(), snow.ID(1), "", "a.png")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("needs write access", func(t *testing.T) {
		uc, _, _, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)
		err := uc.Release(permissionCtx(7), snow.ID(1), "", "a.png")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("invalid path", func(t *testing.T) {
		uc, _, _, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		err := uc.Release(permissionCtx(7), snow.ID(1), "", "..")
		requireUserError(t, err)
	})

	t.Run("branch repo error", func(t *testing.T) {
		uc, _, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		wantErr := errors.New("db down")
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(nil, wantErr)
		require.ErrorIs(t, uc.Release(permissionCtx(7), snow.ID(1), "", "a.png"), wantErr)
	})

	t.Run("repository error", func(t *testing.T) {
		uc, repo, branchRepo, perm := newTestFileLock(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		branchRepo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(defaultBranchFixture(), nil)
		wantErr := errors.New("db down")
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(nil, wantErr)
		require.ErrorIs(t, uc.Release(permissionCtx(7), snow.ID(1), "", "a.png"), wantErr)
	})
}

func TestFileLock_ListRepositoryError(t *testing.T) {
	uc, repo, _, perm := newTestFileLock(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	wantErr := errors.New("db down")
	repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, wantErr)

	_, err := uc.List(permissionCtx(7), snow.ID(1))
	require.ErrorIs(t, err, wantErr)
}

func TestFileLock_EnsureLocksRepositoryError(t *testing.T) {
	uc, repo, _, _ := newTestFileLock(t)
	wantErr := errors.New("db down")
	repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, wantErr)

	err := uc.EnsureLocks(permissionCtx(7), snow.ID(1), defaultBranchFixture(), []string{"a.png"}, 7)
	require.ErrorIs(t, err, wantErr)
}

func TestFileLock_ConflictMessageVariants(t *testing.T) {
	uc, repo, branchRepo, perm := newTestFileLock(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	branch := devBranchFixture()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	branchID := devBranchFixture().ID
	held := &domain.FileLock{ID: 9, BranchID: &branchID, Branch: "feature", Path: "a.png", HeldBy: 8}
	repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{held}, nil)

	_, err := uc.Acquire(permissionCtx(7), snow.ID(1), "feature", "a.png")
	require.True(t, domain.IsErrorConflict(err))
	require.Contains(t, err.Error(), `branch "feature"`)
	require.Contains(t, err.Error(), "8")
}

func TestFileLock_EnsureMergeRequestLocks_ErrorPaths(t *testing.T) {
	t.Run("repository list error", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		wantErr := errors.New("db down")
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, wantErr)
		require.ErrorIs(t, uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), []string{"a.png"}, 7, 7), wantErr)
	})

	t.Run("create race with another holder", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errors.New("unique constraint"))
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).
			Return(&domain.FileLock{ID: 9, Path: "a.png", HeldBy: 8, HeldByName: "bob"}, nil)

		err := uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), []string{"a.png"}, 7, 7)
		require.True(t, domain.IsErrorConflict(err))
	})

	t.Run("create race with the same holder is fine", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errors.New("unique constraint"))
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).
			Return(&domain.FileLock{ID: 9, Path: "a.png", HeldBy: 7}, nil)

		require.NoError(t, uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), []string{"a.png"}, 7, 7))
	})

	t.Run("create fails and lookup finds nothing", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, nil)
		wantErr := errors.New("db down")
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, wantErr)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), "a.png", nil).Return(nil, domain.NewErrorRecordNotFound())

		require.ErrorIs(t, uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), []string{"a.png"}, 7, 7), wantErr)
	})

	t.Run("no paths is a no-op", func(t *testing.T) {
		uc, _, _, _ := newTestFileLock(t)
		require.NoError(t, uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(), nil, 7, 7))
	})
}

func TestFileLock_ReleaseLanded_ErrorAndScopePaths(t *testing.T) {
	t.Run("repository error", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		wantErr := errors.New("db down")
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return(nil, wantErr)
		require.ErrorIs(t, uc.ReleaseLanded(permissionCtx(7), snow.ID(1), defaultBranchFixture(), []string{"a.png"}, 7), wantErr)
	})

	t.Run("other scope and holders are untouched", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		branchID := devBranchFixture().ID
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{
			{ID: 1, BranchID: &branchID, Path: "a.png", HeldBy: 7},
			{ID: 2, Path: "b.png", HeldBy: 8},
		}, nil)
		require.NoError(t, uc.ReleaseLanded(permissionCtx(7), snow.ID(1), defaultBranchFixture(), []string{"a.png", "b.png"}, 7))
	})

	t.Run("no paths is a no-op", func(t *testing.T) {
		uc, _, _, _ := newTestFileLock(t)
		require.NoError(t, uc.ReleaseLanded(permissionCtx(7), snow.ID(1), defaultBranchFixture(), nil, 7))
	})
}

func TestFileLock_ReleaseDelegates_Errors(t *testing.T) {
	uc, repo, _, _ := newTestFileLock(t)
	wantErr := errors.New("db down")
	repo.EXPECT().DeleteByMergeRequest(gomock.Any(), snow.ID(1), snow.ID(5)).Return(wantErr)
	repo.EXPECT().DeleteByBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(wantErr)

	require.ErrorIs(t, uc.ReleaseForMergeRequest(permissionCtx(7), snow.ID(1), 5), wantErr)
	require.ErrorIs(t, uc.ReleaseBranch(permissionCtx(7), snow.ID(1), 3), wantErr)
}

func TestFileLock_EnsureMergeRequestLocks_AuthorAndMRToken(t *testing.T) {
	t.Run("admin merge accepts author-held and request-linked locks", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		mrID := snow.ID(5)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{
			{ID: 1, Path: "a.png", HeldBy: 7, MergeRequestID: &mrID},
			{ID: 2, Path: "assets", HeldBy: 7},
		}, nil)

		err := uc.EnsureMergeRequestLocks(permissionCtx(99), snow.ID(1), 5, defaultBranchFixture(),
			[]string{"a.png", "assets/orc.png"}, 99, 7)
		require.NoError(t, err)
	})

	t.Run("request-linked lock of this request is accepted regardless of holder", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		mrID := snow.ID(5)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{
			{ID: 1, Path: "a.png", HeldBy: 7, MergeRequestID: &mrID},
		}, nil)

		err := uc.EnsureMergeRequestLocks(permissionCtx(99), snow.ID(1), 5, defaultBranchFixture(),
			[]string{"a.png"}, 99, 7)
		require.NoError(t, err)
	})

	t.Run("third-party lock still conflicts for the author", func(t *testing.T) {
		uc, repo, _, _ := newTestFileLock(t)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1)).Return([]*domain.FileLock{
			{ID: 1, Path: "a.png", HeldBy: 8, HeldByName: "bob"},
		}, nil)

		err := uc.EnsureMergeRequestLocks(permissionCtx(7), snow.ID(1), 5, defaultBranchFixture(),
			[]string{"a.png"}, 7, 7)
		require.True(t, domain.IsErrorConflict(err))
	})
}
