package config

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/nipalab/nipa/internal/ratelimit"
)

const (
	RateLimitStoreMemory = "memory"
	RateLimitStoreRedis  = "redis"
)

// RateLimitRules maps the RATE_LIMIT_* settings onto per-scope token buckets.
func (c *Config) RateLimitRules() map[string]ratelimit.Rule {
	rule := func(perMinute float64, burst int) ratelimit.Rule {
		return ratelimit.Rule{Rate: perMinute / 60, Burst: burst}
	}
	return map[string]ratelimit.Rule{
		ratelimit.ScopeAuthIP:   rule(c.RateLimitAuthIPPerMinute, c.RateLimitAuthIPBurst),
		ratelimit.ScopeAuthID:   rule(c.RateLimitAuthIDPerMinute, c.RateLimitAuthIDBurst),
		ratelimit.ScopeAPIUser:  rule(c.RateLimitAPIUserPerMinute, c.RateLimitAPIUserBurst),
		ratelimit.ScopeChunkIP:  rule(c.RateLimitChunkIPPerMinute, c.RateLimitChunkIPBurst),
		ratelimit.ScopeGlobalIP: rule(c.RateLimitGlobalIPPerMinute, c.RateLimitGlobalIPBurst),
	}
}

func (c *Config) TrustedProxyPrefixes() ([]netip.Prefix, error) {
	proxies, err := ratelimit.ParseTrustedProxies(c.RateLimitTrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("RATE_LIMIT_TRUSTED_PROXIES: %w", err)
	}
	return proxies, nil
}

// RateLimitStoreName resolves the store kind. An unset store picks redis only
// when an address is configured; the free server passes redisAllowed=false.
func (c *Config) RateLimitStoreName(redisAllowed bool) (string, error) {
	name := strings.ToLower(strings.TrimSpace(c.RateLimitStore))
	switch name {
	case "":
		if redisAllowed && c.RateLimitRedisAddr != "" {
			return RateLimitStoreRedis, nil
		}
		return RateLimitStoreMemory, nil
	case RateLimitStoreMemory:
		return RateLimitStoreMemory, nil
	case RateLimitStoreRedis:
		if !redisAllowed {
			return "", fmt.Errorf("RATE_LIMIT_STORE=redis requires the enterprise server (nipad-ee)")
		}
		if c.RateLimitRedisAddr == "" {
			return "", fmt.Errorf("RATE_LIMIT_STORE=redis requires RATE_LIMIT_REDIS_ADDR")
		}
		return RateLimitStoreRedis, nil
	default:
		return "", fmt.Errorf("unknown RATE_LIMIT_STORE %q (use memory or redis)", c.RateLimitStore)
	}
}
