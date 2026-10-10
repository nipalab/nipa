package mail

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
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

func TestDispatcher_EmptyOutboxIsNoop(t *testing.T) {
	dispatcher := NewDispatcher(stubStore{}, &fakeSender{}, DispatcherConfig{
		PollInterval: 10 * time.Millisecond,
	})
	dispatcher.Start()
	time.Sleep(30 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, dispatcher.Stop(ctx))
}

func TestDispatcher_SweepErrorIsLogged(t *testing.T) {
	dispatcher := NewDispatcher(stubStore{claimErr: errors.New("db down")}, &fakeSender{}, DispatcherConfig{
		PollInterval: 10 * time.Millisecond,
	})
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
