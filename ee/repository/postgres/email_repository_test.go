package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type EmailRepositorySuite struct {
	baseSuite
}

func TestEmailRepositorySuite(t *testing.T) {
	suite.Run(t, new(EmailRepositorySuite))
}

func testEmailDelivery(id int64, next *time.Time) *domain.EmailDelivery {
	return &domain.EmailDelivery{
		ID:            snow.ID(id),
		Event:         "mr.comment_created",
		ProjectID:     7,
		UserID:        42,
		Email:         "dev@example.com",
		Subject:       "subject",
		Body:          []byte(`{"to":["dev@example.com"],"subject":"subject","text":"body"}`),
		State:         domain.EmailDeliveryPending,
		NextAttemptAt: next,
	}
}

func (s *EmailRepositorySuite) TestEnqueueAndClaimDue() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)

	now := time.Now()
	due := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	s.Require().NoError(repo.Enqueue(ctx, []*domain.EmailDelivery{
		testEmailDelivery(1, &due),
		testEmailDelivery(2, &due),
		testEmailDelivery(3, &future),
	}))

	claimed, err := repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	s.Require().NoError(err)
	s.Len(claimed, 2, "only the due rows are claimed")
	for _, delivery := range claimed {
		s.Equal(domain.EmailDeliverySending, delivery.State)
		s.Equal(int64(1), delivery.Attempts)
		s.NotNil(delivery.ClaimedAt)
	}

	claimed, err = repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	s.Require().NoError(err)
	s.Empty(claimed, "sending rows are not claimed twice")

	futureRow, err := repo.Get(ctx, 3)
	s.Require().NoError(err)
	s.Equal(domain.EmailDeliveryPending, futureRow.State)

	claimed, err = repo.ClaimDue(ctx, now, now.Add(time.Hour), 10)
	s.Require().NoError(err)
	s.Len(claimed, 2, "stale sending rows are reclaimed and claimed again")
	s.Equal(int64(2), claimed[0].Attempts)

	claimed, err = repo.ClaimDue(ctx, now, now.Add(time.Hour), 1)
	s.Require().NoError(err)
	s.Len(claimed, 1, "the limit caps one claim")
	s.Equal(int64(3), claimed[0].Attempts)
}

func (s *EmailRepositorySuite) TestEnqueueDefaultsToImmediate() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)

	s.Require().NoError(repo.Enqueue(ctx, []*domain.EmailDelivery{testEmailDelivery(1, nil)}))
	claimed, err := repo.ClaimDue(ctx, time.Now(), time.Now().Add(-time.Minute), 10)
	s.Require().NoError(err)
	s.Require().Len(claimed, 1, "a delivery without a scheduled attempt is due immediately")
	s.Equal(domain.EmailDeliverySending, claimed[0].State)
}

func (s *EmailRepositorySuite) TestLifecycle() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)

	now := time.Now()
	due := now.Add(-time.Minute)
	s.Require().NoError(repo.Enqueue(ctx, []*domain.EmailDelivery{
		testEmailDelivery(1, &due),
		testEmailDelivery(2, &due),
		testEmailDelivery(3, &due),
	}))
	claimed, err := repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	s.Require().NoError(err)
	s.Require().Len(claimed, 3)

	s.Require().NoError(repo.MarkDelivered(ctx, claimed[0].ID, now))
	delivered, err := repo.Get(ctx, claimed[0].ID)
	s.Require().NoError(err)
	s.Equal(domain.EmailDeliveryDelivered, delivered.State)
	s.NotNil(delivered.DeliveredAt)
	s.Nil(delivered.NextAttemptAt)
	s.Empty(delivered.LastError)

	retryAt := now.Add(time.Minute)
	s.Require().NoError(repo.ScheduleRetry(ctx, claimed[1].ID, "smtp down", retryAt))
	pending, err := repo.Get(ctx, claimed[1].ID)
	s.Require().NoError(err)
	s.Equal(domain.EmailDeliveryPending, pending.State)
	s.Equal("smtp down", pending.LastError)
	s.Nil(pending.ClaimedAt)
	s.Require().NotNil(pending.NextAttemptAt)
	s.WithinDuration(retryAt, *pending.NextAttemptAt, time.Second)

	s.Require().NoError(repo.MarkFailed(ctx, claimed[2].ID, "boom"))
	failed, err := repo.Get(ctx, claimed[2].ID)
	s.Require().NoError(err)
	s.Equal(domain.EmailDeliveryFailed, failed.State)
	s.Equal("boom", failed.LastError)
	s.Nil(failed.NextAttemptAt)
}

func (s *EmailRepositorySuite) TestSweepRetention() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)

	now := time.Now()
	due := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	s.Require().NoError(repo.Enqueue(ctx, []*domain.EmailDelivery{
		testEmailDelivery(1, &due),
		testEmailDelivery(2, &due),
		testEmailDelivery(3, &due),
		testEmailDelivery(4, &future),
	}))
	claimed, err := repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	s.Require().NoError(err)
	s.Require().Len(claimed, 3)
	s.Require().NoError(repo.MarkDelivered(ctx, claimed[0].ID, now))
	s.Require().NoError(repo.MarkFailed(ctx, claimed[1].ID, "boom"))

	old := now.Add(-48 * time.Hour)
	_, err = s.db.ExecContext(ctx, "UPDATE email_deliveries SET updated_at = $1 WHERE id IN ($2, $3)", old, int64(1), int64(2))
	s.Require().NoError(err)

	deleted, err := repo.SweepRetention(ctx, now.Add(-24*time.Hour))
	s.Require().NoError(err)
	s.Equal(int64(2), deleted)

	_, err = repo.Get(ctx, 1)
	s.True(domain.IsErrorNotFound(err))
	_, err = repo.Get(ctx, 2)
	s.True(domain.IsErrorNotFound(err))
	remaining, err := repo.Get(ctx, 4)
	s.Require().NoError(err)
	s.Equal(domain.EmailDeliveryPending, remaining.State, "pending rows are never swept")
}

func (s *EmailRepositorySuite) TestThreadRecipients() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)

	now := time.Now()
	first := testEmailDelivery(1, &now)
	first.UserID = 10
	first.ThreadKey = "acme/game/mr1"
	second := testEmailDelivery(2, &now)
	second.UserID = 20
	second.ThreadKey = "acme/game/mr1"
	other := testEmailDelivery(3, &now)
	other.UserID = 30
	other.ThreadKey = "acme/game/mr2"
	s.Require().NoError(repo.Enqueue(ctx, []*domain.EmailDelivery{first, second, other}))

	recipients, err := repo.ThreadRecipients(ctx, 7, "acme/game/mr1")
	s.Require().NoError(err)
	s.Equal(map[snow.ID]struct{}{10: {}, 20: {}}, recipients)

	recipients, err = repo.ThreadRecipients(ctx, 7, "acme/game/mr2")
	s.Require().NoError(err)
	s.Equal(map[snow.ID]struct{}{30: {}}, recipients)

	recipients, err = repo.ThreadRecipients(ctx, 7, "acme/game/mr3")
	s.Require().NoError(err)
	s.Empty(recipients)

	row, err := repo.Get(ctx, 1)
	s.Require().NoError(err)
	s.Equal("acme/game/mr1", row.ThreadKey)
}

func (s *EmailRepositorySuite) TestListDeliveries() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)

	now := time.Now()
	rows := []*domain.EmailDelivery{
		testEmailDelivery(10, &now),
		testEmailDelivery(20, &now),
		testEmailDelivery(30, &now),
	}
	rows[0].State = domain.EmailDeliveryDelivered
	rows[1].State = domain.EmailDeliveryFailed
	s.Require().NoError(repo.Enqueue(ctx, rows))
	s.Require().NoError(repo.MarkFailed(ctx, 20, "smtp down"))

	list, err := repo.ListDeliveries(ctx, 7, "", nil, 10)
	s.Require().NoError(err)
	s.Len(list, 3)
	s.Equal(snow.ID(30), list[0].ID, "newest first")

	failed, err := repo.ListDeliveries(ctx, 7, domain.EmailDeliveryFailed, nil, 10)
	s.Require().NoError(err)
	s.Require().Len(failed, 1)
	s.Equal(snow.ID(20), failed[0].ID)
	s.Equal("smtp down", failed[0].LastError)

	page, err := repo.ListDeliveries(ctx, 7, "", nil, 2)
	s.Require().NoError(err)
	s.Len(page, 2)
	after := page[len(page)-1].ID
	page2, err := repo.ListDeliveries(ctx, 7, "", &after, 2)
	s.Require().NoError(err)
	s.Require().Len(page2, 1)
	s.Equal(snow.ID(10), page2[0].ID)

	other, err := repo.ListDeliveries(ctx, 8, "", nil, 10)
	s.Require().NoError(err)
	s.Empty(other)
}

func (s *EmailRepositorySuite) TestRedeliver() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)

	now := time.Now()
	s.Require().NoError(repo.Enqueue(ctx, []*domain.EmailDelivery{testEmailDelivery(1, &now)}))
	claimed, err := repo.ClaimDue(ctx, now, now.Add(-time.Minute), 10)
	s.Require().NoError(err)
	s.Require().Len(claimed, 1)
	s.Require().NoError(repo.MarkDelivered(ctx, 1, now))
	delivered, err := repo.Get(ctx, 1)
	s.Require().NoError(err)
	s.NotNil(delivered.DeliveredAt)

	at := now.Add(time.Minute)
	requeued, err := repo.Redeliver(ctx, 7, 1, at)
	s.Require().NoError(err)
	s.True(requeued)
	row, err := repo.Get(ctx, 1)
	s.Require().NoError(err)
	s.Equal(domain.EmailDeliveryPending, row.State)
	s.Zero(row.Attempts)
	s.Nil(row.DeliveredAt, "redelivery clears the delivered marker")
	s.Empty(row.LastError)
	s.NotNil(row.NextAttemptAt)

	requeued, err = repo.Redeliver(ctx, 8, 1, at)
	s.Require().NoError(err)
	s.False(requeued, "another project cannot redeliver")
	requeued, err = repo.Redeliver(ctx, 7, 999, at)
	s.Require().NoError(err)
	s.False(requeued)
	requeued, err = repo.Redeliver(ctx, 7, 1, at)
	s.Require().NoError(err)
	s.False(requeued, "a pending delivery is not redeliverable")

	s.Require().NoError(repo.MarkDelivered(ctx, 1, at))
	s.Require().NoError(repo.MarkFailed(ctx, 1, "boom"))
	failed, err := repo.Get(ctx, 1)
	s.Require().NoError(err)
	s.Equal(domain.EmailDeliveryFailed, failed.State)
	s.Nil(failed.DeliveredAt, "a failed delivery is not marked delivered")
}

func (s *EmailRepositorySuite) TestErrors() {
	ctx := context.Background()
	repo := NewEmailRepository(s.db)
	s.Require().NoError(s.db.Close())

	due := time.Now()
	s.Error(repo.Enqueue(ctx, []*domain.EmailDelivery{testEmailDelivery(1, &due)}))
	_, err := repo.ClaimDue(ctx, time.Now(), time.Now(), 10)
	s.Error(err)
	_, err = repo.Get(ctx, 1)
	s.Error(err)
	_, err = repo.ThreadRecipients(ctx, 1, "acme/game/mr1")
	s.Error(err)
	s.Error(repo.MarkDelivered(ctx, 1, time.Now()))
	s.Error(repo.ScheduleRetry(ctx, 1, "x", time.Now()))
	s.Error(repo.MarkFailed(ctx, 1, "x"))
	_, err = repo.SweepRetention(ctx, time.Now())
	s.Error(err)
	_, err = repo.Redeliver(ctx, 1, 1, time.Now())
	s.Error(err)
}
