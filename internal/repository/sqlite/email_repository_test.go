package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

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

func TestEmailRepositorySQLite_EnqueueAndClaimDue(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

	now := time.Now()
	due := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	require.NoError(t, repo.Enqueue(ctx, []*domain.EmailDelivery{
		testEmailDelivery(1, &due),
		testEmailDelivery(2, &due),
		testEmailDelivery(3, &future),
	}))

	claimed, err := repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 2, "only the due rows are claimed")
	for _, delivery := range claimed {
		require.Equal(t, domain.EmailDeliverySending, delivery.State)
		require.Equal(t, int64(1), delivery.Attempts)
		require.NotNil(t, delivery.ClaimedAt)
	}

	claimed, err = repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	require.NoError(t, err)
	require.Empty(t, claimed, "sending rows are not claimed twice")

	futureRow, err := repo.Get(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryPending, futureRow.State)

	claimed, err = repo.ClaimDue(ctx, now, now.Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 2, "stale sending rows are reclaimed and claimed again")
	require.Equal(t, int64(2), claimed[0].Attempts)

	claimed, err = repo.ClaimDue(ctx, now, now.Add(time.Hour), 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "the limit caps one claim")
	require.Equal(t, int64(3), claimed[0].Attempts)
}

func TestEmailRepositorySQLite_Lifecycle(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

	now := time.Now()
	due := now.Add(-time.Minute)
	require.NoError(t, repo.Enqueue(ctx, []*domain.EmailDelivery{
		testEmailDelivery(1, &due),
		testEmailDelivery(2, &due),
		testEmailDelivery(3, &due),
	}))
	claimed, err := repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 3)

	require.NoError(t, repo.MarkDelivered(ctx, claimed[0].ID, now))
	delivered, err := repo.Get(ctx, claimed[0].ID)
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryDelivered, delivered.State)
	require.NotNil(t, delivered.DeliveredAt)
	require.Nil(t, delivered.NextAttemptAt)
	require.Empty(t, delivered.LastError)

	retryAt := now.Add(time.Minute)
	require.NoError(t, repo.ScheduleRetry(ctx, claimed[1].ID, "smtp down", retryAt))
	pending, err := repo.Get(ctx, claimed[1].ID)
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryPending, pending.State)
	require.Equal(t, "smtp down", pending.LastError)
	require.Nil(t, pending.ClaimedAt)
	require.NotNil(t, pending.NextAttemptAt)
	require.WithinDuration(t, retryAt, *pending.NextAttemptAt, time.Second)

	require.NoError(t, repo.MarkFailed(ctx, claimed[2].ID, "boom"))
	failed, err := repo.Get(ctx, claimed[2].ID)
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryFailed, failed.State)
	require.Equal(t, "boom", failed.LastError)
	require.Nil(t, failed.NextAttemptAt)
}

func TestEmailRepositorySQLite_SweepRetention(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

	now := time.Now()
	due := now.Add(-time.Minute)
	future := now.Add(time.Hour)
	require.NoError(t, repo.Enqueue(ctx, []*domain.EmailDelivery{
		testEmailDelivery(1, &due),
		testEmailDelivery(2, &due),
		testEmailDelivery(3, &due),
		testEmailDelivery(4, &future),
	}))
	claimed, err := repo.ClaimDue(ctx, now, now.Add(-5*time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 3)
	require.NoError(t, repo.MarkDelivered(ctx, claimed[0].ID, now))
	require.NoError(t, repo.MarkFailed(ctx, claimed[1].ID, "boom"))

	old := now.Add(-48 * time.Hour)
	_, err = db.ExecContext(ctx, "UPDATE email_deliveries SET updated_at = ? WHERE id IN (?, ?)", old, int64(1), int64(2))
	require.NoError(t, err)

	deleted, err := repo.SweepRetention(ctx, now.Add(-24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)

	_, err = repo.Get(ctx, 1)
	require.True(t, domain.IsErrorNotFound(err))
	_, err = repo.Get(ctx, 2)
	require.True(t, domain.IsErrorNotFound(err))
	remaining, err := repo.Get(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryPending, remaining.State, "pending rows are never swept")
}

func TestEmailRepositorySQLite_EnqueueDefaultsToImmediate(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

	require.NoError(t, repo.Enqueue(ctx, []*domain.EmailDelivery{testEmailDelivery(1, nil)}))
	claimed, err := repo.ClaimDue(ctx, time.Now(), time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "a delivery without a scheduled attempt is due immediately")
	require.Equal(t, domain.EmailDeliverySending, claimed[0].State)
}

func TestEmailRepositorySQLite_EnqueueRollsBack(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

	due := time.Now()
	require.NoError(t, repo.Enqueue(ctx, []*domain.EmailDelivery{testEmailDelivery(1, &due)}))

	err := repo.Enqueue(ctx, []*domain.EmailDelivery{
		testEmailDelivery(2, &due),
		testEmailDelivery(1, &due),
	})
	require.Error(t, err, "duplicate ids fail the batch")

	_, err = repo.Get(ctx, 1)
	require.NoError(t, err, "the first enqueue is untouched")
	_, err = repo.Get(ctx, 2)
	require.True(t, domain.IsErrorNotFound(err), "the failed batch rolled back")
}

func TestEmailRepositorySQLite_ThreadRecipients(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

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
	require.NoError(t, repo.Enqueue(ctx, []*domain.EmailDelivery{first, second, other}))

	recipients, err := repo.ThreadRecipients(ctx, 7, "acme/game/mr1")
	require.NoError(t, err)
	require.Equal(t, map[snow.ID]struct{}{10: {}, 20: {}}, recipients)

	recipients, err = repo.ThreadRecipients(ctx, 7, "acme/game/mr2")
	require.NoError(t, err)
	require.Equal(t, map[snow.ID]struct{}{30: {}}, recipients)

	recipients, err = repo.ThreadRecipients(ctx, 7, "acme/game/mr3")
	require.NoError(t, err)
	require.Empty(t, recipients)

	row, err := repo.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "acme/game/mr1", row.ThreadKey)
}

func TestEmailRepositorySQLite_ListDeliveries(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

	now := time.Now()
	rows := []*domain.EmailDelivery{
		testEmailDelivery(10, &now),
		testEmailDelivery(20, &now),
		testEmailDelivery(30, &now),
	}
	rows[0].State = domain.EmailDeliveryDelivered
	rows[1].State = domain.EmailDeliveryFailed
	require.NoError(t, repo.Enqueue(ctx, rows))
	require.NoError(t, repo.MarkFailed(ctx, 20, "smtp down"))

	list, err := repo.ListDeliveries(ctx, 7, "", nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 3)
	require.Equal(t, snow.ID(30), list[0].ID, "newest first")

	failed, err := repo.ListDeliveries(ctx, 7, domain.EmailDeliveryFailed, nil, 10)
	require.NoError(t, err)
	require.Len(t, failed, 1)
	require.Equal(t, snow.ID(20), failed[0].ID)
	require.Equal(t, "smtp down", failed[0].LastError)

	page, err := repo.ListDeliveries(ctx, 7, "", nil, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)
	after := page[len(page)-1].ID
	page2, err := repo.ListDeliveries(ctx, 7, "", &after, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, snow.ID(10), page2[0].ID)

	other, err := repo.ListDeliveries(ctx, 8, "", nil, 10)
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestEmailRepositorySQLite_Redeliver(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)

	now := time.Now()
	require.NoError(t, repo.Enqueue(ctx, []*domain.EmailDelivery{testEmailDelivery(1, &now)}))
	claimed, err := repo.ClaimDue(ctx, now, now.Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, repo.MarkDelivered(ctx, 1, now))
	delivered, err := repo.Get(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, delivered.DeliveredAt)

	at := now.Add(time.Minute)
	require.NoError(t, repo.Redeliver(ctx, 7, 1, at))
	row, err := repo.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryPending, row.State)
	require.Zero(t, row.Attempts)
	require.Nil(t, row.DeliveredAt, "redelivery clears the delivered marker")
	require.Empty(t, row.LastError)
	require.NotNil(t, row.NextAttemptAt)

	require.True(t, domain.IsErrorNotFound(repo.Redeliver(ctx, 8, 1, at)), "another project cannot redeliver")
	require.True(t, domain.IsErrorNotFound(repo.Redeliver(ctx, 7, 999, at)))

	require.NoError(t, repo.MarkDelivered(ctx, 1, at))
	require.NoError(t, repo.MarkFailed(ctx, 1, "boom"))
	failed, err := repo.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, domain.EmailDeliveryFailed, failed.State)
	require.Nil(t, failed.DeliveredAt, "a failed delivery is not marked delivered")
}

func TestEmailRepositorySQLite_Errors(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	require.NoError(t, db.Close())

	due := time.Now()
	require.Error(t, repo.Enqueue(ctx, []*domain.EmailDelivery{testEmailDelivery(1, &due)}))
	_, err := repo.ClaimDue(ctx, time.Now(), time.Now(), 10)
	require.Error(t, err)
	_, err = repo.Get(ctx, 1)
	require.Error(t, err)
	_, err = repo.ThreadRecipients(ctx, 1, "acme/game/mr1")
	require.Error(t, err)
	require.Error(t, repo.MarkDelivered(ctx, 1, time.Now()))
	require.Error(t, repo.ScheduleRetry(ctx, 1, "x", time.Now()))
	require.Error(t, repo.MarkFailed(ctx, 1, "x"))
	_, err = repo.SweepRetention(ctx, time.Now())
	require.Error(t, err)
}
