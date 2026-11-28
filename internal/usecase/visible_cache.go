package usecase

import (
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

const visibleChunksTTL = 2 * time.Minute

// DefaultVisibleChunksComputeTimeout bounds one visible-set walk
// (CHUNK_VISIBILITY_TIMEOUT_SECONDS overrides it).
const DefaultVisibleChunksComputeTimeout = 30 * time.Second

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
// their last use and the total number of cached hashes is bounded. The cache
// has no invalidation and needs none: an answer is a pure function of its key
// (see visibleChunksKey), so the idle ttl only bounds memory.
//
// An answer bigger than the whole bound cannot be kept, so every request for
// it walks the trees again. That is reported, not silent: the oversize
// lookup is counted and logged once per key and ttl, naming the size and the
// bound (CHUNK_VISIBILITY_CACHE_HASHES).
type visibleChunkCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	timeout   time.Duration
	maxHashes int
	now       func() time.Time
	entries   map[string]*visibleEntry
	lru       *list.List
	size      int
	// oversize remembers when an oversize answer was last reported, so the
	// warning is not repeated for every download window.
	oversize map[string]time.Time
	metrics  *obs.Metrics
	logger   *slog.Logger
}

// visibleRequest describes a lookup for the log lines of the cache.
type visibleRequest struct {
	projectID snow.ID
	commits   int
	paths     int
}

func newVisibleChunkCache(ttl time.Duration, maxHashes int, now func() time.Time) *visibleChunkCache {
	return &visibleChunkCache{
		ttl:       ttl,
		timeout:   DefaultVisibleChunksComputeTimeout,
		maxHashes: maxHashes,
		now:       now,
		entries:   map[string]*visibleEntry{},
		lru:       list.New(),
		oversize:  map[string]time.Time{},
	}
}

// setMaxHashes changes the bound and evicts down to it.
func (c *visibleChunkCache) setMaxHashes(n int) {
	c.mu.Lock()
	c.maxHashes = n
	c.evict()
	c.mu.Unlock()
}

func (c *visibleChunkCache) log() *slog.Logger {
	if c.logger != nil {
		return c.logger
	}
	return slog.Default()
}

// visibleChunksKey names everything a VisibleChunks answer depends on: the
// project, the commits, the sparse prefixes and the fingerprint of the read
// filter the trees are walked under (PathFilter.appendFingerprint, the
// encoding manifest view tokens use). It holds no caller identity: who asks
// does not matter, only which paths their rules let through, so the key
// changes with the rules and a stale answer cannot be reached. It reports
// false for a filter that has no fingerprint; such an answer must not be
// cached.
func visibleChunksKey(filter *PathFilter, projectID snow.ID, commitIDs, paths []string) (string, bool) {
	fingerprint, ok := filter.appendFingerprint(nil)
	if !ok {
		return "", false
	}
	commits := slices.Clone(commitIDs)
	slices.Sort(commits)
	commits = slices.Compact(commits)
	prefixes := slices.Clone(paths)
	slices.Sort(prefixes)
	// The fingerprint grows with the rule set; its digest keeps keys short.
	view := sha256.Sum256(fingerprint)
	var b strings.Builder
	b.Write(view[:])
	b.WriteString("|")
	b.WriteString(strconv.FormatInt(projectID.Int64(), 10))
	b.WriteString("|")
	b.WriteString(strings.Join(commits, ","))
	b.WriteString("|")
	b.WriteString(strings.Join(prefixes, "\x00"))
	return b.String(), true
}

// get returns the cached answer for key, computing it once when missing.
// Concurrent callers of a missing key wait for the same computation; a failed
// computation is not kept.
func (c *visibleChunkCache) get(ctx context.Context, key string, req visibleRequest, compute func(context.Context) ([]domain.Hash, error)) ([]domain.Hash, error) {
	c.mu.Lock()
	now := c.now()
	if e, ok := c.entries[key]; ok {
		if e.elem == nil || now.Sub(e.used) < c.ttl {
			if e.elem != nil {
				e.used = now
				c.lru.MoveToFront(e.elem)
			}
			c.mu.Unlock()
			c.metrics.ObserveVisibleChunksLookup("hit")
			return e.wait(ctx)
		}
		c.remove(e)
	}
	e := &visibleEntry{key: key, done: make(chan struct{})}
	c.entries[key] = e
	timeout := c.timeout
	c.mu.Unlock()

	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	started := time.Now()
	e.hashes, e.err = compute(cctx)
	elapsed := time.Since(started)
	timedOut := e.err != nil && errors.Is(cctx.Err(), context.DeadlineExceeded)
	cancel()
	e.weight = len(e.hashes) + 1 + len(key)/32
	c.observeCompute(req, e.err, timedOut, elapsed, timeout)

	c.mu.Lock()
	var warnOversize bool
	switch {
	case e.err != nil:
		delete(c.entries, key)
	case e.weight > c.maxHashes:
		delete(c.entries, key)
		warnOversize = c.markOversize(key)
	default:
		e.used = c.now()
		e.elem = c.lru.PushFront(e)
		c.size += e.weight
		c.evict()
	}
	maxHashes := c.maxHashes
	c.mu.Unlock()
	close(e.done)

	switch {
	case e.err != nil:
		c.metrics.ObserveVisibleChunksLookup("error")
	case e.weight > maxHashes:
		c.metrics.ObserveVisibleChunksLookup("oversize")
		if warnOversize {
			c.log().Warn("visible chunk set is larger than the whole visibility cache; every download window recomputes it",
				"project_id", req.projectID.Base36(), "commits", req.commits, "paths", req.paths,
				"hashes", len(e.hashes), "cache_hashes", maxHashes, "compute", elapsed,
				"hint", "raise CHUNK_VISIBILITY_CACHE_HASHES")
		}
	default:
		c.metrics.ObserveVisibleChunksLookup("miss")
	}
	return e.wait(ctx)
}

func (c *visibleChunkCache) observeCompute(req visibleRequest, err error, timedOut bool, elapsed, timeout time.Duration) {
	switch {
	case timedOut:
		c.metrics.ObserveVisibleChunksCompute("timeout", elapsed)
		c.log().Warn("visible chunk set computation timed out",
			"project_id", req.projectID.Base36(), "commits", req.commits, "paths", req.paths,
			"timeout", timeout, "hint", "raise CHUNK_VISIBILITY_TIMEOUT_SECONDS")
	case err != nil:
		c.metrics.ObserveVisibleChunksCompute("error", elapsed)
	default:
		c.metrics.ObserveVisibleChunksCompute("ok", elapsed)
	}
}

// markOversize reports whether an oversize answer for key should be logged:
// once per key and ttl. Expired marks are dropped, so the map stays small.
// The caller holds c.mu.
func (c *visibleChunkCache) markOversize(key string) bool {
	now := c.now()
	for k, at := range c.oversize {
		if now.Sub(at) >= c.ttl {
			delete(c.oversize, k)
		}
	}
	if _, ok := c.oversize[key]; ok {
		return false
	}
	c.oversize[key] = now
	return true
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
