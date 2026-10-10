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

func newTestEmailDelivery(t *testing.T) (*EmailDelivery, *MockemailDeliveryRepository, *MockpermissionUsecase, *fakeKicker) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockemailDeliveryRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	kicker := &fakeKicker{}
	uc := NewEmailDelivery(repo, perm).WithKicker(kicker)
	uc.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	return uc, repo, perm, kicker
}

func testEmailDeliveryRow(id, projectID snow.ID, state string) *domain.EmailDelivery {
	return &domain.EmailDelivery{
		ID:        id,
		Event:     "mr.comment_created",
		ProjectID: projectID,
		UserID:    10,
		Email:     "dev@example.com",
		Subject:   "subject",
		State:     state,
	}
}

func TestEmailDelivery_List(t *testing.T) {
	uc, repo, perm, _ := newTestEmailDelivery(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().ListDeliveries(gomock.Any(), snow.ID(1), "failed", (*snow.ID)(nil), int64(defaultEmailDeliveryPageSize+1)).
		Return([]*domain.EmailDelivery{
			testEmailDeliveryRow(30, 1, domain.EmailDeliveryFailed),
			testEmailDeliveryRow(20, 1, domain.EmailDeliveryFailed),
		}, nil)

	deliveries, next, err := uc.List(context.Background(), snow.ID(1), EmailDeliveryListOptions{State: " failed "})
	require.NoError(t, err)
	require.Len(t, deliveries, 2)
	require.Empty(t, next)
	require.Equal(t, snow.ID(30), deliveries[0].ID)
}

func TestEmailDelivery_ListPaginates(t *testing.T) {
	uc, repo, perm, _ := newTestEmailDelivery(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	after := snow.ID(40)
	repo.EXPECT().ListDeliveries(gomock.Any(), snow.ID(1), "", &after, int64(3)).
		Return([]*domain.EmailDelivery{
			testEmailDeliveryRow(30, 1, domain.EmailDeliveryDelivered),
			testEmailDeliveryRow(20, 1, domain.EmailDeliveryDelivered),
			testEmailDeliveryRow(10, 1, domain.EmailDeliveryDelivered),
		}, nil)

	deliveries, next, err := uc.List(context.Background(), snow.ID(1), EmailDeliveryListOptions{After: &after, Limit: 2})
	require.NoError(t, err)
	require.Len(t, deliveries, 2)
	require.Equal(t, snow.ID(20).Base36(), next)
}

func TestEmailDelivery_ListValidation(t *testing.T) {
	uc, _, perm, _ := newTestEmailDelivery(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	_, _, err := uc.List(context.Background(), snow.ID(1), EmailDeliveryListOptions{State: "bogus"})
	requireUserError(t, err)
}

func TestEmailDelivery_ListRequiresAdmin(t *testing.T) {
	uc, _, perm, _ := newTestEmailDelivery(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)
	_, _, err := uc.List(context.Background(), snow.ID(1), EmailDeliveryListOptions{})
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestEmailDelivery_Redeliver(t *testing.T) {
	uc, repo, perm, kicker := newTestEmailDelivery(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(9)).Return(testEmailDeliveryRow(9, 1, domain.EmailDeliveryFailed), nil)
	repo.EXPECT().Redeliver(gomock.Any(), snow.ID(1), snow.ID(9), time.Unix(1000, 0).UTC()).Return(true, nil)
	repo.EXPECT().Get(gomock.Any(), snow.ID(9)).Return(testEmailDeliveryRow(9, 1, domain.EmailDeliveryPending), nil)

	delivery, err := uc.Redeliver(context.Background(), snow.ID(1), snow.ID(9))
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryPending, delivery.State)
	require.Equal(t, 1, kicker.kicks)
}

func TestEmailDelivery_RedeliverRejects(t *testing.T) {
	t.Run("not an admin", func(t *testing.T) {
		uc, _, perm, _ := newTestEmailDelivery(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)
		_, err := uc.Redeliver(context.Background(), snow.ID(1), snow.ID(9))
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("another project", func(t *testing.T) {
		uc, repo, perm, _ := newTestEmailDelivery(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(9)).Return(testEmailDeliveryRow(9, 2, domain.EmailDeliveryFailed), nil)
		_, err := uc.Redeliver(context.Background(), snow.ID(1), snow.ID(9))
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("still pending", func(t *testing.T) {
		uc, repo, perm, _ := newTestEmailDelivery(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(9)).Return(testEmailDeliveryRow(9, 1, domain.EmailDeliveryPending), nil)
		_, err := uc.Redeliver(context.Background(), snow.ID(1), snow.ID(9))
		require.True(t, domain.IsErrorConflict(err))
	})

	t.Run("repo failure", func(t *testing.T) {
		uc, repo, perm, _ := newTestEmailDelivery(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(9)).Return(testEmailDeliveryRow(9, 1, domain.EmailDeliveryDelivered), nil)
		repo.EXPECT().Redeliver(gomock.Any(), snow.ID(1), snow.ID(9), gomock.Any()).Return(false, errors.New("db down"))
		_, err := uc.Redeliver(context.Background(), snow.ID(1), snow.ID(9))
		require.ErrorContains(t, err, "db down")
	})

	t.Run("state moved before the update", func(t *testing.T) {
		uc, repo, perm, kicker := newTestEmailDelivery(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(9)).Return(testEmailDeliveryRow(9, 1, domain.EmailDeliveryFailed), nil)
		repo.EXPECT().Redeliver(gomock.Any(), snow.ID(1), snow.ID(9), gomock.Any()).Return(false, nil)
		_, err := uc.Redeliver(context.Background(), snow.ID(1), snow.ID(9))
		require.True(t, domain.IsErrorConflict(err), "a raced redelivery conflicts")
		require.Zero(t, kicker.kicks)
	})
}
