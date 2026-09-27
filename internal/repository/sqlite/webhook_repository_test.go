package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func seedWebhook(t *testing.T, repo *WebhookRepository, projectID snow.ID, id int64, name string, events []string) *domain.Webhook {
	t.Helper()

	webhook, err := repo.Create(context.Background(), domain.Webhook{
		ID:          snow.ID(id),
		ProjectID:   projectID,
		Name:        name,
		URL:         "https://example.com/hooks/" + name,
		Secret:      "s3cret",
		Events:      events,
		IsActive:    true,
		InsecureTLS: false,
	})
	require.NoError(t, err)
	return webhook
}

func TestWebhookRepositorySQLite_CRUD(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)

	projectID := seedProject(t, q, 1, "game")
	otherProjectID := seedProject(t, q, 1, "other")

	created := seedWebhook(t, repo, projectID, 1001, "ci", []string{
		domain.WebhookEventPush,
		domain.WebhookEventMRCreated,
	})
	require.Equal(t, projectID, created.ProjectID)
	require.Equal(t, "s3cret", created.Secret)
	require.Equal(t, []string{domain.WebhookEventPush, domain.WebhookEventMRCreated}, created.Events)
	require.True(t, created.IsActive)
	require.False(t, created.InsecureTLS)
	require.False(t, created.CreatedAt.IsZero())

	got, err := repo.Get(ctx, projectID, snow.ID(1001))
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.Events, got.Events)

	_, err = repo.Get(ctx, otherProjectID, snow.ID(1001))
	requireRecordNotFound(t, err)

	_, err = repo.Get(ctx, projectID, snow.ID(9999))
	requireRecordNotFound(t, err)

	second := seedWebhook(t, repo, projectID, 1002, "chat", []string{domain.WebhookEventPush})
	second.InsecureTLS = true
	second.PathPrefix = "assets"
	second.IsActive = false
	updated, err := repo.Update(ctx, *second)
	require.NoError(t, err)
	require.True(t, updated.InsecureTLS)
	require.Equal(t, "assets", updated.PathPrefix)
	require.False(t, updated.IsActive)
	require.Equal(t, []string{domain.WebhookEventPush}, updated.Events)

	all, err := repo.ListByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, all, 2)

	active, err := repo.ListActiveByProject(ctx, projectID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, snow.ID(1001), active[0].ID)

	rotated, err := repo.RotateSecret(ctx, projectID, snow.ID(1001), "new-secret")
	require.NoError(t, err)
	require.Equal(t, "new-secret", rotated.Secret)

	require.NoError(t, repo.Delete(ctx, projectID, snow.ID(1002)))
	_, err = repo.Get(ctx, projectID, snow.ID(1002))
	requireRecordNotFound(t, err)
	requireRecordNotFound(t, repo.Delete(ctx, projectID, snow.ID(1002)))
}

func TestWebhookRepositorySQLite_ListEmpty(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)
	projectID := seedProject(t, q, 1, "game")

	list, err := repo.ListByProject(ctx, projectID)
	require.NoError(t, err)
	require.Empty(t, list)

	active, err := repo.ListActiveByProject(ctx, projectID)
	require.NoError(t, err)
	require.Empty(t, active)

	deliveries, err := repo.ListDeliveries(ctx, snow.ID(1), 10, 0)
	require.NoError(t, err)
	require.Empty(t, deliveries)
}

func TestWebhookRepositorySQLite_EventsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)
	projectID := seedProject(t, q, 1, "game")

	empty := seedWebhook(t, repo, projectID, 1001, "plain", nil)
	require.Empty(t, empty.Events)

	got, err := repo.Get(ctx, projectID, empty.ID)
	require.NoError(t, err)
	require.Empty(t, got.Events)
}

func TestWebhookRepositorySQLite_DeliveryLifecycle(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)
	projectID := seedProject(t, q, 1, "game")
	webhook := seedWebhook(t, repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	pending, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
		ID:          snow.ID(5001),
		WebhookID:   webhook.ID,
		EventType:   domain.WebhookEventPush,
		Payload:     []byte(`{"event":"push"}`),
		NextRetryAt: &now,
	})
	require.NoError(t, err)
	require.Equal(t, domain.WebhookDeliveryPending, pending.State)
	require.Equal(t, int64(0), pending.Attempt)
	require.Nil(t, pending.ResponseStatus)
	require.Nil(t, pending.DeliveredAt)
	require.NotNil(t, pending.NextRetryAt)

	got, err := repo.GetDelivery(ctx, webhook.ID, pending.ID)
	require.NoError(t, err)
	require.Equal(t, []byte(`{"event":"push"}`), got.Payload)

	_, err = repo.GetDelivery(ctx, webhook.ID, snow.ID(9999))
	requireRecordNotFound(t, err)

	status := int64(200)
	deliveredAt := now.Add(time.Second)
	delivered, err := repo.MarkDelivered(ctx, pending.ID, 1, &status, deliveredAt)
	require.NoError(t, err)
	require.Equal(t, domain.WebhookDeliveryDelivered, delivered.State)
	require.Equal(t, int64(1), delivered.Attempt)
	require.NotNil(t, delivered.ResponseStatus)
	require.Equal(t, status, *delivered.ResponseStatus)
	require.NotNil(t, delivered.DeliveredAt)
	require.WithinDuration(t, deliveredAt, *delivered.DeliveredAt, time.Second)
	require.Nil(t, delivered.NextRetryAt)

	failedStatus := int64(500)
	nextRetry := now.Add(time.Minute)
	rescheduled, err := repo.Reschedule(ctx, pending.ID, 2, &failedStatus, "boom", nextRetry)
	require.NoError(t, err)
	require.Equal(t, domain.WebhookDeliveryPending, rescheduled.State)
	require.Equal(t, int64(2), rescheduled.Attempt)
	require.Equal(t, "boom", rescheduled.LastError)
	require.NotNil(t, rescheduled.NextRetryAt)
	require.WithinDuration(t, nextRetry, *rescheduled.NextRetryAt, time.Second)

	failed, err := repo.MarkFailed(ctx, pending.ID, 5, &failedStatus, "still down")
	require.NoError(t, err)
	require.Equal(t, domain.WebhookDeliveryFailed, failed.State)
	require.Equal(t, int64(5), failed.Attempt)
	require.Equal(t, "still down", failed.LastError)
	require.Nil(t, failed.NextRetryAt)

	requeuedAt := now.Add(2 * time.Minute)
	requeued, err := repo.Requeue(ctx, webhook.ID, pending.ID, requeuedAt)
	require.NoError(t, err)
	require.Equal(t, domain.WebhookDeliveryPending, requeued.State)
	require.Empty(t, requeued.LastError)
	require.NotNil(t, requeued.NextRetryAt)
	require.WithinDuration(t, requeuedAt, *requeued.NextRetryAt, time.Second)

	_, err = repo.Requeue(ctx, webhook.ID, snow.ID(9999), requeuedAt)
	requireRecordNotFound(t, err)
}

func TestWebhookRepositorySQLite_ListDeliveries(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)
	projectID := seedProject(t, q, 1, "game")
	webhook := seedWebhook(t, repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	for i, id := range []int64{5001, 5002, 5003} {
		_, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
			ID:        snow.ID(id),
			WebhookID: webhook.ID,
			EventType: domain.WebhookEventPush,
			Payload:   []byte(`{}`),
			State:     domain.WebhookDeliveryDelivered,
			Attempt:   int64(i + 1),
		})
		require.NoError(t, err)
	}

	page, err := repo.ListDeliveries(ctx, webhook.ID, 2, 0)
	require.NoError(t, err)
	require.Len(t, page, 2)
	require.Equal(t, snow.ID(5003), page[0].ID)
	require.Equal(t, snow.ID(5002), page[1].ID)

	page, err = repo.ListDeliveries(ctx, webhook.ID, 2, 2)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, snow.ID(5001), page[0].ID)
}

func TestWebhookRepositorySQLite_ListPendingRetries(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)
	projectID := seedProject(t, q, 1, "game")
	webhook := seedWebhook(t, repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	later := now.Add(time.Hour)

	create := func(id int64, state string, retryAt *time.Time) {
		t.Helper()
		_, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
			ID:          snow.ID(id),
			WebhookID:   webhook.ID,
			EventType:   domain.WebhookEventPush,
			Payload:     []byte(`{}`),
			State:       state,
			NextRetryAt: retryAt,
		})
		require.NoError(t, err)
	}
	create(5001, domain.WebhookDeliveryPending, &due)
	create(5002, domain.WebhookDeliveryPending, &later)
	create(5003, domain.WebhookDeliveryDelivered, nil)
	create(5004, domain.WebhookDeliveryPending, nil)

	retries, err := repo.ListPendingRetries(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, retries, 1)
	require.Equal(t, snow.ID(5001), retries[0].ID)

	retries, err = repo.ListPendingRetries(ctx, now, 0)
	require.NoError(t, err)
	require.Empty(t, retries)
}

func TestWebhookRepositorySQLite_DeleteOldDeliveries(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)
	projectID := seedProject(t, q, 1, "game")
	webhook := seedWebhook(t, repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	for _, row := range []struct {
		id    int64
		state string
	}{
		{5001, domain.WebhookDeliveryDelivered},
		{5002, domain.WebhookDeliveryPending},
		{5003, domain.WebhookDeliveryDelivered},
		{5004, domain.WebhookDeliveryDelivered},
	} {
		_, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
			ID:        snow.ID(row.id),
			WebhookID: webhook.ID,
			EventType: domain.WebhookEventPush,
			Payload:   []byte(`{}`),
			State:     row.state,
		})
		require.NoError(t, err)
	}

	deleted, err := repo.DeleteOldDeliveries(ctx, webhook.ID, 2)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted, "the oldest delivered row is trimmed, the pending one is kept")

	remaining, err := repo.ListDeliveries(ctx, webhook.ID, 10, 0)
	require.NoError(t, err)
	require.Len(t, remaining, 3)
	require.Equal(t, snow.ID(5002), remaining[2].ID)
}

func TestWebhookRepositorySQLite_DeleteRemovesDeliveries(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewWebhookRepository(db)
	projectID := seedProject(t, q, 1, "game")
	webhook := seedWebhook(t, repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	_, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
		ID:        snow.ID(5001),
		WebhookID: webhook.ID,
		EventType: domain.WebhookEventPush,
		Payload:   []byte(`{}`),
	})
	require.NoError(t, err)

	require.NoError(t, repo.Delete(ctx, projectID, webhook.ID))
	_, err = repo.GetDelivery(ctx, webhook.ID, snow.ID(5001))
	requireRecordNotFound(t, err)
}
