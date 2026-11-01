package ratelimit

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nipalab/nipa/internal/ratelimit"
)

const takeScript = `
local t = redis.call('TIME')
local now = t[1] * 1000 + math.floor(t[2] / 1000)
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local need = tonumber(ARGV[3])
local d = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(d[1])
local ts = tonumber(d[2])
if tokens == nil or ts == nil then
  tokens = burst
  ts = now
end
tokens = math.min(burst, tokens + math.max(0, now - ts) / 1000 * rate)
local allowed = 0
local retry = 0
if tokens >= need then
  tokens = tokens - need
  allowed = 1
else
  retry = math.ceil((need - tokens) / rate * 1000)
end
redis.call('HSET', KEYS[1], 'tokens', tostring(tokens), 'ts', now)
redis.call('PEXPIRE', KEYS[1], math.ceil(burst / rate * 1000) + 1000)
return {allowed, math.floor(tokens), retry}
`

type Config struct {
	Addr     string
	Password string
	DB       int
	TLS      bool
	Prefix   string
}

// Redis is a ratelimit.Store shared by every server process using the same instance.
type Redis struct {
	client redis.UniversalClient
	script *redis.Script
	prefix string
}

func New(ctx context.Context, cfg Config) (*Redis, error) {
	opts := &redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB}
	if cfg.TLS {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	store := NewWithClient(redis.NewClient(opts), cfg.Prefix)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := store.client.Ping(pingCtx).Err(); err != nil {
		_ = store.client.Close()
		return nil, fmt.Errorf("ping redis %s: %w", cfg.Addr, err)
	}
	return store, nil
}

func NewWithClient(client redis.UniversalClient, prefix string) *Redis {
	return &Redis{client: client, script: redis.NewScript(takeScript), prefix: prefix}
}

func (r *Redis) Take(ctx context.Context, key string, rule ratelimit.Rule, n int) (ratelimit.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	raw, err := r.script.Run(ctx, r.client, []string{r.prefix + key}, rule.Rate, rule.Burst, n).Int64Slice()
	if err != nil {
		return ratelimit.Result{}, err
	}
	if len(raw) != 3 {
		return ratelimit.Result{}, fmt.Errorf("unexpected script reply of %d values", len(raw))
	}
	return ratelimit.Result{
		Allowed:    raw[0] == 1,
		Limit:      rule.Burst,
		Remaining:  int(raw[1]),
		RetryAfter: time.Duration(raw[2]) * time.Millisecond,
	}, nil
}

func (r *Redis) Close() error {
	return r.client.Close()
}
