package server

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
	"github.com/nipalab/nipa/internal/snow"
)

func newTestLimiter(scope string, burst int) *ratelimit.Limiter {
	return ratelimit.NewLimiter(ratelimit.NewMemory(), map[string]ratelimit.Rule{scope: {Rate: 0.0001, Burst: burst}})
}

func peerCtx(addr string, xff string) context.Context {
	tcp, _ := net.ResolveTCPAddr("tcp", addr)
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: tcp})
	if xff != "" {
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-forwarded-for", xff))
	}
	return ctx
}

func okHandler(context.Context, interface{}) (interface{}, error) { return "ok", nil }

func TestRateLimiterPublicMethodByIP(t *testing.T) {
	proxies, _ := ratelimit.ParseTrustedProxies("127.0.0.1")
	rl := NewRateLimiter(newTestLimiter(ratelimit.ScopeAuthIP, 2), proxies)
	info := &grpc.UnaryServerInfo{FullMethod: "/greet.NipaService/LoginWithUsernamePassword"}

	for i := 0; i < 2; i++ {
		_, err := rl.Unary()(peerCtx("127.0.0.1:1", "198.51.100.1"), nil, info, okHandler)
		require.NoError(t, err)
	}
	_, err := rl.Unary()(peerCtx("127.0.0.1:1", "198.51.100.1"), nil, info, okHandler)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	assert.Contains(t, err.Error(), domain.RateLimitMessage)

	_, err = rl.Unary()(peerCtx("127.0.0.1:1", "198.51.100.2"), nil, info, okHandler)
	require.NoError(t, err)
}

func TestRateLimiterUserScope(t *testing.T) {
	rl := NewRateLimiter(newTestLimiter(ratelimit.ScopeAPIUser, 1), nil)
	info := &grpc.UnaryServerInfo{FullMethod: "/greet.NipaService/GetListBranch"}
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(42)})

	_, err := rl.Unary()(ctx, nil, info, okHandler)
	require.NoError(t, err)
	_, err = rl.Unary()(ctx, nil, info, okHandler)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	_, err = rl.Unary()(context.Background(), nil, info, okHandler)
	require.NoError(t, err)
}

func TestRateLimiterExemptAndDisabled(t *testing.T) {
	rl := NewRateLimiter(newTestLimiter(ratelimit.ScopeAuthIP, 1), nil)
	for i := 0; i < 5; i++ {
		_, err := rl.Unary()(peerCtx("1.2.3.4:1", ""), nil, &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}, okHandler)
		require.NoError(t, err)
		_, err = rl.Unary()(peerCtx("1.2.3.4:1", ""), nil, &grpc.UnaryServerInfo{FullMethod: "/greet.NipaService/GetServerVersion"}, okHandler)
		require.NoError(t, err)
	}
	var disabled *RateLimiter
	require.NoError(t, disabled.check(context.Background(), "/x"))
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f fakeStream) Context() context.Context { return f.ctx }

func TestRateLimiterStream(t *testing.T) {
	rl := NewRateLimiter(newTestLimiter(ratelimit.ScopeAPIUser, 1), nil)
	info := &grpc.StreamServerInfo{FullMethod: "/greet.NipaService/WatchSomething"}
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
	handler := func(interface{}, grpc.ServerStream) error { return nil }

	require.NoError(t, rl.Stream()(nil, fakeStream{ctx: ctx}, info, handler))
	err := rl.Stream()(nil, fakeStream{ctx: ctx}, info, handler)
	assert.Equal(t, codes.ResourceExhausted, status.Code(err))
}
