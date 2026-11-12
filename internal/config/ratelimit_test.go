package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/ratelimit"
)

func TestRateLimitRules(t *testing.T) {
	c := &Config{RateLimitAuthIPPerMinute: 60, RateLimitAuthIPBurst: 7, RateLimitAuthEmailPerMinute: 120, RateLimitAuthEmailBurst: 20,
		RateLimitRegisterEmailPerMinute: 6, RateLimitRegisterEmailBurst: 3}
	rules := c.RateLimitRules()
	assert.Equal(t, ratelimit.Rule{Rate: 1, Burst: 7}, rules[ratelimit.ScopeAuthIP])
	assert.Equal(t, ratelimit.Rule{Rate: 2, Burst: 20}, rules[ratelimit.ScopeAuthEmail])
	assert.Equal(t, ratelimit.Rule{Rate: 0.1, Burst: 3}, rules[ratelimit.ScopeRegisterEmail])
	assert.Len(t, rules, 10)
}

func TestRateLimitTrustedProxies(t *testing.T) {
	c := &Config{RateLimitTrustedProxies: "127.0.0.1,10.0.0.0/8"}
	got, err := c.TrustedProxyPrefixes()
	require.NoError(t, err)
	assert.Len(t, got, 2)

	c.RateLimitTrustedProxies = "bad"
	_, err = c.TrustedProxyPrefixes()
	assert.Error(t, err)
}

func TestRateLimitStoreName(t *testing.T) {
	tests := []struct {
		name         string
		store, addr  string
		redisAllowed bool
		want         string
		wantErr      string
	}{
		{"free default", "", "", false, "memory", ""},
		{"free ignores addr", "", "r:6379", false, "memory", ""},
		{"free memory", "memory", "", false, "memory", ""},
		{"free redis refused", "redis", "r:6379", false, "", "enterprise"},
		{"ee default memory", "", "", true, "memory", ""},
		{"ee default redis with addr", "", "r:6379", true, "redis", ""},
		{"ee explicit memory wins", "memory", "r:6379", true, "memory", ""},
		{"ee redis", " Redis ", "r:6379", true, "redis", ""},
		{"ee redis without addr", "redis", "", true, "", "RATE_LIMIT_REDIS_ADDR"},
		{"unknown", "etcd", "", true, "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{RateLimitStore: tt.store, RateLimitRedisAddr: tt.addr}
			got, err := c.RateLimitStoreName(tt.redisAllowed)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
