package daemon

import (
	"log/slog"
	"sync"
	"time"
)

const defaultPruneQuiet = 30 * time.Second

// cachePruner drops the content GUI reads downloaded into the object cache
// once the root has been quiet for a while. Diff, blob and preview calls
// request it, every admission postpones it, and it only runs when it can take
// the coordinator's exclusive slot without waiting.
type cachePruner struct {
	root  string
	coord *coordinator
	quiet time.Duration
	prune func() (bool, error)

	mu      sync.Mutex
	timer   *time.Timer
	pending bool
	closed  bool
	running sync.WaitGroup
}

func newCachePruner(root string, coord *coordinator, quiet time.Duration, prune func() (bool, error)) *cachePruner {
	if quiet <= 0 {
		quiet = defaultPruneQuiet
	}
	return &cachePruner{root: root, coord: coord, quiet: quiet, prune: prune}
}

func (p *cachePruner) request() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.pending = true
	p.armLocked()
}

func (p *cachePruner) touch() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending && !p.closed {
		p.armLocked()
	}
}

func (p *cachePruner) armLocked() {
	if p.timer == nil {
		p.timer = time.AfterFunc(p.quiet, p.fire)
		return
	}
	p.timer.Reset(p.quiet)
}

func (p *cachePruner) fire() {
	release, ok := p.coord.tryAcquireExclusive()
	if !ok {
		p.touch()
		return
	}
	defer release()
	p.mu.Lock()
	if p.closed || !p.pending {
		p.mu.Unlock()
		return
	}
	p.pending = false
	p.running.Add(1)
	p.mu.Unlock()
	defer p.running.Done()
	skipped, err := p.prune()
	if err != nil {
		slog.Warn("daemon cache prune failed", "root", p.root, "error", err)
	}
	if skipped {
		p.request()
	}
}

// settle drops a pending request after a manual prune covered it.
func (p *cachePruner) settle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pending = false
	if p.timer != nil {
		p.timer.Stop()
	}
}

// close cancels a scheduled prune and waits for a running one.
func (p *cachePruner) close() {
	p.mu.Lock()
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
	p.mu.Unlock()
	p.running.Wait()
}
