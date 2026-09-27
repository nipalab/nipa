package webhook

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	defaultWorkers    = 4
	deliveryRetention = 1000
)

// Config tunes delivery retries. Zero values fall back to the defaults.
type Config struct {
	// MaxAttempts is the total number of tries per delivery, including the first.
	MaxAttempts int
	// InitialBackoff is the delay before the second attempt.
	InitialBackoff time.Duration
	// BackoffFactor multiplies the delay after every failed attempt.
	BackoffFactor int
	// SweepInterval is how often persisted pending deliveries are picked up.
	SweepInterval time.Duration
	// RetryBatch caps one sweep.
	RetryBatch int64
	// Workers is the number of concurrent delivery goroutines.
	Workers int
}

// DispatcherStore is the slice of the webhook repository the dispatcher needs.
// Deliveries are persisted before the first attempt so a crash cannot lose
// them; the sweep resumes whatever is still pending.
type DispatcherStore interface {
	GetByID(ctx context.Context, id snow.ID) (*domain.Webhook, error)
	CreateDelivery(ctx context.Context, delivery domain.WebhookDelivery) (*domain.WebhookDelivery, error)
	ListPendingRetries(ctx context.Context, now time.Time, limit int64) ([]*domain.WebhookDelivery, error)
	MarkDelivered(ctx context.Context, id snow.ID, attempt int64, responseStatus *int64, at time.Time) (*domain.WebhookDelivery, error)
	Reschedule(ctx context.Context, id snow.ID, attempt int64, responseStatus *int64, lastError string, nextRetryAt time.Time) (*domain.WebhookDelivery, error)
	MarkFailed(ctx context.Context, id snow.ID, attempt int64, responseStatus *int64, lastError string) (*domain.WebhookDelivery, error)
	DeleteOldDeliveries(ctx context.Context, webhookID snow.ID, keep int64) (int64, error)
}

type Dispatcher struct {
	store  DispatcherStore
	client *Client
	node   snow.Node
	cfg    Config

	jobs chan job
	stop chan struct{}
	done chan struct{}

	mu       sync.Mutex
	workers  sync.WaitGroup
	inFlight map[snow.ID]struct{}
	started  bool
	stopped  bool
}

type job struct {
	hook     domain.Webhook
	delivery domain.WebhookDelivery
}

func NewDispatcher(store DispatcherStore, client *Client, node snow.Node, cfg Config) *Dispatcher {
	return &Dispatcher{
		store:    store,
		client:   client,
		node:     node,
		cfg:      cfg.withDefaults(),
		jobs:     make(chan job, 64),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
		inFlight: map[snow.ID]struct{}{},
	}
}

// Start launches the workers and the retry sweeper. It is a no-op when the
// dispatcher is already running or has been stopped.
func (d *Dispatcher) Start() {
	d.mu.Lock()
	if d.started || d.stopped {
		d.mu.Unlock()
		return
	}
	d.started = true
	d.mu.Unlock()

	for i := 0; i < d.cfg.Workers; i++ {
		d.workers.Add(1)
		go d.worker()
	}
	go d.run()
}

// Stop stops scheduling new attempts and waits for the in-flight ones to
// finish. Pending deliveries stay in the store and are resumed by the sweep
// on the next Start.
func (d *Dispatcher) Stop(ctx context.Context) error {
	d.mu.Lock()
	started := d.started
	d.started = false
	d.stopped = true
	if started {
		close(d.stop)
	}
	d.mu.Unlock()
	if !started {
		return nil
	}

	select {
	case <-d.done:
	case <-ctx.Done():
		return ctx.Err()
	}

	finished := make(chan struct{})
	go func() {
		d.workers.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Enqueue persists a pending delivery and schedules an immediate attempt. The
// call never blocks on the network: if the queue is full the sweep picks the
// row up later.
func (d *Dispatcher) Enqueue(ctx context.Context, hook domain.Webhook, event string, payload []byte) (*domain.WebhookDelivery, error) {
	now := time.Now()
	delivery, err := d.store.CreateDelivery(ctx, domain.WebhookDelivery{
		ID:          d.node.Generate(),
		WebhookID:   hook.ID,
		EventType:   event,
		Payload:     payload,
		State:       domain.WebhookDeliveryPending,
		NextRetryAt: &now,
	})
	if err != nil {
		return nil, err
	}
	d.Schedule(hook, *delivery)
	return delivery, nil
}

// Schedule queues an already persisted delivery for an immediate attempt.
func (d *Dispatcher) Schedule(hook domain.Webhook, delivery domain.WebhookDelivery) {
	d.mu.Lock()
	running := d.started && !d.stopped
	d.mu.Unlock()
	if !running {
		return
	}
	if !d.claim(delivery.ID) {
		return
	}
	select {
	case d.jobs <- job{hook: hook, delivery: delivery}:
	default:
		d.release(delivery.ID)
	}
}

func (d *Dispatcher) run() {
	defer close(d.done)

	d.sweep()

	ticker := time.NewTicker(d.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		default:
		}
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.sweep()
		}
	}
}

func (d *Dispatcher) worker() {
	defer d.workers.Done()
	for {
		select {
		case <-d.stop:
			return
		default:
		}
		select {
		case <-d.stop:
			return
		case j := <-d.jobs:
			d.deliver(j)
			d.release(j.delivery.ID)
		}
	}
}

func (d *Dispatcher) sweep() {
	rows, err := d.store.ListPendingRetries(context.Background(), time.Now(), d.cfg.RetryBatch)
	if err != nil {
		slog.Warn("webhook retry sweep failed", "error", err)
		return
	}
	for _, row := range rows {
		hook, err := d.store.GetByID(context.Background(), row.WebhookID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				d.markFailed(*row, row.Attempt, nil, "webhook no longer exists")
				continue
			}
			slog.Warn("webhook retry hook lookup failed", "webhook_id", row.WebhookID, "error", err)
			continue
		}
		d.Schedule(*hook, *row)
	}
}

func (d *Dispatcher) deliver(j job) {
	attempt := j.delivery.Attempt + 1
	result := d.client.Post(context.Background(), PostRequest{
		URL:         j.hook.URL,
		Secret:      j.hook.Secret,
		InsecureTLS: j.hook.InsecureTLS,
		Event:       j.delivery.EventType,
		DeliveryID:  j.delivery.ID.Base36(),
		HookID:      j.hook.ID.Base36(),
		Payload:     j.delivery.Payload,
	})

	ctx := context.Background()
	if result.Err == nil {
		if _, err := d.store.MarkDelivered(ctx, j.delivery.ID, attempt, errorStatus(result.StatusCode), time.Now()); err != nil {
			slog.Warn("webhook delivery state update failed", "delivery_id", j.delivery.ID, "error", err)
		}
		d.trim(j.hook.ID)
		return
	}

	status := errorStatus(result.StatusCode)
	if IsEgressBlocked(result.Err) || attempt >= int64(d.cfg.MaxAttempts) {
		d.markFailed(j.delivery, attempt, status, result.Err.Error())
		return
	}
	next := time.Now().Add(d.backoff(attempt))
	if _, err := d.store.Reschedule(ctx, j.delivery.ID, attempt, status, result.Err.Error(), next); err != nil {
		slog.Warn("webhook delivery reschedule failed", "delivery_id", j.delivery.ID, "error", err)
	}
	slog.Warn("webhook delivery failed; scheduled a retry",
		"delivery_id", j.delivery.ID, "hook_id", j.hook.ID, "attempt", attempt, "next_retry_at", next, "error", result.Err)
}

func (d *Dispatcher) markFailed(delivery domain.WebhookDelivery, attempt int64, status *int64, reason string) {
	if _, err := d.store.MarkFailed(context.Background(), delivery.ID, attempt, status, reason); err != nil {
		slog.Warn("webhook delivery state update failed", "delivery_id", delivery.ID, "error", err)
	}
	d.trim(delivery.WebhookID)
}

func (d *Dispatcher) trim(webhookID snow.ID) {
	if _, err := d.store.DeleteOldDeliveries(context.Background(), webhookID, deliveryRetention); err != nil {
		slog.Warn("webhook delivery log trim failed", "webhook_id", webhookID, "error", err)
	}
}

func (d *Dispatcher) backoff(attempt int64) time.Duration {
	delay := d.cfg.InitialBackoff
	for i := int64(1); i < attempt; i++ {
		delay *= time.Duration(d.cfg.BackoffFactor)
	}
	return delay
}

func (d *Dispatcher) claim(id snow.ID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.inFlight[id]; ok {
		return false
	}
	d.inFlight[id] = struct{}{}
	return true
}

func (d *Dispatcher) release(id snow.ID) {
	d.mu.Lock()
	delete(d.inFlight, id)
	d.mu.Unlock()
}

func (cfg Config) withDefaults() Config {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 10 * time.Second
	}
	if cfg.BackoffFactor < 2 {
		cfg.BackoffFactor = 4
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = 10 * time.Second
	}
	if cfg.RetryBatch <= 0 {
		cfg.RetryBatch = 100
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultWorkers
	}
	return cfg
}

func errorStatus(statusCode int) *int64 {
	if statusCode == 0 {
		return nil
	}
	status := int64(statusCode)
	return &status
}
