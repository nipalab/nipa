package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestMemoryBurstThenLimit(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	m := newMemory(clock.Now)
	rule := Rule{Rate: 1, Burst: 3}
	for i := 0; i < 3; i++ {
		res, err := m.Take(context.Background(), "k", rule, 1)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 2-i, res.Remaining)
	}
	res, err := m.Take(context.Background(), "k", rule, 1)
	require.NoError(t, err)
	assert.False(t, res.Allowed)
	assert.Equal(t, time.Second, res.RetryAfter)
	assert.Equal(t, 3, res.Limit)
}

func TestMemoryRefill(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	m := newMemory(clock.Now)
	rule := Rule{Rate: 2, Burst: 2}
	for i := 0; i < 2; i++ {
		_, _ = m.Take(context.Background(), "k", rule, 1)
	}
	res, _ := m.Take(context.Background(), "k", rule, 1)
	require.False(t, res.Allowed)

	clock.Advance(500 * time.Millisecond)
	res, _ = m.Take(context.Background(), "k", rule, 1)
	assert.True(t, res.Allowed)

	clock.Advance(time.Hour)
	res, _ = m.Take(context.Background(), "k", rule, 1)
	assert.True(t, res.Allowed)
	assert.Equal(t, 1, res.Remaining)
}

func TestMemoryKeysIndependent(t *testing.T) {
	m := newMemory(time.Now)
	rule := Rule{Rate: 0.001, Burst: 1}
	a, _ := m.Take(context.Background(), "a", rule, 1)
	b, _ := m.Take(context.Background(), "b", rule, 1)
	a2, _ := m.Take(context.Background(), "a", rule, 1)
	assert.True(t, a.Allowed)
	assert.True(t, b.Allowed)
	assert.False(t, a2.Allowed)
}

func TestMemoryConcurrentNeverExceedsBurst(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	m := newMemory(clock.Now)
	rule := Rule{Rate: 1, Burst: 50}
	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := m.Take(context.Background(), "k", rule, 1)
			if res.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 50, allowed)
}

func TestMemorySweepEvictsIdle(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	m := newMemory(clock.Now)
	_, _ = m.Take(context.Background(), "k", Rule{Rate: 1, Burst: 1}, 1)
	clock.Advance(memoryIdleTTL + time.Second)
	m.sweep()
	total := 0
	for i := range m.shards {
		total += len(m.shards[i].buckets)
	}
	assert.Zero(t, total)
}

func TestMemoryEvictsWhenShardFull(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1000, 0)}
	m := newMemory(clock.Now)
	s := m.shardFor("x")
	limit := memoryMaxEntries / memoryShards
	for i := 0; i < limit; i++ {
		s.buckets[string(rune(i))+"-old"] = &bucket{last: clock.Now()}
	}
	clock.Advance(memoryIdleTTL + time.Second)
	_, _ = m.Take(context.Background(), "x", Rule{Rate: 1, Burst: 1}, 1)
	assert.Len(t, s.buckets, 1)

	for i := 0; i < limit; i++ {
		s.buckets[string(rune(i))+"-fresh"] = &bucket{last: clock.Now()}
	}
	_, _ = m.Take(context.Background(), "y-new", Rule{Rate: 1, Burst: 1}, 1)
	assert.LessOrEqual(t, len(m.shardFor("y-new").buckets), limit+1)
}

func TestMemoryCloseIdempotent(t *testing.T) {
	m := NewMemory()
	require.NoError(t, m.Close())
	require.NoError(t, m.Close())
}

func TestMemoryJanitorStops(t *testing.T) {
	m := newMemory(time.Now)
	done := make(chan struct{})
	go func() {
		m.janitor(5 * time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, m.Close())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("janitor did not stop")
	}
}
