package daemon

import (
	"context"
	"log/slog"
)

// reconciler drains watcher batches and refreshes the stat cache in the
// background. It runs under the shared coordinator slot so it can never race
// an exclusive operation, and every failure is logged and dropped: the
// accelerator must never break status.
type reconciler struct {
	root   string
	rp     *repo
	cancel context.CancelFunc
	done   chan struct{}
}

func startReconciler(rp *repo, w *watcher) *reconciler {
	ctx, cancel := context.WithCancel(context.Background())
	r := &reconciler{root: rp.root, rp: rp, cancel: cancel, done: make(chan struct{})}
	go r.run(ctx, w)
	return r
}

func (r *reconciler) run(ctx context.Context, w *watcher) {
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return
		case paths, ok := <-w.updates():
			if !ok {
				return
			}
			r.refresh(ctx, paths)
		case err, ok := <-w.errors():
			if !ok {
				return
			}
			slog.Warn("daemon file watcher error", "root", r.root, "error", err)
		}
	}
}

func (r *reconciler) refresh(ctx context.Context, paths []string) {
	release, _, err := r.rp.coord.Acquire(ctx, false)
	if err != nil {
		return
	}
	defer release()
	if err := r.rp.wc.RefreshStatEntries(paths); err != nil {
		slog.Warn("daemon stat refresh failed", "root", r.root, "error", err)
	}
}

// stop cancels a queued refresh and waits for the loop to exit.
func (r *reconciler) stop() {
	r.cancel()
	<-r.done
}
