// Package dispatch provides the shared lifecycle scaffolding for background
// delivery loops: guarded start/stop, a stop signal for loops and workers, and
// a drain wait on stop.
package dispatch

import (
	"context"
	"sync"
)

// Runner manages the started/stopped state of a background dispatcher, the
// stop channel its loops select on and the drain wait for in-flight work.
type Runner struct {
	mu      sync.Mutex
	stop    chan struct{}
	done    chan struct{}
	workers sync.WaitGroup
	started bool
	stopped bool
}

func NewRunner() *Runner {
	return &Runner{stop: make(chan struct{}), done: make(chan struct{})}
}

// Start launches one goroutine per worker plus the loop. It is a no-op when
// the runner is already running or has been stopped.
func (r *Runner) Start(workers int, worker, loop func()) {
	r.mu.Lock()
	if r.started || r.stopped {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()

	for i := 0; i < workers; i++ {
		r.workers.Add(1)
		go func() {
			defer r.workers.Done()
			worker()
		}()
	}
	go func() {
		defer close(r.done)
		loop()
	}()
}

// Stop closes the stop signal and waits for the loop and the in-flight workers
// to finish. It is a no-op when the runner never started.
func (r *Runner) Stop(ctx context.Context) error {
	r.mu.Lock()
	started := r.started
	r.started = false
	r.stopped = true
	if started {
		close(r.stop)
	}
	r.mu.Unlock()
	if !started {
		return nil
	}

	select {
	case <-r.done:
	case <-ctx.Done():
		return ctx.Err()
	}

	finished := make(chan struct{})
	go func() {
		r.workers.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Running reports whether the runner is started and not yet stopped.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started && !r.stopped
}

// StopCh is closed when the runner stops; loops and workers select on it.
func (r *Runner) StopCh() <-chan struct{} {
	return r.stop
}
