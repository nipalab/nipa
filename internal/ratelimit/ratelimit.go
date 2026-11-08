package ratelimit

import (
	"context"
	"time"

	"github.com/nipalab/nipa/internal/obs"
)

const (
	ScopeAuthIP   = "auth_ip"
	ScopeAuthID   = "auth_id"
	ScopeAPIUser  = "api_user"
	ScopeChunkIP  = "chunk_ip"
	ScopeGlobalIP = "global_ip"

	ScopeInviteUser  = "invite_user"
	ScopeInviteEmail = "invite_email"
	ScopeInviteToken = "invite_token"
)

const (
	ResultAllowed = "allowed"
	ResultLimited = "limited"
	ResultError   = "error"
)

// Rule is a token bucket: Rate tokens per second refill up to Burst.
type Rule struct {
	Rate  float64
	Burst int
}

func (r Rule) enabled() bool { return r.Rate > 0 && r.Burst > 0 }

type Result struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
}

// Store keeps token buckets. Take must be atomic per key.
//
//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=ratelimit_mock_test.go -package=ratelimit
type Store interface {
	Take(ctx context.Context, key string, rule Rule, n int) (Result, error)
	Close() error
}

// Limiter applies named scope rules on top of a Store. A nil Limiter allows everything.
type Limiter struct {
	store   Store
	rules   map[string]Rule
	metrics *obs.Metrics
}

func NewLimiter(store Store, rules map[string]Rule) *Limiter {
	return &Limiter{store: store, rules: rules}
}

func (l *Limiter) WithMetrics(m *obs.Metrics) *Limiter {
	l.metrics = m
	return l
}

// Allow fails open: an unknown scope, a disabled rule or a store error all allow the request.
func (l *Limiter) Allow(ctx context.Context, scope, subject string) Result {
	if l == nil || subject == "" {
		return Result{Allowed: true}
	}
	rule, ok := l.rules[scope]
	if !ok || !rule.enabled() {
		return Result{Allowed: true}
	}
	res, err := l.store.Take(ctx, scope+":"+subject, rule, 1)
	if err != nil {
		obs.Log(ctx).Warn("rate limit store failed, allowing request", "scope", scope, "error", err)
		l.metrics.ObserveRateLimit(scope, ResultError)
		return Result{Allowed: true}
	}
	if res.Allowed {
		l.metrics.ObserveRateLimit(scope, ResultAllowed)
	} else {
		l.metrics.ObserveRateLimit(scope, ResultLimited)
	}
	return res
}

func (l *Limiter) Close() error {
	if l == nil || l.store == nil {
		return nil
	}
	return l.store.Close()
}
