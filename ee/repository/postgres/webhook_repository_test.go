package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type WebhookRepositorySuite struct {
	baseSuite
}

func TestWebhookRepositorySuite(t *testing.T) {
	suite.Run(t, new(WebhookRepositorySuite))
}

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

func (s *WebhookRepositorySuite) TestCRUD() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)

	projectID := seedProject(s.T(), s.q, 1, "game")
	otherProjectID := seedProject(s.T(), s.q, 1, "other")

	created := seedWebhook(s.T(), repo, projectID, 1001, "ci", []string{
		domain.WebhookEventPush,
		domain.WebhookEventMRCreated,
	})
	s.Equal(projectID, created.ProjectID)
	s.Equal("s3cret", created.Secret)
	s.Equal([]string{domain.WebhookEventPush, domain.WebhookEventMRCreated}, created.Events)
	s.True(created.IsActive)
	s.False(created.InsecureTLS)
	s.False(created.CreatedAt.IsZero())

	got, err := repo.Get(ctx, projectID, snow.ID(1001))
	s.Require().NoError(err)
	s.Equal(created.ID, got.ID)
	s.Equal(created.Events, got.Events)

	_, err = repo.Get(ctx, otherProjectID, snow.ID(1001))
	requireRecordNotFound(s.T(), err)

	_, err = repo.Get(ctx, projectID, snow.ID(9999))
	requireRecordNotFound(s.T(), err)

	second := seedWebhook(s.T(), repo, projectID, 1002, "chat", []string{domain.WebhookEventPush})
	second.InsecureTLS = true
	second.PathPrefix = "assets"
	second.IsActive = false
	updated, err := repo.Update(ctx, *second)
	s.Require().NoError(err)
	s.True(updated.InsecureTLS)
	s.Equal("assets", updated.PathPrefix)
	s.False(updated.IsActive)
	s.Equal([]string{domain.WebhookEventPush}, updated.Events)

	all, err := repo.ListByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Len(all, 2)

	active, err := repo.ListActiveByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Len(active, 1)
	s.Equal(snow.ID(1001), active[0].ID)

	rotated, err := repo.RotateSecret(ctx, projectID, snow.ID(1001), "new-secret")
	s.Require().NoError(err)
	s.Equal("new-secret", rotated.Secret)

	s.Require().NoError(repo.Delete(ctx, projectID, snow.ID(1002)))
	_, err = repo.Get(ctx, projectID, snow.ID(1002))
	requireRecordNotFound(s.T(), err)
	requireRecordNotFound(s.T(), repo.Delete(ctx, projectID, snow.ID(1002)))
}

func (s *WebhookRepositorySuite) TestGetByID() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")

	created := seedWebhook(s.T(), repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	got, err := repo.GetByID(ctx, created.ID)
	s.Require().NoError(err)
	s.Equal(created.ID, got.ID)
	s.Equal(created.ProjectID, got.ProjectID)

	_, err = repo.GetByID(ctx, snow.ID(9999))
	requireRecordNotFound(s.T(), err)
}

func (s *WebhookRepositorySuite) TestListEmpty() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")

	list, err := repo.ListByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Empty(list)

	active, err := repo.ListActiveByProject(ctx, projectID)
	s.Require().NoError(err)
	s.Empty(active)

	deliveries, err := repo.ListDeliveries(ctx, snow.ID(1), 10, 0)
	s.Require().NoError(err)
	s.Empty(deliveries)
}

func (s *WebhookRepositorySuite) TestEventsRoundTrip() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")

	empty := seedWebhook(s.T(), repo, projectID, 1001, "plain", nil)
	s.Empty(empty.Events)

	got, err := repo.Get(ctx, projectID, empty.ID)
	s.Require().NoError(err)
	s.Empty(got.Events)
}

func (s *WebhookRepositorySuite) TestDeliveryLifecycle() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")
	webhook := seedWebhook(s.T(), repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	pending, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
		ID:          snow.ID(5001),
		WebhookID:   webhook.ID,
		EventType:   domain.WebhookEventPush,
		Payload:     []byte(`{"event":"push"}`),
		NextRetryAt: &now,
	})
	s.Require().NoError(err)
	s.Equal(domain.WebhookDeliveryPending, pending.State)
	s.Equal(int64(0), pending.Attempt)
	s.Nil(pending.ResponseStatus)
	s.Nil(pending.DeliveredAt)
	s.NotNil(pending.NextRetryAt)

	got, err := repo.GetDelivery(ctx, webhook.ID, pending.ID)
	s.Require().NoError(err)
	s.Equal([]byte(`{"event":"push"}`), got.Payload)

	_, err = repo.GetDelivery(ctx, webhook.ID, snow.ID(9999))
	requireRecordNotFound(s.T(), err)

	status := int64(200)
	deliveredAt := now.Add(time.Second)
	delivered, err := repo.MarkDelivered(ctx, pending.ID, 1, &status, deliveredAt)
	s.Require().NoError(err)
	s.Equal(domain.WebhookDeliveryDelivered, delivered.State)
	s.Equal(int64(1), delivered.Attempt)
	s.Require().NotNil(delivered.ResponseStatus)
	s.Equal(status, *delivered.ResponseStatus)
	s.Require().NotNil(delivered.DeliveredAt)
	s.WithinDuration(deliveredAt, *delivered.DeliveredAt, time.Second)
	s.Nil(delivered.NextRetryAt)

	failedStatus := int64(500)
	nextRetry := now.Add(time.Minute)
	rescheduled, err := repo.Reschedule(ctx, pending.ID, 2, &failedStatus, "boom", nextRetry)
	s.Require().NoError(err)
	s.Equal(domain.WebhookDeliveryPending, rescheduled.State)
	s.Equal(int64(2), rescheduled.Attempt)
	s.Equal("boom", rescheduled.LastError)
	s.Require().NotNil(rescheduled.NextRetryAt)
	s.WithinDuration(nextRetry, *rescheduled.NextRetryAt, time.Second)

	failed, err := repo.MarkFailed(ctx, pending.ID, 5, &failedStatus, "still down")
	s.Require().NoError(err)
	s.Equal(domain.WebhookDeliveryFailed, failed.State)
	s.Equal(int64(5), failed.Attempt)
	s.Equal("still down", failed.LastError)
	s.Nil(failed.NextRetryAt)

	requeuedAt := now.Add(2 * time.Minute)
	requeued, err := repo.Requeue(ctx, webhook.ID, pending.ID, requeuedAt)
	s.Require().NoError(err)
	s.Equal(domain.WebhookDeliveryPending, requeued.State)
	s.Empty(requeued.LastError)
	s.Require().NotNil(requeued.NextRetryAt)
	s.WithinDuration(requeuedAt, *requeued.NextRetryAt, time.Second)

	_, err = repo.Requeue(ctx, webhook.ID, snow.ID(9999), requeuedAt)
	requireRecordNotFound(s.T(), err)
}

func (s *WebhookRepositorySuite) TestListDeliveries() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")
	webhook := seedWebhook(s.T(), repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	for i, id := range []int64{5001, 5002, 5003} {
		_, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
			ID:        snow.ID(id),
			WebhookID: webhook.ID,
			EventType: domain.WebhookEventPush,
			Payload:   []byte(`{}`),
			State:     domain.WebhookDeliveryDelivered,
			Attempt:   int64(i + 1),
		})
		s.Require().NoError(err)
	}

	page, err := repo.ListDeliveries(ctx, webhook.ID, 2, 0)
	s.Require().NoError(err)
	s.Len(page, 2)
	s.Equal(snow.ID(5003), page[0].ID)
	s.Equal(snow.ID(5002), page[1].ID)

	page, err = repo.ListDeliveries(ctx, webhook.ID, 2, 2)
	s.Require().NoError(err)
	s.Len(page, 1)
	s.Equal(snow.ID(5001), page[0].ID)
}

func (s *WebhookRepositorySuite) TestListPendingRetries() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")
	webhook := seedWebhook(s.T(), repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	due := now.Add(-time.Minute)
	later := now.Add(time.Hour)

	create := func(id int64, state string, retryAt *time.Time) {
		s.T().Helper()
		_, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
			ID:          snow.ID(id),
			WebhookID:   webhook.ID,
			EventType:   domain.WebhookEventPush,
			Payload:     []byte(`{}`),
			State:       state,
			NextRetryAt: retryAt,
		})
		s.Require().NoError(err)
	}
	create(5001, domain.WebhookDeliveryPending, &due)
	create(5002, domain.WebhookDeliveryPending, &later)
	create(5003, domain.WebhookDeliveryDelivered, nil)
	create(5004, domain.WebhookDeliveryPending, nil)

	retries, err := repo.ListPendingRetries(ctx, now, 10)
	s.Require().NoError(err)
	s.Len(retries, 1)
	s.Equal(snow.ID(5001), retries[0].ID)

	retries, err = repo.ListPendingRetries(ctx, now, 0)
	s.Require().NoError(err)
	s.Empty(retries)
}

func (s *WebhookRepositorySuite) TestDeleteOldDeliveries() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")
	webhook := seedWebhook(s.T(), repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

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
		s.Require().NoError(err)
	}

	deleted, err := repo.DeleteOldDeliveries(ctx, webhook.ID, 2)
	s.Require().NoError(err)
	s.Equal(int64(1), deleted, "the oldest delivered row is trimmed, the pending one is kept")

	remaining, err := repo.ListDeliveries(ctx, webhook.ID, 10, 0)
	s.Require().NoError(err)
	s.Len(remaining, 3)
	s.Equal(snow.ID(5002), remaining[2].ID)
}

func (s *WebhookRepositorySuite) TestDeleteRemovesDeliveries() {
	ctx := context.Background()
	repo := NewWebhookRepository(s.db)
	projectID := seedProject(s.T(), s.q, 1, "game")
	webhook := seedWebhook(s.T(), repo, projectID, 1001, "ci", []string{domain.WebhookEventPush})

	_, err := repo.CreateDelivery(ctx, domain.WebhookDelivery{
		ID:        snow.ID(5001),
		WebhookID: webhook.ID,
		EventType: domain.WebhookEventPush,
		Payload:   []byte(`{}`),
	})
	s.Require().NoError(err)

	s.Require().NoError(repo.Delete(ctx, projectID, webhook.ID))
	_, err = repo.GetDelivery(ctx, webhook.ID, snow.ID(5001))
	requireRecordNotFound(s.T(), err)
}
