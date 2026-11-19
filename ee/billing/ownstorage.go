package billing

import (
	"context"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/snow"
)

const (
	ownStorageTTL      = time.Minute
	ownStorageMaxCache = 10000
)

type ownStorageEntry struct {
	active  bool
	expires time.Time
}

type ownStorageCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[snow.ID]ownStorageEntry
	gen     uint64
}

func newOwnStorageCache(now func() time.Time) *ownStorageCache {
	return &ownStorageCache{now: now, entries: map[snow.ID]ownStorageEntry{}}
}

func (c *ownStorageCache) get(owner snow.ID) (bool, bool, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[owner]
	if !ok || !c.now().Before(entry.expires) {
		return false, false, c.gen
	}
	return entry.active, true, c.gen
}

func (c *ownStorageCache) put(owner snow.ID, active bool, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return
	}
	now := c.now()
	if len(c.entries) >= ownStorageMaxCache {
		for id, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, id)
			}
		}
	}
	c.entries[owner] = ownStorageEntry{active: active, expires: now.Add(ownStorageTTL)}
}

func (c *ownStorageCache) forget(owner snow.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	delete(c.entries, owner)
}

// OwnStorageActive reports whether the add-on is offered and the owner's live
// subscription carries it. Answers are cached per owner for a minute; applying a
// subscription forgets the owner's entry.
func (s *Service) OwnStorageActive(ctx context.Context, ownerID snow.ID) (bool, error) {
	if !s.cfg.OwnStorage {
		return false, nil
	}
	active, ok, gen := s.own.get(ownerID)
	if ok {
		return active, nil
	}
	live, err := s.repo.LiveSubscription(ctx, ownerID)
	if err != nil {
		return false, err
	}
	active = live != nil && IsLive(live.Status) && live.OwnStorage
	s.own.put(ownerID, active, gen)
	return active, nil
}

func (s *Service) forgetOwnStorage(owner snow.ID) {
	s.own.forget(owner)
}
