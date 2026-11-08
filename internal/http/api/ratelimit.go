package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/ratelimit"
)

// RateLimit carries the limiter and the proxies trusted for X-Forwarded-For.
type RateLimit struct {
	Limiter *ratelimit.Limiter
	Proxies []netip.Prefix
}

func (rl *RateLimit) active() bool { return rl != nil && rl.Limiter != nil }

func (rl *RateLimit) clientIP(r *http.Request) string {
	return ratelimit.ClientIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"), rl.Proxies)
}

func retryAfterSeconds(d time.Duration) int {
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		return 1
	}
	return secs
}

func setRateLimitHeaders(h http.Header, res ratelimit.Result) {
	if res.Limit > 0 {
		h.Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
		h.Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
	}
	if !res.Allowed {
		h.Set("Retry-After", strconv.Itoa(retryAfterSeconds(res.RetryAfter)))
	}
}

func writeTooMany(w http.ResponseWriter, res ratelimit.Result) {
	setRateLimitHeaders(w.Header(), res)
	w.Header().Set("Content-Type", restful.MIME_JSON)
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(model.NewAPIError(domain.NewErrorTooManyRequests(res.RetryAfter).Message))
}

func exemptFromRateLimit(path string) bool {
	switch path {
	case "/healthz", "/readyz", "/metrics", "/version":
		return true
	}
	return strings.HasPrefix(path, "/debug/pprof")
}

// Middleware throttles every non-gRPC request per client IP; the signed
// chunk routes get their own, larger bucket.
func (rl *RateLimit) Middleware(next http.Handler) http.Handler {
	if !rl.active() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if obs.IsGRPCRequest(r) || exemptFromRateLimit(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		scope := ratelimit.ScopeGlobalIP
		if strings.HasPrefix(r.URL.Path, "/api/chunks/") {
			scope = ratelimit.ScopeChunkIP
		}
		res := rl.Limiter.Allow(r.Context(), scope, rl.clientIP(r))
		if !res.Allowed {
			writeTooMany(w, res)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AuthFilter throttles the public auth routes per client IP.
func (rl *RateLimit) AuthFilter() restful.FilterFunction {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		if req.Request.Method != http.MethodOptions {
			res := rl.Limiter.Allow(req.Request.Context(), ratelimit.ScopeAuthIP, rl.clientIP(req.Request))
			if !res.Allowed {
				writeTooMany(resp.ResponseWriter, res)
				return
			}
		}
		chain.ProcessFilter(req, resp)
	}
}

// UserFilter throttles authenticated routes per user; it must run after the auth filter.
func (rl *RateLimit) UserFilter() restful.FilterFunction {
	return func(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {
		if claims, ok := domain.ClaimFromContext(req.Request.Context()); ok {
			key := claims.UserID.Base36()
			if claims.ServiceTokenID != 0 {
				key = "token:" + claims.ServiceTokenID.Base36()
			}
			res := rl.Limiter.Allow(req.Request.Context(), ratelimit.ScopeAPIUser, key)
			setRateLimitHeaders(resp.Header(), res)
			if !res.Allowed {
				writeTooMany(resp.ResponseWriter, res)
				return
			}
		}
		chain.ProcessFilter(req, resp)
	}
}
