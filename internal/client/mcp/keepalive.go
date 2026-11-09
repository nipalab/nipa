package mcp

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const defaultAgentLockTTL = 10 * time.Minute

type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }

func newRealTicker(d time.Duration) ticker { return realTicker{t: time.NewTicker(d)} }

type keepAliveKey struct {
	root   string
	branch string
	path   string
}

type keepAliveEntry struct {
	cancel context.CancelFunc
}

// keepAlive renews the TTL locks this MCP server took every ttl/3, so a live
// agent keeps them while a crashed one frees them within one TTL.
type keepAlive struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	entries   map[keepAliveKey]*keepAliveEntry
	wg        sync.WaitGroup
	newTicker func(time.Duration) ticker
	renew     func(ctx context.Context, key keepAliveKey, ttl time.Duration) error
}

func newKeepAlive(newTicker func(time.Duration) ticker, renew func(context.Context, keepAliveKey, time.Duration) error) *keepAlive {
	if newTicker == nil {
		newTicker = newRealTicker
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &keepAlive{
		ctx:       ctx,
		cancel:    cancel,
		entries:   map[keepAliveKey]*keepAliveEntry{},
		newTicker: newTicker,
		renew:     renew,
	}
}

func (k *keepAlive) track(key keepAliveKey, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.ctx.Err() != nil {
		return
	}
	if old, ok := k.entries[key]; ok {
		old.cancel()
	}
	ctx, cancel := context.WithCancel(k.ctx)
	entry := &keepAliveEntry{cancel: cancel}
	k.entries[key] = entry
	t := k.newTicker(ttl / 3)
	k.wg.Add(1)
	go k.loop(ctx, key, entry, t, ttl)
}

func (k *keepAlive) loop(ctx context.Context, key keepAliveKey, entry *keepAliveEntry, t ticker, ttl time.Duration) {
	defer k.wg.Done()
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			if err := k.renew(ctx, key, ttl); err != nil {
				if ctx.Err() != nil {
					return
				}
				slog.Warn("lock keep-alive failed, no longer renewing", "path", key.path, "branch", key.branch, "error", err)
				k.remove(key, entry)
				return
			}
		}
	}
}

func (k *keepAlive) remove(key keepAliveKey, entry *keepAliveEntry) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if current, ok := k.entries[key]; ok && current == entry {
		delete(k.entries, key)
		current.cancel()
	}
}

func (k *keepAlive) untrack(key keepAliveKey) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if entry, ok := k.entries[key]; ok {
		delete(k.entries, key)
		entry.cancel()
	}
}

func (k *keepAlive) tracking(root string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for key := range k.entries {
		if key.root == root {
			return true
		}
	}
	return false
}

// retain stops renewing the root's locks whose path is no longer held, such
// as exact-path locks the server released after a push landed them.
func (k *keepAlive) retain(root string, held map[string]bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for key, entry := range k.entries {
		if key.root == root && !held[key.path] {
			delete(k.entries, key)
			entry.cancel()
		}
	}
}

func (k *keepAlive) keys() []keepAliveKey {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]keepAliveKey, 0, len(k.entries))
	for key := range k.entries {
		out = append(out, key)
	}
	return out
}

func (k *keepAlive) stop() {
	k.mu.Lock()
	k.cancel()
	k.entries = map[keepAliveKey]*keepAliveEntry{}
	k.mu.Unlock()
	k.wg.Wait()
}
