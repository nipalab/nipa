package projectpurge

import (
	"context"
	"log/slog"
	"time"

	"github.com/nipalab/nipa/internal/dispatch"
)

const (
	// DefaultInterval is how often the job looks for expired projects.
	DefaultInterval = time.Hour
	runTimeout      = 30 * time.Minute
)

type purgeRunner interface {
	PurgeExpired(ctx context.Context) ([]Stats, error)
}

// Job runs the purge in the background: once shortly after start, then every
// interval. It skips runs while paused (a frozen database).
type Job struct {
	purger   purgeRunner
	interval time.Duration
	delay    time.Duration
	pause    dispatch.Pause
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewJob builds the background job; a non-positive interval uses
// DefaultInterval.
func NewJob(purger purgeRunner, interval time.Duration) *Job {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Job{purger: purger, interval: interval, delay: time.Minute}
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
	purged, err := j.purger.PurgeExpired(ctx)
	if err != nil && ctx.Err() == nil {
		slog.Warn("project purge run failed", "err", err)
	}
	if len(purged) > 0 {
		slog.Info("project purge run finished", "projects", len(purged))
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
