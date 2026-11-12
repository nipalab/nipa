package daemon

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/metadata"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

// maxChangePaths bounds what one subscriber buffers; past it the pending
// notice collapses into a refresh-everything one (no paths).
const maxChangePaths = 512

// changeHub fans working-copy change notices out to WatchChanges streams.
// Publishing never blocks: each subscriber coalesces notices until it reads.
type changeHub struct {
	gen    atomic.Uint64
	mu     sync.Mutex
	subs   map[*changeSub]struct{}
	closed bool
}

type changeSub struct {
	signal chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	paths   map[string]struct{}
	all     bool
	pending bool
}

func newChangeHub() *changeHub {
	return &changeHub{subs: map[*changeSub]struct{}{}}
}

func (h *changeHub) subscribe() (*changeSub, func()) {
	sub := &changeSub{signal: make(chan struct{}, 1), done: make(chan struct{}), paths: map[string]struct{}{}}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(sub.done)
		return sub, func() {}
	}
	h.subs[sub] = struct{}{}
	return sub, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[sub]; ok {
			delete(h.subs, sub)
			close(sub.done)
		}
	}
}

// publish records a change; nil or empty paths mean "refresh everything".
func (h *changeHub) publish(paths []string) {
	h.gen.Add(1)
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		sub.add(paths)
	}
}

// generation counts published notices, so cached answers know they are stale.
func (h *changeHub) generation() uint64 {
	return h.gen.Load()
}

func (h *changeHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for sub := range h.subs {
		delete(h.subs, sub)
		close(sub.done)
	}
}

func (s *changeSub) add(paths []string) {
	s.mu.Lock()
	s.pending = true
	if len(paths) == 0 {
		s.all = true
	}
	for _, p := range paths {
		if s.all {
			break
		}
		s.paths[p] = struct{}{}
		if len(s.paths) > maxChangePaths {
			s.all = true
		}
	}
	if s.all {
		s.paths = map[string]struct{}{}
	}
	s.mu.Unlock()
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

func (s *changeSub) take() *daemonpb.RepoChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pending {
		return nil
	}
	change := &daemonpb.RepoChange{}
	if !s.all {
		for p := range s.paths {
			change.Paths = append(change.Paths, p)
		}
		sort.Strings(change.Paths)
	}
	s.paths = map[string]struct{}{}
	s.all = false
	s.pending = false
	return change
}

// WatchChanges streams a notice whenever the watched clone may have changed:
// watcher batches (after the stat cache refresh), finished operations and
// stage/restore calls. Headers are sent once subscribed, so a client that
// waits for them knows no later change is missed. The stream ends when the
// repo is unwatched or the daemon stops. It never pins the repo, so it cannot
// block UnwatchRepo.
func (s *Server) WatchChanges(req *daemonpb.WatchChangesRequest, stream daemonpb.NipaDaemon_WatchChangesServer) error {
	rp, unref, err := s.repos.ref(req.GetRoot())
	if err != nil {
		return toStatusError(err)
	}
	sub, unsubscribe := rp.changes.subscribe()
	unref()
	defer unsubscribe()
	if err := stream.SendHeader(metadata.MD{}); err != nil {
		return err
	}
	return pumpChanges(stream.Context(), s.stop, sub, stream.Send)
}

func pumpChanges(ctx context.Context, stop <-chan struct{}, sub *changeSub, send func(*daemonpb.RepoChange) error) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-stop:
			return nil
		case <-sub.done:
			return nil
		case <-sub.signal:
			if change := sub.take(); change != nil {
				if err := send(change); err != nil {
					return err
				}
			}
		}
	}
}
