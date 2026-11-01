package serverapp

import (
	"fmt"
	"net/netip"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/ratelimit"
)

// RedisStoreFactory builds the redis-backed store; only the enterprise main supplies one.
type RedisStoreFactory func(cfg *config.Config) (ratelimit.Store, error)

// BuildRateLimiter resolves RATE_LIMIT_STORE and builds the limiter. It returns
// a nil limiter when RATE_LIMIT_ENABLED is false. The free server passes a nil
// factory, which makes `redis` a startup error.
func BuildRateLimiter(cfg *config.Config, metrics *obs.Metrics, redis RedisStoreFactory) (*ratelimit.Limiter, []netip.Prefix, error) {
	proxies, err := cfg.TrustedProxyPrefixes()
	if err != nil {
		return nil, nil, err
	}
	name, err := cfg.RateLimitStoreName(redis != nil)
	if err != nil {
		return nil, nil, err
	}
	if !cfg.RateLimitEnabled {
		return nil, proxies, nil
	}

	var store ratelimit.Store
	switch name {
	case config.RateLimitStoreRedis:
		store, err = redis(cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("rate limit redis store: %w", err)
		}
	default:
		store = ratelimit.NewMemory()
	}
	return ratelimit.NewLimiter(store, cfg.RateLimitRules()).WithMetrics(metrics), proxies, nil
}
