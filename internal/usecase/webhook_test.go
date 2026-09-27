package usecase

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func newTestWebhook(t *testing.T) (*Webhook, *MockwebhookRepository, *MockpermissionUsecase) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockwebhookRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return NewWebhook(repo, perm, node), repo, perm
}

func webhookFixture() *domain.Webhook {
	return &domain.Webhook{
		ID:        snow.ID(1001),
		ProjectID: snow.ID(1),
		Name:      "ci",
		URL:       "https://example.com/hook",
		Secret:    "old-secret",
		Events:    []string{domain.WebhookEventPush},
		IsActive:  true,
	}
}

func TestWebhook_Create(t *testing.T) {
	ctx := permissionCtx(7)

	t.Run("requires project admin", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

		_, err := uc.Create(ctx, snow.ID(1), WebhookInput{URL: "https://example.com/hook", Events: []string{domain.WebhookEventPush}})
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("rejects invalid urls", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true).AnyTimes()

		for _, raw := range []string{
			"example.com/hook",
			"ftp://example.com/hook",
			"https://example.com/hook#frag",
			"https://user:pass@example.com/hook",
			"",
		} {
			_, err := uc.Create(ctx, snow.ID(1), WebhookInput{URL: raw, Events: []string{domain.WebhookEventPush}})
			requireUserError(t, err)
		}
	})

	t.Run("rejects bad events", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true).AnyTimes()

		_, err := uc.Create(ctx, snow.ID(1), WebhookInput{URL: "https://example.com/hook"})
		requireUserError(t, err)

		_, err = uc.Create(ctx, snow.ID(1), WebhookInput{URL: "https://example.com/hook", Events: []string{"nope"}})
		requireUserError(t, err)
	})

	t.Run("rejects an overlong url and name", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true).AnyTimes()

		_, err := uc.Create(ctx, snow.ID(1), WebhookInput{
			URL: "https://example.com/" + strings.Repeat("a", webhookMaxURLLen), Events: []string{domain.WebhookEventPush},
		})
		requireUserError(t, err)

		_, err = uc.Create(ctx, snow.ID(1), WebhookInput{
			Name: strings.Repeat("a", webhookMaxNameLen+1), URL: "https://example.com/hook", Events: []string{domain.WebhookEventPush},
		})
		requireUserError(t, err)
	})

	t.Run("rejects invalid path prefix", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)

		_, err := uc.Create(ctx, snow.ID(1), WebhookInput{
			URL: "https://example.com/hook", Events: []string{domain.WebhookEventPush}, PathPrefix: "../etc",
		})
		requireUserError(t, err)
	})

	t.Run("normalizes input and generates a secret", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, webhook domain.Webhook) (*domain.Webhook, error) {
				require.NotZero(t, webhook.ID)
				require.Equal(t, snow.ID(1), webhook.ProjectID)
				require.Equal(t, "ci", webhook.Name)
				require.Equal(t, "https://example.com/hook", webhook.URL)
				require.Equal(t, []string{domain.WebhookEventMRCreated, domain.WebhookEventPush}, webhook.Events)
				require.Equal(t, "assets", webhook.PathPrefix)
				require.True(t, webhook.IsActive)
				require.False(t, webhook.InsecureTLS)
				require.Len(t, webhook.Secret, 64)
				_, err := hex.DecodeString(webhook.Secret)
				require.NoError(t, err)
				created := webhook
				return &created, nil
			})

		created, err := uc.Create(ctx, snow.ID(1), WebhookInput{
			Name:       "  ci  ",
			URL:        " https://example.com/hook ",
			Events:     []string{domain.WebhookEventPush, domain.WebhookEventPush, domain.WebhookEventMRCreated},
			PathPrefix: "assets/",
			IsActive:   true,
		})
		require.NoError(t, err)
		require.Equal(t, "ci", created.Name)
		require.Equal(t, "assets", created.PathPrefix)
		require.Len(t, created.Secret, 64)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorDatabase("boom"))

		_, err := uc.Create(ctx, snow.ID(1), WebhookInput{URL: "https://example.com/hook", Events: []string{domain.WebhookEventPush}})
		require.Error(t, err)
	})
}

func TestWebhook_ListAndGet(t *testing.T) {
	ctx := permissionCtx(7)

	t.Run("requires project admin", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false).AnyTimes()

		_, err := uc.List(ctx, snow.ID(1))
		require.True(t, domain.IsErrorNoPermission(err))

		_, err = uc.Get(ctx, snow.ID(1), snow.ID(1001))
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("returns an empty list when nothing is configured", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return(nil, nil)

		webhooks, err := uc.List(ctx, snow.ID(1))
		require.NoError(t, err)
		require.Empty(t, webhooks)
		require.NotNil(t, webhooks)
	})

	t.Run("returns configured webhooks", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{webhookFixture()}, nil)

		webhooks, err := uc.List(ctx, snow.ID(1))
		require.NoError(t, err)
		require.Len(t, webhooks, 1)
		require.Equal(t, snow.ID(1001), webhooks[0].ID)
	})

	t.Run("propagates repository errors", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().ListByProject(gomock.Any(), snow.ID(1)).Return(nil, domain.NewErrorDatabase("boom"))

		_, err := uc.List(ctx, snow.ID(1))
		require.Error(t, err)
	})

	t.Run("propagates not found", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), snow.ID(9999)).Return(nil, domain.NewErrorRecordNotFound())

		_, err := uc.Get(ctx, snow.ID(1), snow.ID(9999))
		require.True(t, domain.IsErrorNotFound(err))
	})
}

func TestWebhook_Update(t *testing.T) {
	ctx := permissionCtx(7)

	t.Run("requires project admin", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

		_, err := uc.Update(ctx, snow.ID(1), snow.ID(1001), WebhookUpdate{})
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("applies the provided fields only", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), snow.ID(1001)).Return(webhookFixture(), nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, webhook domain.Webhook) (*domain.Webhook, error) {
				require.Equal(t, "renamed", webhook.Name)
				require.Equal(t, "https://example.com/hook", webhook.URL)
				require.Equal(t, []string{domain.WebhookEventPush}, webhook.Events)
				require.False(t, webhook.IsActive)
				require.True(t, webhook.InsecureTLS)
				require.Equal(t, "old-secret", webhook.Secret)
				updated := webhook
				return &updated, nil
			})

		name := "renamed"
		inactive := false
		insecure := true
		updated, err := uc.Update(ctx, snow.ID(1), snow.ID(1001), WebhookUpdate{
			Name:        &name,
			IsActive:    &inactive,
			InsecureTLS: &insecure,
		})
		require.NoError(t, err)
		require.Equal(t, "renamed", updated.Name)
		require.False(t, updated.IsActive)
	})

	t.Run("normalizes a new url and events", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), snow.ID(1001)).Return(webhookFixture(), nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, webhook domain.Webhook) (*domain.Webhook, error) {
				require.Equal(t, "https://example.com/new", webhook.URL)
				require.Equal(t, []string{domain.WebhookEventMRMerged}, webhook.Events)
				require.Equal(t, "src", webhook.PathPrefix)
				updated := webhook
				return &updated, nil
			})

		newURL := " https://example.com/new "
		events := []string{domain.WebhookEventMRMerged}
		prefix := "src/"
		_, err := uc.Update(ctx, snow.ID(1), snow.ID(1001), WebhookUpdate{URL: &newURL, Events: &events, PathPrefix: &prefix})
		require.NoError(t, err)
	})

	t.Run("rejects invalid patches before writing", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true).AnyTimes()
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), snow.ID(1001)).Return(webhookFixture(), nil).AnyTimes()

		badURL := "not-a-url"
		_, err := uc.Update(ctx, snow.ID(1), snow.ID(1001), WebhookUpdate{URL: &badURL})
		requireUserError(t, err)

		badEvents := []string{"nope"}
		_, err = uc.Update(ctx, snow.ID(1), snow.ID(1001), WebhookUpdate{Events: &badEvents})
		requireUserError(t, err)

		badPrefix := "../etc"
		_, err = uc.Update(ctx, snow.ID(1), snow.ID(1001), WebhookUpdate{PathPrefix: &badPrefix})
		requireUserError(t, err)
	})

	t.Run("propagates not found", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), snow.ID(1001)).Return(nil, domain.NewErrorRecordNotFound())

		_, err := uc.Update(ctx, snow.ID(1), snow.ID(1001), WebhookUpdate{})
		require.True(t, domain.IsErrorNotFound(err))
	})
}

func TestWebhook_Delete(t *testing.T) {
	ctx := permissionCtx(7)

	uc, repo, perm := newTestWebhook(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)
	require.True(t, domain.IsErrorNoPermission(uc.Delete(ctx, snow.ID(1), snow.ID(1001))))

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().Delete(gomock.Any(), snow.ID(1), snow.ID(1001)).Return(nil)
	require.NoError(t, uc.Delete(ctx, snow.ID(1), snow.ID(1001)))
}

func TestWebhook_RotateSecret(t *testing.T) {
	ctx := permissionCtx(7)

	t.Run("requires project admin", func(t *testing.T) {
		uc, _, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

		_, err := uc.RotateSecret(ctx, snow.ID(1), snow.ID(1001))
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("stores a fresh secret", func(t *testing.T) {
		uc, repo, perm := newTestWebhook(t)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().RotateSecret(gomock.Any(), snow.ID(1), snow.ID(1001), gomock.Any()).DoAndReturn(
			func(_ context.Context, _, _ snow.ID, secret string) (*domain.Webhook, error) {
				require.NotEqual(t, "old-secret", secret)
				require.Len(t, secret, 64)
				_, err := hex.DecodeString(secret)
				require.NoError(t, err)
				rotated := webhookFixture()
				rotated.Secret = secret
				return rotated, nil
			})

		rotated, err := uc.RotateSecret(ctx, snow.ID(1), snow.ID(1001))
		require.NoError(t, err)
		require.Len(t, rotated.Secret, 64)
	})
}

func TestGenerateWebhookSecret(t *testing.T) {
	first, err := generateWebhookSecret()
	require.NoError(t, err)
	second, err := generateWebhookSecret()
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.Len(t, first, 64)
}
