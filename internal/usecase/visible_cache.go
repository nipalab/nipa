package usecase

import (
	"container/list"
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const visibleChunksTTL = 2 * time.Minute

// DefaultVisibleChunksCacheHashes bounds the chunk hashes the download
// visibility cache keeps per server process (32 bytes each).
const DefaultVisibleChunksCacheHashes = 2_000_000

type visibleEntry struct {
	key    string
	done   chan struct{}
	hashes []domain.Hash
	err    error
	used   time.Time
	elem   *list.Element
	weight int
}

// visibleChunkCache keeps recent VisibleChunks answers so the download
// windows of one clone walk the commit trees once. Entries expire ttl after
// their last use and the total number of cached hashes is bounded.
type visibleChunkCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	maxHashes int
	now       func() time.Time
	entries   map[string]*visibleEntry
	lru       *list.List
	size      int
}

func newVisibleChunkCache(ttl time.Duration, maxHashes int, now func() time.Time) *visibleChunkCache {
	return &visibleChunkCache{
		ttl:       ttl,
		maxHashes: maxHashes,
		now:       now,
		entries:   map[string]*visibleEntry{},
		lru:       list.New(),
	}
}

func visibleChunksKey(claim domain.Claims, projectID snow.ID, commitIDs, paths []string) string {
	commits := slices.Clone(commitIDs)
	slices.Sort(commits)
	commits = slices.Compact(commits)
	prefixes := slices.Clone(paths)
	slices.Sort(prefixes)
	var b strings.Builder
	b.WriteString(strconv.FormatInt(claim.UserID.Int64(), 10))
	b.WriteString("|")
	b.WriteString(strconv.FormatBool(claim.IsAdmin))
	b.WriteString(strconv.FormatBool(claim.IsSuperAdmin))
	b.WriteString("|")
	b.WriteString(strconv.FormatInt(projectID.Int64(), 10))
	b.WriteString("|")
	b.WriteString(strings.Join(commits, ","))
	b.WriteString("|")
	b.WriteString(strings.Join(prefixes, "\x00"))
	return b.String()
}

// get returns the cached answer for key, computing it once when missing.
// Concurrent callers of a missing key wait for the same computation; a failed
// computation is not kept.
func (c *visibleChunkCache) get(ctx context.Context, key string, compute func(context.Context) ([]domain.Hash, error)) ([]domain.Hash, error) {
	c.mu.Lock()
	now := c.now()
	if e, ok := c.entries[key]; ok {
		if e.elem == nil || now.Sub(e.used) < c.ttl {
			if e.elem != nil {
				e.used = now
				c.lru.MoveToFront(e.elem)
			}
			c.mu.Unlock()
			return e.wait(ctx)
		}
		c.remove(e)
	}
	e := &visibleEntry{key: key, done: make(chan struct{})}
	c.entries[key] = e
	c.mu.Unlock()

	e.hashes, e.err = compute(context.WithoutCancel(ctx))
	e.weight = len(e.hashes) + 1 + len(key)/32

	c.mu.Lock()
	switch {
	case e.err != nil || e.weight > c.maxHashes:
		delete(c.entries, key)
	default:
		e.used = c.now()
		e.elem = c.lru.PushFront(e)
		c.size += e.weight
		c.evict()
	}
	c.mu.Unlock()
	close(e.done)
	return e.wait(ctx)
}

func (e *visibleEntry) wait(ctx context.Context) ([]domain.Hash, error) {
	select {
	case <-e.done:
		return e.hashes, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *visibleChunkCache) evict() {
	now := c.now()
	for back := c.lru.Back(); back != nil; back = c.lru.Back() {
		e := back.Value.(*visibleEntry)
		if c.size <= c.maxHashes && now.Sub(e.used) < c.ttl {
			return
		}
		c.remove(e)
	}
}

func (c *visibleChunkCache) remove(e *visibleEntry) {
	delete(c.entries, e.key)
	if e.elem != nil {
		c.lru.Remove(e.elem)
		c.size -= e.weight
		e.elem = nil
	}
}
