package ratelimit

import (
	"context"
	"hash/fnv"
	"sync"
	"time"
)

const (
	memoryShards     = 32
	memoryMaxEntries = 200_000
	memoryIdleTTL    = 10 * time.Minute
)

type bucket struct {
	tokens float64
	last   time.Time
}

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

// Memory is a per-process Store.
type Memory struct {
	shards [memoryShards]shard
	now    func() time.Time
	stop   chan struct{}
	once   sync.Once
}

func NewMemory() *Memory {
	m := newMemory(time.Now)
	go m.janitor(time.Minute)
	return m
}

func newMemory(now func() time.Time) *Memory {
	m := &Memory{now: now, stop: make(chan struct{})}
	for i := range m.shards {
		m.shards[i].buckets = make(map[string]*bucket)
	}
	return m
}

func (m *Memory) shardFor(key string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &m.shards[h.Sum32()%memoryShards]
}

func (m *Memory) Take(_ context.Context, key string, rule Rule, n int) (Result, error) {
	now := m.now()
	s := m.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[key]
	if !ok {
		if len(s.buckets) >= memoryMaxEntries/memoryShards {
			s.evictLocked(now)
		}
		b = &bucket{tokens: float64(rule.Burst), last: now}
		s.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens += elapsed * rule.Rate
			if b.tokens > float64(rule.Burst) {
				b.tokens = float64(rule.Burst)
			}
			b.last = now
		}
	}

	need := float64(n)
	if b.tokens >= need {
		b.tokens -= need
		return Result{Allowed: true, Limit: rule.Burst, Remaining: int(b.tokens)}, nil
	}
	wait := time.Duration((need - b.tokens) / rule.Rate * float64(time.Second))
	return Result{Limit: rule.Burst, RetryAfter: wait}, nil
}

func (s *shard) evictLocked(now time.Time) {
	for k, b := range s.buckets {
		if now.Sub(b.last) > memoryIdleTTL {
			delete(s.buckets, k)
		}
	}
	if len(s.buckets) >= memoryMaxEntries/memoryShards {
		for k := range s.buckets {
			delete(s.buckets, k)
			break
		}
	}
}

func (m *Memory) sweep() {
	now := m.now()
	for i := range m.shards {
		s := &m.shards[i]
		s.mu.Lock()
		for k, b := range s.buckets {
			if now.Sub(b.last) > memoryIdleTTL {
				delete(s.buckets, k)
			}
		}
		s.mu.Unlock()
	}
}

func (m *Memory) janitor(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.sweep()
		case <-m.stop:
			return
		}
	}
}

func (m *Memory) Close() error {
	m.once.Do(func() { close(m.stop) })
	return nil
}
