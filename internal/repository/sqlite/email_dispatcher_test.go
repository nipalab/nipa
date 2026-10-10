package sqlite

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/snow"
)

type dispatcherFakeSender struct {
	mu       sync.Mutex
	calls    int
	sent     []mail.Message
	failures int
}

func (s *dispatcherFakeSender) Send(_ context.Context, msg mail.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failures > 0 {
		s.failures--
		return errors.New("smtp down")
	}
	s.sent = append(s.sent, msg)
	return nil
}

func (s *dispatcherFakeSender) Close() error { return nil }

func (s *dispatcherFakeSender) stats() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, len(s.sent)
}

func enqueueTestDelivery(t *testing.T, repo *EmailRepository, id int64, next time.Time) {
	t.Helper()
	body, err := mail.EncodeMessage(mail.Message{To: []string{"dev@example.com"}, Subject: "subject", Text: "body"})
	require.NoError(t, err)
	require.NoError(t, repo.Enqueue(context.Background(), []*domain.EmailDelivery{{
		ID:            snow.ID(id),
		Event:         "mr.created",
		ProjectID:     1,
		UserID:        2,
		Email:         "dev@example.com",
		Subject:       "subject",
		Body:          body,
		State:         domain.EmailDeliveryPending,
		NextAttemptAt: &next,
	}}))
}

func fastDispatcherConfig() mail.DispatcherConfig {
	return mail.DispatcherConfig{
		Workers:                2,
		PollInterval:           10 * time.Millisecond,
		BatchSize:              10,
		MaxAttempts:            3,
		InitialBackoff:         5 * time.Millisecond,
		BackoffFactor:          2,
		StaleAfter:             5 * time.Minute,
		Retention:              time.Hour,
		RetentionSweepInterval: time.Hour,
	}
}

func startEmailDispatcher(t *testing.T, repo *EmailRepository, sender mail.Sender, cfg mail.DispatcherConfig) *mail.Dispatcher {
	t.Helper()
	dispatcher := mail.NewDispatcher(repo, sender, cfg)
	dispatcher.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, dispatcher.Stop(ctx))
	})
	return dispatcher
}

func waitForDeliveryState(t *testing.T, repo *EmailRepository, id snow.ID, state string) *domain.EmailDelivery {
	t.Helper()
	var row *domain.EmailDelivery
	require.Eventually(t, func() bool {
		current, err := repo.Get(context.Background(), id)
		if err != nil || current.State != state {
			return false
		}
		row = current
		return true
	}, 3*time.Second, 10*time.Millisecond, "delivery %d never reached %s", id, state)
	return row
}

func TestEmailDispatcher_DeliversDueRow(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{}
	enqueueTestDelivery(t, repo, 1, time.Now().Add(-time.Minute))
	startEmailDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForDeliveryState(t, repo, 1, domain.EmailDeliveryDelivered)
	require.NotNil(t, row.DeliveredAt)
	require.Equal(t, int64(1), row.Attempts)
	calls, sent := sender.stats()
	require.Equal(t, 1, calls)
	require.Equal(t, 1, sent)
	require.Equal(t, "dev@example.com", sender.sent[0].To[0])
}

func TestEmailDispatcher_DeliversUnscheduledDelivery(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{}
	body, err := mail.EncodeMessage(mail.Message{To: []string{"dev@example.com"}, Subject: "subject", Text: "body"})
	require.NoError(t, err)
	require.NoError(t, repo.Enqueue(context.Background(), []*domain.EmailDelivery{{
		ID:        snow.ID(8),
		Event:     "mr.created",
		ProjectID: 1,
		UserID:    2,
		Email:     "dev@example.com",
		Subject:   "subject",
		Body:      body,
		State:     domain.EmailDeliveryPending,
	}}))
	startEmailDispatcher(t, repo, sender, fastDispatcherConfig())

	waitForDeliveryState(t, repo, 8, domain.EmailDeliveryDelivered)
	calls, sent := sender.stats()
	require.Equal(t, 1, calls)
	require.Equal(t, 1, sent)
}

func TestEmailDispatcher_RetriesThenFails(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{failures: 100}
	enqueueTestDelivery(t, repo, 2, time.Now().Add(-time.Minute))
	startEmailDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForDeliveryState(t, repo, 2, domain.EmailDeliveryFailed)
	require.Equal(t, int64(3), row.Attempts)
	require.Contains(t, row.LastError, "smtp down")
	calls, sent := sender.stats()
	require.Equal(t, 3, calls)
	require.Zero(t, sent)

	time.Sleep(50 * time.Millisecond)
	calls, _ = sender.stats()
	require.Equal(t, 3, calls, "a failed delivery is not attempted again")
}

func TestEmailDispatcher_RetriesAfterBackoff(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{failures: 1}
	enqueueTestDelivery(t, repo, 3, time.Now().Add(-time.Minute))
	cfg := fastDispatcherConfig()
	cfg.InitialBackoff = 40 * time.Millisecond
	startEmailDispatcher(t, repo, sender, cfg)

	row := waitForDeliveryState(t, repo, 3, domain.EmailDeliveryDelivered)
	require.Equal(t, int64(2), row.Attempts)
	calls, sent := sender.stats()
	require.Equal(t, 2, calls)
	require.Equal(t, 1, sent)
}

func TestEmailDispatcher_ReclaimsStaleSending(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{}
	enqueueTestDelivery(t, repo, 4, time.Now().Add(-time.Minute))
	claimed, err := repo.ClaimDue(context.Background(), time.Now(), time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = db.Exec("UPDATE email_deliveries SET claimed_at = ? WHERE id = ?", time.Now().Add(-10*time.Minute), int64(4))
	require.NoError(t, err)

	startEmailDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForDeliveryState(t, repo, 4, domain.EmailDeliveryDelivered)
	require.Equal(t, int64(2), row.Attempts, "the reclaimed row keeps its attempt count")
	calls, _ := sender.stats()
	require.Equal(t, 1, calls)
}

func TestEmailDispatcher_KickPollsImmediately(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{}
	cfg := fastDispatcherConfig()
	cfg.PollInterval = time.Hour
	dispatcher := startEmailDispatcher(t, repo, sender, cfg)

	enqueueTestDelivery(t, repo, 5, time.Now().Add(-time.Minute))
	dispatcher.Kick()

	waitForDeliveryState(t, repo, 5, domain.EmailDeliveryDelivered)
}

func TestEmailDispatcher_FailsUndecodableBody(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{}
	next := time.Now().Add(-time.Minute)
	require.NoError(t, repo.Enqueue(context.Background(), []*domain.EmailDelivery{{
		ID:            snow.ID(6),
		Event:         "mr.created",
		ProjectID:     1,
		UserID:        2,
		Email:         "dev@example.com",
		Subject:       "subject",
		Body:          []byte("{not json"),
		State:         domain.EmailDeliveryPending,
		NextAttemptAt: &next,
	}}))
	startEmailDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForDeliveryState(t, repo, 6, domain.EmailDeliveryFailed)
	require.Contains(t, row.LastError, "decode")
	calls, _ := sender.stats()
	require.Zero(t, calls)
}

func TestEmailDispatcher_RetentionSweep(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewEmailRepository(db)
	sender := &dispatcherFakeSender{}
	enqueueTestDelivery(t, repo, 7, time.Now().Add(-time.Minute))
	cfg := fastDispatcherConfig()
	cfg.Retention = time.Nanosecond
	cfg.RetentionSweepInterval = 10 * time.Millisecond
	startEmailDispatcher(t, repo, sender, cfg)

	waitForDeliveryState(t, repo, 7, domain.EmailDeliveryDelivered)
	require.Eventually(t, func() bool {
		_, err := repo.Get(context.Background(), 7)
		return domain.IsErrorNotFound(err)
	}, 3*time.Second, 10*time.Millisecond, "the delivered row was never swept")
}
