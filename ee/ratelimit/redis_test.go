package ratelimit

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/nipalab/nipa/internal/ratelimit"
)

var testRedisAddr string

func TestMain(m *testing.M) {
	ctx := context.Background()
	var container *tcredis.RedisContainer
	if addr := os.Getenv("NIPA_TEST_REDIS_ADDR"); addr != "" {
		testRedisAddr = addr
	} else {
		c, err := tcredis.Run(ctx, "redis:7")
		if err != nil {
			fmt.Fprintln(os.Stderr, "start redis container:", err)
			os.Exit(1)
		}
		container = c
		endpoint, err := c.Endpoint(ctx, "")
		if err != nil {
			fmt.Fprintln(os.Stderr, "redis endpoint:", err)
			os.Exit(1)
		}
		testRedisAddr = endpoint
	}
	code := m.Run()
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
	os.Exit(code)
}

func newStore(t *testing.T) *Redis {
	t.Helper()
	store, err := New(context.Background(), Config{Addr: testRedisAddr, Prefix: fmt.Sprintf("test:%s:", t.Name())})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRedisBurstThenLimit(t *testing.T) {
	store := newStore(t)
	rule := ratelimit.Rule{Rate: 0.5, Burst: 3}
	for i := 0; i < 3; i++ {
		res, err := store.Take(context.Background(), "k", rule, 1)
		require.NoError(t, err)
		assert.True(t, res.Allowed)
		assert.Equal(t, 2-i, res.Remaining)
	}
	res, err := store.Take(context.Background(), "k", rule, 1)
	require.NoError(t, err)
	assert.False(t, res.Allowed)
	assert.Equal(t, 3, res.Limit)
	assert.Greater(t, res.RetryAfter, time.Duration(0))
	assert.LessOrEqual(t, res.RetryAfter, 2*time.Second)
}

func TestRedisRefill(t *testing.T) {
	store := newStore(t)
	rule := ratelimit.Rule{Rate: 20, Burst: 1}
	res, _ := store.Take(context.Background(), "k", rule, 1)
	require.True(t, res.Allowed)
	res, _ = store.Take(context.Background(), "k", rule, 1)
	require.False(t, res.Allowed)

	time.Sleep(120 * time.Millisecond)
	res, err := store.Take(context.Background(), "k", rule, 1)
	require.NoError(t, err)
	assert.True(t, res.Allowed)
}

func TestRedisKeysIndependentAndPrefixed(t *testing.T) {
	store := newStore(t)
	rule := ratelimit.Rule{Rate: 0.001, Burst: 1}
	a, _ := store.Take(context.Background(), "a", rule, 1)
	b, _ := store.Take(context.Background(), "b", rule, 1)
	a2, _ := store.Take(context.Background(), "a", rule, 1)
	assert.True(t, a.Allowed)
	assert.True(t, b.Allowed)
	assert.False(t, a2.Allowed)

	raw := redis.NewClient(&redis.Options{Addr: testRedisAddr})
	defer func() { _ = raw.Close() }()
	n, err := raw.Exists(context.Background(), store.prefix+"a").Result()
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	ttl, err := raw.PTTL(context.Background(), store.prefix+"a").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
}

func TestRedisAtomicAcrossStores(t *testing.T) {
	a := newStore(t)
	b, err := New(context.Background(), Config{Addr: testRedisAddr, Prefix: a.prefix})
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	rule := ratelimit.Rule{Rate: 0.0001, Burst: 40}
	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 120; i++ {
		wg.Add(1)
		store := a
		if i%2 == 0 {
			store = b
		}
		go func() {
			defer wg.Done()
			res, err := store.Take(context.Background(), "shared", rule, 1)
			if err == nil && res.Allowed {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 40, allowed)
}

func TestRedisUnreachable(t *testing.T) {
	_, err := New(context.Background(), Config{Addr: "127.0.0.1:1"})
	require.ErrorContains(t, err, "ping redis")

	store := NewWithClient(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "x:")
	defer func() { _ = store.Close() }()
	_, err = store.Take(context.Background(), "k", ratelimit.Rule{Rate: 1, Burst: 1}, 1)
	assert.Error(t, err)
}

func TestRedisLimiterFailsOpen(t *testing.T) {
	store := NewWithClient(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), "x:")
	limiter := ratelimit.NewLimiter(store, map[string]ratelimit.Rule{ratelimit.ScopeAuthIP: {Rate: 1, Burst: 1}})
	defer func() { _ = limiter.Close() }()
	assert.True(t, limiter.Allow(context.Background(), ratelimit.ScopeAuthIP, "1.2.3.4").Allowed)
}
