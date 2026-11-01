package server

import (
	"context"
	"net/netip"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
)

// RateLimiter throttles gRPC calls: the public login RPCs per client IP and
// every authenticated call per user. Chain it after the JWT interceptor.
type RateLimiter struct {
	limiter *ratelimit.Limiter
	proxies []netip.Prefix
}

func NewRateLimiter(limiter *ratelimit.Limiter, proxies []netip.Prefix) *RateLimiter {
	return &RateLimiter{limiter: limiter, proxies: proxies}
}

func exemptFromRateLimit(method string) bool {
	return method == "/greet.NipaService/GetServerVersion" || strings.HasPrefix(method, "/grpc.health.v1.Health/")
}

func (r *RateLimiter) clientIP(ctx context.Context) string {
	var remote string
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		remote = p.Addr.String()
	}
	var xff string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		xff = strings.Join(md.Get("x-forwarded-for"), ",")
	}
	return ratelimit.ClientIP(remote, xff, r.proxies)
}

func (r *RateLimiter) check(ctx context.Context, method string) error {
	if r == nil || r.limiter == nil || exemptFromRateLimit(method) {
		return nil
	}
	var res ratelimit.Result
	if isPublicMethod(method) {
		res = r.limiter.Allow(ctx, ratelimit.ScopeAuthIP, r.clientIP(ctx))
	} else if claims, ok := domain.ClaimFromContext(ctx); ok {
		res = r.limiter.Allow(ctx, ratelimit.ScopeAPIUser, claims.UserID.Base36())
	} else {
		return nil
	}
	if res.Allowed {
		return nil
	}
	return status.Error(codes.ResourceExhausted, domain.NewErrorTooManyRequests(res.RetryAfter).Message)
}

func (r *RateLimiter) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if err := r.check(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func (r *RateLimiter) Stream() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := r.check(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
