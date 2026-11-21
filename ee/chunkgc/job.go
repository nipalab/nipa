package chunkgc

import (
	"context"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/internal/dispatch"
)

const (
	// DefaultInterval is how often the collector runs.
	DefaultInterval = 6 * time.Hour
	runTimeout      = 2 * time.Hour
)

type runner interface {
	Run(ctx context.Context) (Report, error)
}

// Job runs the collector in the background: once a few minutes after start,
// then every interval. It skips runs while paused (a frozen database).
type Job struct {
	collector runner
	interval  time.Duration
	delay     time.Duration
	pause     dispatch.Pause
	cancel    context.CancelFunc
	done      chan struct{}
}

// NewJob builds the background job; a non-positive interval uses
// DefaultInterval.
func NewJob(collector runner, interval time.Duration) *Job {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Job{collector: collector, interval: interval, delay: 5 * time.Minute}
}

// SetPause skips runs while paused reports true.
func (j *Job) SetPause(paused func() bool) {
	j.pause = paused
}

// Start launches the loop.
func (j *Job) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.done = make(chan struct{})
	go func() {
		defer close(j.done)
		timer := time.NewTimer(j.delay)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if !j.pause.Active() {
					j.run(ctx)
				}
				timer.Reset(j.interval)
			}
		}
	}()
}

func (j *Job) run(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	if _, err := j.collector.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("chunk gc run failed", "err", err)
	}
}

// Stop cancels the loop and waits for an in-flight run, bounded by ctx.
func (j *Job) Stop(ctx context.Context) error {
	if j.cancel == nil {
		return nil
	}
	j.cancel()
	select {
	case <-j.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
