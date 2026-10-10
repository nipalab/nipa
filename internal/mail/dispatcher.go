package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/internal/dispatch"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	defaultEmailWorkers   = 4
	defaultPollInterval   = 10 * time.Second
	defaultBatchSize      = 100
	defaultStaleAfter     = 5 * time.Minute
	defaultRetention      = 30 * 24 * time.Hour
	defaultRetentionSweep = time.Hour
	defaultInitialBackoff = 10 * time.Second
	defaultBackoffFactor  = 4
	defaultMaxAttempts    = 5
)

// DispatcherConfig tunes outbox delivery. Zero values fall back to defaults.
type DispatcherConfig struct {
	// Workers is the number of concurrent delivery goroutines.
	Workers int
	// PollInterval is how often the outbox is polled for due deliveries.
	PollInterval time.Duration
	// BatchSize caps one claim.
	BatchSize int64
	// MaxAttempts is the total number of tries per delivery, including the first.
	MaxAttempts int
	// InitialBackoff is the delay before the second attempt.
	InitialBackoff time.Duration
	// BackoffFactor multiplies the delay after every failed attempt.
	BackoffFactor int
	// StaleAfter reclaims sending rows older than this (a crashed attempt).
	StaleAfter time.Duration
	// Retention is how long delivered and failed rows are kept.
	Retention time.Duration
	// RetentionSweepInterval is how often expired rows are deleted.
	RetentionSweepInterval time.Duration
}

// DispatcherStore is the slice of the email repository the dispatcher needs.
// Deliveries are persisted before the first attempt so a crash cannot lose
// them; stale sending rows are reclaimed on the next claim.
type DispatcherStore interface {
	ClaimDue(ctx context.Context, now, staleBefore time.Time, limit int64) ([]*domain.EmailDelivery, error)
	MarkDelivered(ctx context.Context, id snow.ID, at time.Time) error
	ScheduleRetry(ctx context.Context, id snow.ID, lastError string, next time.Time) error
	MarkFailed(ctx context.Context, id snow.ID, lastError string) error
	SweepRetention(ctx context.Context, before time.Time) (int64, error)
}

// Dispatcher delivers queued outbox rows through the configured sender. It
// polls the store for due rows, claims them atomically and retries failures
// with exponential backoff until MaxAttempts.
type Dispatcher struct {
	store  DispatcherStore
	sender Sender
	cfg    DispatcherConfig

	runner *dispatch.Runner
	jobs   chan domain.EmailDelivery
	kick   chan struct{}
}

func NewDispatcher(store DispatcherStore, sender Sender, cfg DispatcherConfig) *Dispatcher {
	return &Dispatcher{
		store:  store,
		sender: sender,
		cfg:    cfg.withDefaults(),
		runner: dispatch.NewRunner(),
		jobs:   make(chan domain.EmailDelivery, 64),
		kick:   make(chan struct{}, 1),
	}
}

// Start launches the workers and the poll loop. It is a no-op when the
// dispatcher is already running or has been stopped.
func (d *Dispatcher) Start() {
	d.runner.Start(d.cfg.Workers, d.worker, d.run)
}

// Stop stops claiming new rows and waits for in-flight deliveries to finish.
// Claimed-but-undelivered rows are reclaimed by the next Start.
func (d *Dispatcher) Stop(ctx context.Context) error {
	return d.runner.Stop(ctx)
}

// Kick asks the dispatcher to poll immediately instead of waiting for the next
// interval. It never blocks and is safe to call while stopped.
func (d *Dispatcher) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) run() {
	d.sweep()

	poll := time.NewTicker(d.cfg.PollInterval)
	defer poll.Stop()
	retention := time.NewTicker(d.cfg.RetentionSweepInterval)
	defer retention.Stop()
	for {
		select {
		case <-d.runner.StopCh():
			return
		case <-poll.C:
			d.sweep()
		case <-d.kick:
			d.sweep()
		case <-retention.C:
			d.trim()
		}
	}
}

func (d *Dispatcher) worker() {
	for {
		select {
		case <-d.runner.StopCh():
			return
		default:
		}
		select {
		case <-d.runner.StopCh():
			return
		case delivery := <-d.jobs:
			d.deliver(delivery)
		}
	}
}

func (d *Dispatcher) sweep() {
	now := time.Now()
	rows, err := d.store.ClaimDue(context.Background(), now, now.Add(-d.cfg.StaleAfter), d.cfg.BatchSize)
	if err != nil {
		slog.Warn("email outbox sweep failed", "error", err)
		return
	}
	for _, row := range rows {
		select {
		case d.jobs <- *row:
		case <-d.runner.StopCh():
			return
		}
	}
}

func (d *Dispatcher) deliver(delivery domain.EmailDelivery) {
	ctx := context.Background()
	msg, err := DecodeMessage(delivery.Body)
	if err != nil {
		d.markFailed(delivery, fmt.Errorf("decode stored message: %w", err))
		return
	}
	if err := d.sender.Send(ctx, msg); err != nil {
		if delivery.Attempts >= int64(d.cfg.MaxAttempts) {
			d.markFailed(delivery, err)
			return
		}
		next := time.Now().Add(d.backoff(delivery.Attempts))
		if err := d.store.ScheduleRetry(ctx, delivery.ID, err.Error(), next); err != nil {
			slog.Warn("email delivery reschedule failed", "delivery_id", delivery.ID, "error", err)
		}
		slog.Warn("email delivery failed; scheduled a retry",
			"delivery_id", delivery.ID, "attempt", delivery.Attempts, "next_retry_at", next, "error", err)
		return
	}
	if err := d.store.MarkDelivered(ctx, delivery.ID, time.Now()); err != nil {
		slog.Warn("email delivery state update failed", "delivery_id", delivery.ID, "error", err)
	}
}

func (d *Dispatcher) markFailed(delivery domain.EmailDelivery, cause error) {
	if err := d.store.MarkFailed(context.Background(), delivery.ID, cause.Error()); err != nil {
		slog.Warn("email delivery state update failed", "delivery_id", delivery.ID, "error", err)
	}
}

func (d *Dispatcher) trim() {
	if _, err := d.store.SweepRetention(context.Background(), time.Now().Add(-d.cfg.Retention)); err != nil {
		slog.Warn("email delivery retention sweep failed", "error", err)
	}
}

func (d *Dispatcher) backoff(attempt int64) time.Duration {
	delay := d.cfg.InitialBackoff
	for i := int64(1); i < attempt; i++ {
		delay *= time.Duration(d.cfg.BackoffFactor)
	}
	return delay
}

// EncodeMessage renders a message for outbox storage.
func EncodeMessage(msg Message) ([]byte, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("encode email message: %w", err)
	}
	return body, nil
}

// DecodeMessage parses an outbox-stored message.
func DecodeMessage(body []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return Message{}, fmt.Errorf("decode email message: %w", err)
	}
	return msg, nil
}

func (cfg DispatcherConfig) withDefaults() DispatcherConfig {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultEmailWorkers
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = defaultInitialBackoff
	}
	if cfg.BackoffFactor < 2 {
		cfg.BackoffFactor = defaultBackoffFactor
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = defaultStaleAfter
	}
	if cfg.Retention <= 0 {
		cfg.Retention = defaultRetention
	}
	if cfg.RetentionSweepInterval <= 0 {
		cfg.RetentionSweepInterval = defaultRetentionSweep
	}
	return cfg
}
