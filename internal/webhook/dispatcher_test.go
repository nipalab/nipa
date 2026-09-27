package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeStore struct {
	mu          sync.Mutex
	hooks       map[snow.ID]*domain.Webhook
	deliveries  map[snow.ID]*domain.WebhookDelivery
	reschedules int
	trimmed     int
	createErr   error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		hooks:      map[snow.ID]*domain.Webhook{},
		deliveries: map[snow.ID]*domain.WebhookDelivery{},
	}
}

func (f *fakeStore) addHook(hook domain.Webhook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stored := hook
	f.hooks[hook.ID] = &stored
}

func (f *fakeStore) addPending(id, hookID snow.ID, attempt int64, retryAt time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	retry := retryAt
	f.deliveries[id] = &domain.WebhookDelivery{
		ID: id, WebhookID: hookID, EventType: domain.WebhookEventPush,
		Payload: []byte(`{}`), State: domain.WebhookDeliveryPending,
		Attempt: attempt, NextRetryAt: &retry,
	}
}

func (f *fakeStore) delivery(id snow.ID) (domain.WebhookDelivery, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.deliveries[id]
	if !ok {
		return domain.WebhookDelivery{}, false
	}
	return *row, true
}

func (f *fakeStore) GetByID(_ context.Context, id snow.ID) (*domain.Webhook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hook, ok := f.hooks[id]
	if !ok {
		return nil, domain.NewErrorRecordNotFound()
	}
	stored := *hook
	return &stored, nil
}

func (f *fakeStore) CreateDelivery(_ context.Context, delivery domain.WebhookDelivery) (*domain.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	stored := delivery
	f.deliveries[delivery.ID] = &stored
	return &stored, nil
}

func (f *fakeStore) ListPendingRetries(_ context.Context, now time.Time, limit int64) ([]*domain.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []*domain.WebhookDelivery
	for _, row := range f.deliveries {
		if row.State != domain.WebhookDeliveryPending || row.NextRetryAt == nil || row.NextRetryAt.After(now) {
			continue
		}
		stored := *row
		rows = append(rows, &stored)
		if int64(len(rows)) >= limit {
			break
		}
	}
	return rows, nil
}

func (f *fakeStore) MarkDelivered(_ context.Context, id snow.ID, attempt int64, status *int64, at time.Time) (*domain.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.deliveries[id]
	row.State = domain.WebhookDeliveryDelivered
	row.Attempt = attempt
	row.ResponseStatus = status
	row.DeliveredAt = &at
	row.NextRetryAt = nil
	stored := *row
	return &stored, nil
}

func (f *fakeStore) Reschedule(_ context.Context, id snow.ID, attempt int64, status *int64, lastError string, nextRetryAt time.Time) (*domain.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.deliveries[id]
	row.State = domain.WebhookDeliveryPending
	row.Attempt = attempt
	row.ResponseStatus = status
	row.LastError = lastError
	row.NextRetryAt = &nextRetryAt
	f.reschedules++
	stored := *row
	return &stored, nil
}

func (f *fakeStore) MarkFailed(_ context.Context, id snow.ID, attempt int64, status *int64, lastError string) (*domain.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := f.deliveries[id]
	row.State = domain.WebhookDeliveryFailed
	row.Attempt = attempt
	row.ResponseStatus = status
	row.LastError = lastError
	row.NextRetryAt = nil
	stored := *row
	return &stored, nil
}

func (f *fakeStore) DeleteOldDeliveries(_ context.Context, _ snow.ID, _ int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trimmed++
	return 0, nil
}

func testNode(t *testing.T) snow.Node {
	t.Helper()
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return node
}

func testDispatcherConfig() Config {
	return Config{
		MaxAttempts:    5,
		InitialBackoff: time.Millisecond,
		BackoffFactor:  2,
		SweepInterval:  5 * time.Millisecond,
		RetryBatch:     100,
		Workers:        2,
	}
}

func stopDispatcher(t *testing.T, d *Dispatcher) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, d.Stop(ctx))
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestDispatcher_EnqueueDelivers(t *testing.T) {
	var mu sync.Mutex
	var headers http.Header
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		headers = r.Header.Clone()
		body = data
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	store := newFakeStore()
	hook := domain.Webhook{ID: 1001, URL: server.URL, Secret: "test-secret", IsActive: true}
	store.addHook(hook)

	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig())
	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })

	payload := []byte(`{"event":"push"}`)
	created, err := dispatcher.Enqueue(context.Background(), hook, domain.WebhookEventPush, payload)
	require.NoError(t, err)
	require.Equal(t, domain.WebhookDeliveryPending, created.State)
	require.NotNil(t, created.NextRetryAt)

	waitFor(t, 2*time.Second, func() bool {
		row, ok := store.delivery(created.ID)
		return ok && row.State == domain.WebhookDeliveryDelivered
	})

	row, _ := store.delivery(created.ID)
	require.Equal(t, int64(1), row.Attempt)
	require.NotNil(t, row.ResponseStatus)
	require.Equal(t, int64(200), *row.ResponseStatus)
	require.NotNil(t, row.DeliveredAt)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, payload, body)
	require.Equal(t, "push", headers.Get(EventHeader))
	require.Equal(t, created.ID.Base36(), headers.Get(DeliveryHeader))
	require.Equal(t, hook.ID.Base36(), headers.Get(HookHeader))
	require.True(t, Verify("test-secret", body, headers.Get(SignatureHeader)))

	store.mu.Lock()
	require.GreaterOrEqual(t, store.trimmed, 1)
	store.mu.Unlock()
}

func TestDispatcher_RetriesUntilSuccess(t *testing.T) {
	store := newFakeStore()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	hook := domain.Webhook{ID: 1001, URL: server.URL, Secret: "s", IsActive: true}
	store.addHook(hook)

	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig())
	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })

	created, err := dispatcher.Enqueue(context.Background(), hook, domain.WebhookEventPush, []byte(`{}`))
	require.NoError(t, err)

	waitFor(t, 3*time.Second, func() bool {
		row, ok := store.delivery(created.ID)
		return ok && row.State == domain.WebhookDeliveryDelivered
	})

	row, _ := store.delivery(created.ID)
	require.Equal(t, int64(3), row.Attempt, "two failures then a success")
	require.Equal(t, int32(3), attempts.Load())

	store.mu.Lock()
	require.Equal(t, 2, store.reschedules)
	store.mu.Unlock()
}

func TestDispatcher_MaxAttemptsMarksFailed(t *testing.T) {
	store := newFakeStore()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "always down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	hook := domain.Webhook{ID: 1001, URL: server.URL, Secret: "s", IsActive: true}
	store.addHook(hook)

	cfg := testDispatcherConfig()
	cfg.MaxAttempts = 3
	cfg.Workers = 1
	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), cfg)
	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })

	created, err := dispatcher.Enqueue(context.Background(), hook, domain.WebhookEventPush, []byte(`{}`))
	require.NoError(t, err)

	waitFor(t, 3*time.Second, func() bool {
		row, ok := store.delivery(created.ID)
		return ok && row.State == domain.WebhookDeliveryFailed
	})

	row, _ := store.delivery(created.ID)
	require.Equal(t, int64(3), row.Attempt)
	require.Contains(t, row.LastError, "503")
	require.Nil(t, row.NextRetryAt)
	require.NotNil(t, row.ResponseStatus)
	require.Equal(t, int64(503), *row.ResponseStatus)
}

func TestDispatcher_EgressBlockedFailsImmediately(t *testing.T) {
	store := newFakeStore()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	hook := domain.Webhook{ID: 1001, URL: server.URL, Secret: "s", IsActive: true}
	store.addHook(hook)

	client := NewClient(ClientConfig{EgressAllowlist: []string{"example.com"}})
	dispatcher := NewDispatcher(store, client, testNode(t), testDispatcherConfig())
	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })

	created, err := dispatcher.Enqueue(context.Background(), hook, domain.WebhookEventPush, []byte(`{}`))
	require.NoError(t, err)

	waitFor(t, 2*time.Second, func() bool {
		row, ok := store.delivery(created.ID)
		return ok && row.State == domain.WebhookDeliveryFailed
	})

	row, _ := store.delivery(created.ID)
	require.Equal(t, int64(1), row.Attempt, "blocked deliveries are not retried")
	require.Contains(t, row.LastError, "egress allowlist")
	require.Zero(t, hits.Load())
}

func TestDispatcher_SweepResumesPendingAfterRestart(t *testing.T) {
	store := newFakeStore()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	store.addHook(domain.Webhook{ID: 1001, URL: server.URL, Secret: "s", IsActive: true})
	store.addPending(snow.ID(5001), snow.ID(1001), 2, time.Now().Add(-time.Minute))

	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig())
	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })

	waitFor(t, 2*time.Second, func() bool {
		row, ok := store.delivery(snow.ID(5001))
		return ok && row.State == domain.WebhookDeliveryDelivered
	})
	row, _ := store.delivery(snow.ID(5001))
	require.Equal(t, int64(3), row.Attempt)
	require.Equal(t, int32(1), hits.Load())
}

func TestDispatcher_SweepFailsDeliveryOfMissingHook(t *testing.T) {
	store := newFakeStore()
	store.addPending(snow.ID(5001), snow.ID(9999), 1, time.Now().Add(-time.Minute))

	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig())
	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })

	waitFor(t, 2*time.Second, func() bool {
		row, ok := store.delivery(snow.ID(5001))
		return ok && row.State == domain.WebhookDeliveryFailed
	})
	row, _ := store.delivery(snow.ID(5001))
	require.Contains(t, row.LastError, "no longer exists")
}

func TestDispatcher_EnqueuePropagatesStoreError(t *testing.T) {
	store := newFakeStore()
	store.createErr = domain.NewErrorDatabase("boom")
	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig())

	_, err := dispatcher.Enqueue(context.Background(), domain.Webhook{ID: 1001}, domain.WebhookEventPush, []byte(`{}`))
	require.Error(t, err)
}

func TestDispatcher_EnqueueWhileStoppedStaysPending(t *testing.T) {
	store := newFakeStore()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	hook := domain.Webhook{ID: 1001, URL: server.URL, Secret: "s", IsActive: true}
	store.addHook(hook)

	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig())
	created, err := dispatcher.Enqueue(context.Background(), hook, domain.WebhookEventPush, []byte(`{}`))
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	require.Zero(t, hits.Load(), "a stopped dispatcher must not deliver")

	dispatcher.Start()
	t.Cleanup(func() { stopDispatcher(t, dispatcher) })
	waitFor(t, 2*time.Second, func() bool {
		row, ok := store.delivery(created.ID)
		return ok && row.State == domain.WebhookDeliveryDelivered
	})
}

func TestDispatcher_StopBlocksNewAttempts(t *testing.T) {
	store := newFakeStore()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	hook := domain.Webhook{ID: 1001, URL: server.URL, Secret: "s", IsActive: true}
	store.addHook(hook)

	dispatcher := NewDispatcher(store, loopbackClient(), testNode(t), testDispatcherConfig())
	dispatcher.Start()
	stopDispatcher(t, dispatcher)
	stopDispatcher(t, dispatcher)

	created, err := dispatcher.Enqueue(context.Background(), hook, domain.WebhookEventPush, []byte(`{}`))
	require.NoError(t, err)
	dispatcher.Start()

	time.Sleep(20 * time.Millisecond)
	require.Zero(t, hits.Load())
	row, _ := store.delivery(created.ID)
	require.Equal(t, domain.WebhookDeliveryPending, row.State)
}

func TestDispatcher_BackoffSchedule(t *testing.T) {
	dispatcher := NewDispatcher(nil, nil, nil, Config{})
	require.Equal(t, 10*time.Second, dispatcher.backoff(1))
	require.Equal(t, 40*time.Second, dispatcher.backoff(2))
	require.Equal(t, 160*time.Second, dispatcher.backoff(3))
	require.Equal(t, 640*time.Second, dispatcher.backoff(4))
	require.Equal(t, 5, dispatcher.cfg.MaxAttempts)
	require.Equal(t, 10*time.Second, dispatcher.cfg.SweepInterval)
	require.Equal(t, int64(100), dispatcher.cfg.RetryBatch)
	require.Equal(t, defaultWorkers, dispatcher.cfg.Workers)
}
