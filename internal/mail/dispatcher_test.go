package mail

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeSender struct {
	mu       sync.Mutex
	calls    int
	sent     []Message
	failures int
}

func (s *fakeSender) Send(_ context.Context, msg Message) error {
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

func (s *fakeSender) Close() error { return nil }

func (s *fakeSender) stats() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, len(s.sent)
}

func newOutboxTestRepo(t *testing.T) (*sqlite.EmailRepository, *sql.DB) {
	t.Helper()
	db, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	require.NoError(t, database.MigrateUp(db, "sqlite3"))
	return sqlite.NewEmailRepository(db), db
}

func enqueueTestDelivery(t *testing.T, repo *sqlite.EmailRepository, id int64, next time.Time) {
	t.Helper()
	body, err := EncodeMessage(Message{To: []string{"dev@example.com"}, Subject: "subject", Text: "body"})
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

func startTestDispatcher(t *testing.T, repo *sqlite.EmailRepository, sender Sender, cfg DispatcherConfig) *Dispatcher {
	t.Helper()
	dispatcher := NewDispatcher(repo, sender, cfg)
	dispatcher.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, dispatcher.Stop(ctx))
	})
	return dispatcher
}

func fastDispatcherConfig() DispatcherConfig {
	return DispatcherConfig{
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

func waitForState(t *testing.T, repo *sqlite.EmailRepository, id snow.ID, state string) *domain.EmailDelivery {
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

func TestDispatcher_DeliversDueRow(t *testing.T) {
	repo, _ := newOutboxTestRepo(t)
	sender := &fakeSender{}
	enqueueTestDelivery(t, repo, 1, time.Now().Add(-time.Minute))
	startTestDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForState(t, repo, 1, domain.EmailDeliveryDelivered)
	require.NotNil(t, row.DeliveredAt)
	require.Equal(t, int64(1), row.Attempts)
	calls, sent := sender.stats()
	require.Equal(t, 1, calls)
	require.Equal(t, 1, sent)
	require.Equal(t, "dev@example.com", sender.sent[0].To[0])
}

func TestDispatcher_RetriesThenFails(t *testing.T) {
	repo, _ := newOutboxTestRepo(t)
	sender := &fakeSender{failures: 100}
	enqueueTestDelivery(t, repo, 2, time.Now().Add(-time.Minute))
	startTestDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForState(t, repo, 2, domain.EmailDeliveryFailed)
	require.Equal(t, int64(3), row.Attempts)
	require.Contains(t, row.LastError, "smtp down")
	calls, sent := sender.stats()
	require.Equal(t, 3, calls)
	require.Zero(t, sent)

	time.Sleep(50 * time.Millisecond)
	calls, _ = sender.stats()
	require.Equal(t, 3, calls, "a failed delivery is not attempted again")
}

func TestDispatcher_RetriesAfterBackoff(t *testing.T) {
	repo, _ := newOutboxTestRepo(t)
	sender := &fakeSender{failures: 1}
	enqueueTestDelivery(t, repo, 3, time.Now().Add(-time.Minute))
	cfg := fastDispatcherConfig()
	cfg.InitialBackoff = 40 * time.Millisecond
	startTestDispatcher(t, repo, sender, cfg)

	row := waitForState(t, repo, 3, domain.EmailDeliveryDelivered)
	require.Equal(t, int64(2), row.Attempts)
	calls, sent := sender.stats()
	require.Equal(t, 2, calls)
	require.Equal(t, 1, sent)
}

func TestDispatcher_ReclaimsStaleSending(t *testing.T) {
	repo, db := newOutboxTestRepo(t)
	sender := &fakeSender{}
	enqueueTestDelivery(t, repo, 4, time.Now().Add(-time.Minute))
	claimed, err := repo.ClaimDue(context.Background(), time.Now(), time.Now().Add(-time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = db.Exec("UPDATE email_deliveries SET claimed_at = ? WHERE id = ?", time.Now().Add(-10*time.Minute), int64(4))
	require.NoError(t, err)

	startTestDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForState(t, repo, 4, domain.EmailDeliveryDelivered)
	require.Equal(t, int64(2), row.Attempts, "the reclaimed row keeps its attempt count")
	calls, _ := sender.stats()
	require.Equal(t, 1, calls)
}

func TestDispatcher_KickPollsImmediately(t *testing.T) {
	repo, _ := newOutboxTestRepo(t)
	sender := &fakeSender{}
	cfg := fastDispatcherConfig()
	cfg.PollInterval = time.Hour
	dispatcher := startTestDispatcher(t, repo, sender, cfg)

	enqueueTestDelivery(t, repo, 5, time.Now().Add(-time.Minute))
	dispatcher.Kick()

	waitForState(t, repo, 5, domain.EmailDeliveryDelivered)
}

func TestDispatcher_EmptyOutboxIsNoop(t *testing.T) {
	repo, _ := newOutboxTestRepo(t)
	dispatcher := NewDispatcher(repo, &fakeSender{}, fastDispatcherConfig())
	dispatcher.Start()
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, dispatcher.Stop(ctx))
}

func TestDispatcher_FailsUndecodableBody(t *testing.T) {
	repo, _ := newOutboxTestRepo(t)
	sender := &fakeSender{}
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
	startTestDispatcher(t, repo, sender, fastDispatcherConfig())

	row := waitForState(t, repo, 6, domain.EmailDeliveryFailed)
	require.Contains(t, row.LastError, "decode")
	calls, _ := sender.stats()
	require.Zero(t, calls)
}

func TestDispatcher_RetentionSweep(t *testing.T) {
	repo, _ := newOutboxTestRepo(t)
	sender := &fakeSender{}
	enqueueTestDelivery(t, repo, 7, time.Now().Add(-time.Minute))
	cfg := fastDispatcherConfig()
	cfg.Retention = time.Nanosecond
	cfg.RetentionSweepInterval = 10 * time.Millisecond
	startTestDispatcher(t, repo, sender, cfg)

	waitForState(t, repo, 7, domain.EmailDeliveryDelivered)
	require.Eventually(t, func() bool {
		_, err := repo.Get(context.Background(), 7)
		return domain.IsErrorNotFound(err)
	}, 3*time.Second, 10*time.Millisecond, "the delivered row was never swept")
}

type stubStore struct {
	DispatcherStore
	claimErr         error
	markDeliveredErr error
	scheduleRetryErr error
	markFailedErr    error
	sweepErr         error
}

func (s stubStore) ClaimDue(context.Context, time.Time, time.Time, int64) ([]*domain.EmailDelivery, error) {
	return nil, s.claimErr
}

func (s stubStore) MarkDelivered(context.Context, snow.ID, time.Time) error {
	return s.markDeliveredErr
}

func (s stubStore) ScheduleRetry(context.Context, snow.ID, string, time.Time) error {
	return s.scheduleRetryErr
}

func (s stubStore) MarkFailed(context.Context, snow.ID, string) error {
	return s.markFailedErr
}

func (s stubStore) SweepRetention(context.Context, time.Time) (int64, error) {
	return 0, s.sweepErr
}

func TestDispatcherConfigDefaults(t *testing.T) {
	cfg := DispatcherConfig{}.withDefaults()
	require.Equal(t, defaultEmailWorkers, cfg.Workers)
	require.Equal(t, defaultPollInterval, cfg.PollInterval)
	require.Equal(t, int64(defaultBatchSize), cfg.BatchSize)
	require.Equal(t, defaultMaxAttempts, cfg.MaxAttempts)
	require.Equal(t, defaultInitialBackoff, cfg.InitialBackoff)
	require.Equal(t, defaultBackoffFactor, cfg.BackoffFactor)
	require.Equal(t, defaultStaleAfter, cfg.StaleAfter)
	require.Equal(t, defaultRetention, cfg.Retention)
	require.Equal(t, defaultRetentionSweep, cfg.RetentionSweepInterval)
}

func TestDispatcher_StoreErrorsAreLogged(t *testing.T) {
	body, err := EncodeMessage(Message{To: []string{"dev@example.com"}, Subject: "s", Text: "b"})
	require.NoError(t, err)
	store := stubStore{
		markDeliveredErr: errors.New("db down"),
		scheduleRetryErr: errors.New("db down"),
		markFailedErr:    errors.New("db down"),
		sweepErr:         errors.New("db down"),
	}
	dispatcher := NewDispatcher(store, &fakeSender{}, DispatcherConfig{})

	dispatcher.deliver(domain.EmailDelivery{ID: 1, Body: body})

	failing := NewDispatcher(store, &fakeSender{failures: 100}, DispatcherConfig{})
	failing.deliver(domain.EmailDelivery{ID: 2, Body: body, Attempts: 1})

	failing.markFailed(domain.EmailDelivery{ID: 3}, errors.New("boom"))
	failing.trim()
}

func TestDispatcher_StopWithoutStartAndAfterStop(t *testing.T) {
	dispatcher := NewDispatcher(stubStore{}, &fakeSender{}, DispatcherConfig{})
	require.NoError(t, dispatcher.Stop(context.Background()))

	dispatcher.Start()
	dispatcher.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, dispatcher.Stop(ctx))
	require.NoError(t, dispatcher.Stop(ctx))
	dispatcher.Start()
}

func TestDispatcher_SweepErrorIsLogged(t *testing.T) {
	dispatcher := NewDispatcher(stubStore{claimErr: errors.New("db down")}, &fakeSender{}, fastDispatcherConfig())
	dispatcher.Start()
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, dispatcher.Stop(ctx))
}

func TestMessageCodecRoundTrip(t *testing.T) {
	original := Message{
		From:       "Nipa <noreply@example.com>",
		To:         []string{"dev@example.com"},
		Subject:    "MR ready",
		Text:       "plain",
		HTML:       "<p>html</p>",
		MessageID:  "msg-1@nipa",
		InReplyTo:  "root@nipa",
		References: []string{"root@nipa"},
	}
	body, err := EncodeMessage(original)
	require.NoError(t, err)
	decoded, err := DecodeMessage(body)
	require.NoError(t, err)
	require.Equal(t, original, decoded)

	_, err = DecodeMessage([]byte("{not json"))
	require.Error(t, err)
}
