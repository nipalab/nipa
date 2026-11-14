package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubLockHooks struct {
	calls   int
	request *domain.FileLockRequest
	err     error
}

func (s *stubLockHooks) EmitLockReleaseRequested(_ context.Context, _ snow.ID, _ *domain.FileLock, request *domain.FileLockRequest, _ snow.ID) error {
	s.calls++
	s.request = request
	return s.err
}

func requestFixture(t *testing.T) (*FileLock, *MockfileLockRepository, *MockpermissionUsecase, *stubLockHooks, time.Time) {
	t.Helper()
	uc, repo, _, perm := newTestFileLock(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	hooks := &stubLockHooks{}
	uc.WithClock(func() time.Time { return now }).WithHooks(hooks)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	perm.EXPECT().HasPathAccess(gomock.Any(), snow.ID(1), gomock.Any(), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().ListProject(gomock.Any(), snow.ID(1), now).Return([]*domain.FileLock{{ID: 9, Path: "art/a.psd", HeldBy: 3}, {ID: 10, Path: "mine.psd", HeldBy: 7}}, nil).AnyTimes()
	return uc, repo, perm, hooks, now
}

func TestFileLock_RequestRelease(t *testing.T) {
	t.Run("creates and notifies", func(t *testing.T) {
		uc, repo, _, hooks, now := requestFixture(t)
		repo.EXPECT().GetRequest(gomock.Any(), snow.ID(9), snow.ID(7)).Return(nil, domain.NewErrorRecordNotFound())
		repo.EXPECT().CreateRequest(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, r domain.FileLockRequest) (*domain.FileLockRequest, error) {
			require.Equal(t, snow.ID(9), r.LockID)
			require.Equal(t, snow.ID(7), r.RequesterID)
			require.Equal(t, "please", r.Message)
			require.Equal(t, now, r.CreatedAt)
			return &r, nil
		})
		request, err := uc.RequestRelease(permissionCtx(7), 1, 9, "  please ")
		require.NoError(t, err)
		require.Equal(t, "please", request.Message)
		require.Equal(t, 1, hooks.calls)
	})

	t.Run("repeat within the cooldown only updates the message", func(t *testing.T) {
		uc, repo, _, hooks, now := requestFixture(t)
		earlier := now.Add(-time.Hour)
		repo.EXPECT().GetRequest(gomock.Any(), snow.ID(9), snow.ID(7)).Return(&domain.FileLockRequest{ID: 4, CreatedAt: earlier}, nil)
		repo.EXPECT().UpdateRequest(gomock.Any(), snow.ID(4), "again", earlier).Return(nil)
		request, err := uc.RequestRelease(permissionCtx(7), 1, 9, "again")
		require.NoError(t, err)
		require.Equal(t, earlier, request.CreatedAt)
		require.Zero(t, hooks.calls)
	})

	t.Run("repeat after the cooldown notifies again", func(t *testing.T) {
		uc, repo, _, hooks, now := requestFixture(t)
		repo.EXPECT().GetRequest(gomock.Any(), snow.ID(9), snow.ID(7)).Return(&domain.FileLockRequest{ID: 4, CreatedAt: now.Add(-LockRequestCooldown)}, nil)
		repo.EXPECT().UpdateRequest(gomock.Any(), snow.ID(4), "", now).Return(nil)
		request, err := uc.RequestRelease(permissionCtx(7), 1, 9, "")
		require.NoError(t, err)
		require.Equal(t, now, request.CreatedAt)
		require.Equal(t, 1, hooks.calls)
	})

	t.Run("a failing hook does not fail the request and frees the cooldown", func(t *testing.T) {
		uc, repo, _, hooks, _ := requestFixture(t)
		hooks.err = errors.New("down")
		repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound())
		repo.EXPECT().CreateRequest(gomock.Any(), gomock.Any()).Return(&domain.FileLockRequest{ID: 4, Message: "hi"}, nil)
		repo.EXPECT().UpdateRequest(gomock.Any(), snow.ID(4), "hi", time.Unix(0, 0).UTC()).Return(nil)
		_, err := uc.RequestRelease(permissionCtx(7), 1, 9, "")
		require.NoError(t, err)
	})

	t.Run("a failing hook and a failing reset still succeed", func(t *testing.T) {
		uc, repo, _, hooks, _ := requestFixture(t)
		hooks.err = errors.New("down")
		repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound())
		repo.EXPECT().CreateRequest(gomock.Any(), gomock.Any()).Return(&domain.FileLockRequest{ID: 4}, nil)
		repo.EXPECT().UpdateRequest(gomock.Any(), snow.ID(4), gomock.Any(), gomock.Any()).Return(errors.New("db down"))
		_, err := uc.RequestRelease(permissionCtx(7), 1, 9, "")
		require.NoError(t, err)
	})

	t.Run("losing the insert race updates the winner's row", func(t *testing.T) {
		uc, repo, _, hooks, now := requestFixture(t)
		earlier := now.Add(-time.Minute)
		gomock.InOrder(
			repo.EXPECT().GetRequest(gomock.Any(), snow.ID(9), snow.ID(7)).Return(nil, domain.NewErrorRecordNotFound()),
			repo.EXPECT().CreateRequest(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorConflict("record already exists")),
			repo.EXPECT().GetRequest(gomock.Any(), snow.ID(9), snow.ID(7)).Return(&domain.FileLockRequest{ID: 4, CreatedAt: earlier}, nil),
			repo.EXPECT().UpdateRequest(gomock.Any(), snow.ID(4), "second", earlier).Return(nil),
		)
		request, err := uc.RequestRelease(permissionCtx(7), 1, 9, "second")
		require.NoError(t, err)
		require.Equal(t, "second", request.Message)
		require.Zero(t, hooks.calls, "the winner already notified the holder")
	})

	t.Run("works without hooks", func(t *testing.T) {
		uc, repo, _, _, _ := requestFixture(t)
		uc.hooks = nil
		repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound())
		repo.EXPECT().CreateRequest(gomock.Any(), gomock.Any()).Return(&domain.FileLockRequest{}, nil)
		_, err := uc.RequestRelease(permissionCtx(7), 1, 9, "")
		require.NoError(t, err)
	})

	t.Run("rejects your own lock, unknown locks and long messages", func(t *testing.T) {
		uc, _, _, _, _ := requestFixture(t)
		_, err := uc.RequestRelease(permissionCtx(7), 1, 10, "")
		require.EqualError(t, err, "you hold this lock")
		_, err = uc.RequestRelease(permissionCtx(7), 1, 99, "")
		require.True(t, domain.IsErrorNotFound(err))
		_, err = uc.RequestRelease(permissionCtx(7), 1, 9, strings.Repeat("é", MaxLockRequestMessage+1))
		require.EqualError(t, err, "message is too long")
		_, err = uc.RequestRelease(context.Background(), 1, 9, "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("hides locks on unreadable paths", func(t *testing.T) {
		uc, repo, perm := newTestFileLock3(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().HasPathAccess(gomock.Any(), snow.ID(1), "secret.psd", domain.PermissionRead).Return(false)
		repo.EXPECT().ListProject(gomock.Any(), snow.ID(1), gomock.Any()).Return([]*domain.FileLock{{ID: 9, Path: "secret.psd", HeldBy: 3}}, nil)
		_, err := uc.RequestRelease(permissionCtx(7), 1, 9, "")
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("requires read access", func(t *testing.T) {
		uc, _, perm := newTestFileLock3(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := uc.RequestRelease(permissionCtx(7), 1, 9, "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("repository errors", func(t *testing.T) {
		uc, repo, perm := newTestFileLock3(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
		perm.EXPECT().HasPathAccess(gomock.Any(), gomock.Any(), gomock.Any(), domain.PermissionRead).Return(true).AnyTimes()
		boom := errors.New("boom")
		gomock.InOrder(
			repo.EXPECT().ListProject(gomock.Any(), snow.ID(1), gomock.Any()).Return(nil, boom),
			repo.EXPECT().ListProject(gomock.Any(), snow.ID(1), gomock.Any()).Return([]*domain.FileLock{{ID: 9, Path: "a", HeldBy: 3}}, nil),
			repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, boom),
			repo.EXPECT().ListProject(gomock.Any(), snow.ID(1), gomock.Any()).Return([]*domain.FileLock{{ID: 9, Path: "a", HeldBy: 3}}, nil),
			repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound()),
			repo.EXPECT().CreateRequest(gomock.Any(), gomock.Any()).Return(nil, boom),
			repo.EXPECT().ListProject(gomock.Any(), snow.ID(1), gomock.Any()).Return([]*domain.FileLock{{ID: 9, Path: "a", HeldBy: 3}}, nil),
			repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(&domain.FileLockRequest{ID: 4}, nil),
			repo.EXPECT().UpdateRequest(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(boom),
			repo.EXPECT().ListProject(gomock.Any(), snow.ID(1), gomock.Any()).Return([]*domain.FileLock{{ID: 9, Path: "a", HeldBy: 3}}, nil),
			repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound()),
			repo.EXPECT().CreateRequest(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorConflict("record already exists")),
			repo.EXPECT().GetRequest(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, boom),
		)
		for i := 0; i < 5; i++ {
			_, err := uc.RequestRelease(permissionCtx(7), 1, 9, "")
			require.ErrorIs(t, err, boom)
		}
	})
}

func newTestFileLock3(t *testing.T) (*FileLock, *MockfileLockRepository, *MockpermissionUsecase) {
	t.Helper()
	uc, repo, _, perm := newTestFileLock(t)
	return uc, repo, perm
}
