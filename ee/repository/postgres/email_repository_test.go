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
	s.Error(repo.MarkDelivered(ctx, 1, time.Now()))
	s.Error(repo.ScheduleRetry(ctx, 1, "x", time.Now()))
	s.Error(repo.MarkFailed(ctx, 1, "x"))
	_, err = repo.SweepRetention(ctx, time.Now())
	s.Error(err)
}
