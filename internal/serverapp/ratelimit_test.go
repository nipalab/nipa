package serverapp

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/ratelimit"
)

func baseRateLimitConfig() *config.Config {
	return &config.Config{RateLimitEnabled: true, RateLimitTrustedProxies: "127.0.0.1", RateLimitAuthIPPerMinute: 60, RateLimitAuthIPBurst: 1}
}

type nopStore struct{ ratelimit.Store }

func TestBuildRateLimiterMemoryDefault(t *testing.T) {
	limiter, proxies, err := BuildRateLimiter(baseRateLimitConfig(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, limiter)
	assert.Len(t, proxies, 1)
	require.NoError(t, limiter.Close())
}

func TestBuildRateLimiterDisabled(t *testing.T) {
	cfg := baseRateLimitConfig()
	cfg.RateLimitEnabled = false
	limiter, _, err := BuildRateLimiter(cfg, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, limiter)
}

func TestBuildRateLimiterFreeRejectsRedis(t *testing.T) {
	cfg := baseRateLimitConfig()
	cfg.RateLimitStore = "redis"
	cfg.RateLimitRedisAddr = "r:6379"
	_, _, err := BuildRateLimiter(cfg, nil, nil)
	require.ErrorContains(t, err, "enterprise")
}

func TestBuildRateLimiterBadProxies(t *testing.T) {
	cfg := baseRateLimitConfig()
	cfg.RateLimitTrustedProxies = "nope"
	_, _, err := BuildRateLimiter(cfg, nil, nil)
	require.Error(t, err)
}

func TestBuildRateLimiterRedisFactory(t *testing.T) {
	cfg := baseRateLimitConfig()
	cfg.RateLimitRedisAddr = "r:6379"

	called := 0
	limiter, _, err := BuildRateLimiter(cfg, nil, func(*config.Config) (ratelimit.Store, error) {
		called++
		return ratelimit.NewMemory(), nil
	})
	require.NoError(t, err)
	require.NotNil(t, limiter)
	assert.Equal(t, 1, called)

	cfg.RateLimitStore = "memory"
	_, _, err = BuildRateLimiter(cfg, nil, func(*config.Config) (ratelimit.Store, error) {
		called++
		return nil, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, called)

	cfg.RateLimitStore = "redis"
	_, _, err = BuildRateLimiter(cfg, nil, func(*config.Config) (ratelimit.Store, error) {
		return nil, errors.New("dial failed")
	})
	require.ErrorContains(t, err, "dial failed")
}

func TestWithRateLimitOption(t *testing.T) {
	var ro runOptions
	WithRateLimit(nil, nil)(&ro)
	assert.Nil(t, ro.rateLimit)

	limiter := ratelimit.NewLimiter(ratelimit.NewMemory(), nil)
	WithRateLimit(limiter, nil)(&ro)
	require.NotNil(t, ro.rateLimit)
	assert.Same(t, limiter, ro.rateLimit.Limiter)
}
