package server

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
)

type mockTokenValidator struct {
	claims *domain.Claims
	err    error
}

func (m *mockTokenValidator) ValidateToken(_ context.Context, _ string) (*domain.Claims, error) {
	return m.claims, m.err
}

type fakeServerStream struct {
	ctx context.Context
}

func (s *fakeServerStream) Context() context.Context     { return s.ctx }
func (s *fakeServerStream) SendMsg(interface{}) error    { return nil }
func (s *fakeServerStream) RecvMsg(interface{}) error    { return nil }
func (s *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeServerStream) SetTrailer(metadata.MD)       {}

func unaryHandler(t *testing.T) grpc.UnaryHandler {
	t.Helper()
	return func(_ context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}
}

func TestInterceptor_PublicRoutes(t *testing.T) {
	handler := unaryHandler(t)
	publicRoutes := []string{
		"/greet.NipaService/LoginWithUsernamePassword",
		"/greet.NipaService/LoginWithRefreshToken",
		"/grpc.health.v1.Health/Check",
		"/grpc.health.v1.Health/Watch",
	}

	for _, route := range publicRoutes {
		t.Run(route, func(t *testing.T) {
			interceptor := NewInterceptor(nil)
			resp, err := interceptor.JWTUnary()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: route}, handler)
			require.NoError(t, err)
			require.Equal(t, "ok", resp)
		})
	}
}

func TestInterceptor_MissingMetadata(t *testing.T) {
	interceptor := NewInterceptor(nil)
	handler := unaryHandler(t)

	_, err := interceptor.JWTUnary()(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/nipa.SomeService/Method"}, handler)
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "missing metadata")
}

func TestInterceptor_MissingAuthorizationHeader(t *testing.T) {
	interceptor := NewInterceptor(nil)
	handler := unaryHandler(t)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
	_, err := interceptor.JWTUnary()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/nipa.SomeService/Method"}, handler)
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "missing authorization header")
}

func TestInterceptor_InvalidAuthorizationHeader(t *testing.T) {
	interceptor := NewInterceptor(nil)
	handler := unaryHandler(t)

	tests := []struct {
		name  string
		value string
	}{
		{"no bearer prefix", "token123"},
		{"basic scheme", "Basic dXNlcjpwYXNz"},
		{"only bearer with no token", "Bearer"},
		{"empty token", "Bearer "},
		{"extra segment", "Bearer a b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
				"authorization": []string{tt.value},
			})
			_, err := interceptor.JWTUnary()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/nipa.SomeService/Method"}, handler)
			require.Error(t, err)
			require.Equal(t, codes.Unauthenticated, status.Code(err))
			require.Contains(t, status.Convert(err).Message(), "invalid authorization header")
		})
	}
}

func TestInterceptor_InvalidToken(t *testing.T) {
	validator := &mockTokenValidator{err: domain.NewErrorUnauthorized("invalid token")}
	interceptor := NewInterceptor(validator)
	handler := unaryHandler(t)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
		"authorization": []string{"Bearer bad-token"},
	})
	_, err := interceptor.JWTUnary()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/nipa.SomeService/Method"}, handler)
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "invalid token")
}

func TestInterceptor_TokenServerErrorIsInternal(t *testing.T) {
	for _, validationErr := range []error{domain.NewErrorDatabase("db down"), errors.New("boom")} {
		interceptor := NewInterceptor(&mockTokenValidator{err: validationErr})
		ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
			"authorization": []string{"Bearer token"},
		})

		_, err := interceptor.JWTUnary()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/nipa.SomeService/Method"}, unaryHandler(t))
		require.Equal(t, codes.Internal, status.Code(err))
		require.NotContains(t, status.Convert(err).Message(), "db down")

		err = interceptor.JWTStream()(nil, serverStreamWithContext(ctx), &grpc.StreamServerInfo{FullMethod: "/nipa.SomeService/Stream"}, streamHandler(t, nil))
		require.Equal(t, codes.Internal, status.Code(err))
	}
}

func TestInterceptor_ValidToken(t *testing.T) {
	wantClaims := &domain.Claims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "42"},
		UserID:           42,
		IsAdmin:          true,
	}
	validator := &mockTokenValidator{claims: wantClaims}
	interceptor := NewInterceptor(validator)

	var gotClaims domain.Claims
	handler := func(ctx context.Context, _ interface{}) (interface{}, error) {
		claims, ok := domain.ClaimFromContext(ctx)
		require.True(t, ok)
		gotClaims = claims
		return "ok", nil
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
		"authorization": []string{"Bearer valid-token"},
	})
	resp, err := interceptor.JWTUnary()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/nipa.SomeService/Method"}, handler)
	require.NoError(t, err)
	require.Equal(t, "ok", resp)
	require.Equal(t, wantClaims.UserID, gotClaims.UserID)
	require.True(t, gotClaims.IsAdmin)
}

func streamHandler(t *testing.T, check func(ctx context.Context)) grpc.StreamHandler {
	t.Helper()
	return func(srv interface{}, stream grpc.ServerStream) error {
		if check != nil {
			check(stream.Context())
		}
		return nil
	}
}

func serverStreamWithContext(ctx context.Context) grpc.ServerStream {
	return &wrappedStream{ServerStream: &fakeServerStream{ctx: ctx}, ctx: ctx}
}

func TestStreamInterceptor_PublicRoutes(t *testing.T) {
	publicRoutes := []string{
		"/greet.NipaService/LoginWithUsernamePassword",
		"/greet.NipaService/LoginWithRefreshToken",
		"/grpc.health.v1.Health/Check",
		"/grpc.health.v1.Health/Watch",
	}

	for _, route := range publicRoutes {
		t.Run(route, func(t *testing.T) {
			interceptor := NewInterceptor(nil)
			err := interceptor.JWTStream()(nil, serverStreamWithContext(context.Background()), &grpc.StreamServerInfo{FullMethod: route}, streamHandler(t, nil))
			require.NoError(t, err)
		})
	}
}

func TestStreamInterceptor_MissingAuthorization(t *testing.T) {
	t.Run("missing metadata", func(t *testing.T) {
		interceptor := NewInterceptor(nil)
		err := interceptor.JWTStream()(nil, serverStreamWithContext(context.Background()), &grpc.StreamServerInfo{FullMethod: "/nipa.SomeService/Stream"}, streamHandler(t, nil))
		require.Error(t, err)
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("missing header", func(t *testing.T) {
		interceptor := NewInterceptor(nil)
		ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{})
		err := interceptor.JWTStream()(nil, serverStreamWithContext(ctx), &grpc.StreamServerInfo{FullMethod: "/nipa.SomeService/Stream"}, streamHandler(t, nil))
		require.Error(t, err)
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	})

	t.Run("no bearer prefix", func(t *testing.T) {
		interceptor := NewInterceptor(nil)
		ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
			"authorization": []string{"token123"},
		})
		err := interceptor.JWTStream()(nil, serverStreamWithContext(ctx), &grpc.StreamServerInfo{FullMethod: "/nipa.SomeService/Stream"}, streamHandler(t, nil))
		require.Error(t, err)
		require.Equal(t, codes.Unauthenticated, status.Code(err))
	})
}

func TestStreamInterceptor_InvalidToken(t *testing.T) {
	validator := &mockTokenValidator{err: domain.NewErrorUnauthorized("invalid token")}
	interceptor := NewInterceptor(validator)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
		"authorization": []string{"Bearer bad-token"},
	})
	err := interceptor.JWTStream()(nil, serverStreamWithContext(ctx), &grpc.StreamServerInfo{FullMethod: "/nipa.SomeService/Stream"}, streamHandler(t, nil))
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestStreamInterceptor_ValidToken(t *testing.T) {
	wantClaims := &domain.Claims{UserID: 42, IsAdmin: true}
	validator := &mockTokenValidator{claims: wantClaims}
	interceptor := NewInterceptor(validator)

	var gotClaims domain.Claims
	handler := func(_ interface{}, stream grpc.ServerStream) error {
		claims, ok := domain.ClaimFromContext(stream.Context())
		require.True(t, ok)
		gotClaims = claims
		return nil
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.MD{
		"authorization": []string{"Bearer valid-token"},
	})
	err := interceptor.JWTStream()(nil, serverStreamWithContext(ctx), &grpc.StreamServerInfo{FullMethod: "/nipa.SomeService/Stream"}, handler)
	require.NoError(t, err)
	require.Equal(t, wantClaims.UserID, gotClaims.UserID)
	require.True(t, gotClaims.IsAdmin)
}

func TestInterceptor_RequestMeta(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	interceptor := NewInterceptor(&mockTokenValidator{claims: &domain.Claims{UserID: 1}}).WithTrustedProxies(proxies)
	info := &grpc.UnaryServerInfo{FullMethod: "/greet.NipaService/GetBranch"}

	call := func(peerAddr string) domain.RequestMeta {
		t.Helper()
		ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(peerAddr), Port: 4000}})
		ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(
			"authorization", "Bearer token", "x-forwarded-for", "203.0.113.9", "user-agent", "nipa-cli/1.0"))
		var meta domain.RequestMeta
		_, err := interceptor.JWTUnary()(ctx, nil, info, func(ctx context.Context, _ interface{}) (interface{}, error) {
			var ok bool
			meta, ok = domain.RequestMetaFromContext(ctx)
			require.True(t, ok)
			return nil, nil
		})
		require.NoError(t, err)
		return meta
	}

	trusted := call("10.1.2.3")
	require.Equal(t, "203.0.113.9", trusted.ClientIP)
	require.Equal(t, "nipa-cli/1.0", trusted.UserAgent)
	require.Equal(t, "192.0.2.4", call("192.0.2.4").ClientIP, "an untrusted peer's forwarded header is ignored")

	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 4000}})
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer token", "x-forwarded-for", "203.0.113.9"))
	err := interceptor.JWTStream()(nil, &fakeServerStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: "/greet.NipaService/Push"},
		func(_ interface{}, ss grpc.ServerStream) error {
			meta, ok := domain.RequestMetaFromContext(ss.Context())
			require.True(t, ok)
			require.Equal(t, "203.0.113.9", meta.ClientIP)
			return nil
		})
	require.NoError(t, err)
}

func TestBearerToken_CaseInsensitiveScheme(t *testing.T) {
	for _, header := range []string{"Bearer abc", "bearer abc", "BEARER abc"} {
		token, ok := bearerToken(header)
		require.True(t, ok, header)
		require.Equal(t, "abc", token)
	}
}

func TestInterceptor_PublicLoginCarriesRequestMeta(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	interceptor := NewInterceptor(nil).WithTrustedProxies(proxies)
	base := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 4000}})
	withMD := metadata.NewIncomingContext(base, metadata.Pairs("x-forwarded-for", "203.0.113.9", "user-agent", "nipa-cli/1.0"))

	for _, method := range []string{
		"/greet.NipaService/LoginWithUsernamePassword",
		"/greet.NipaService/BeginLoginSSHKey",
		"/greet.NipaService/CompleteLoginSSHKey",
	} {
		t.Run(method, func(t *testing.T) {
			_, err := interceptor.JWTUnary()(withMD, nil, &grpc.UnaryServerInfo{FullMethod: method}, func(ctx context.Context, _ interface{}) (interface{}, error) {
				meta, ok := domain.RequestMetaFromContext(ctx)
				require.True(t, ok)
				require.Equal(t, domain.RequestMeta{ClientIP: "203.0.113.9", UserAgent: "nipa-cli/1.0"}, meta)
				_, hasClaim := domain.ClaimFromContext(ctx)
				require.False(t, hasClaim)
				return nil, nil
			})
			require.NoError(t, err)
		})
	}

	_, err := interceptor.JWTUnary()(base, nil, &grpc.UnaryServerInfo{FullMethod: "/greet.NipaService/LoginWithUsernamePassword"}, func(ctx context.Context, _ interface{}) (interface{}, error) {
		meta, ok := domain.RequestMetaFromContext(ctx)
		require.True(t, ok)
		require.Equal(t, "10.1.2.3", meta.ClientIP)
		return nil, nil
	})
	require.NoError(t, err)

	err = interceptor.JWTStream()(nil, &fakeServerStream{ctx: withMD}, &grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"},
		func(_ interface{}, ss grpc.ServerStream) error {
			meta, ok := domain.RequestMetaFromContext(ss.Context())
			require.True(t, ok)
			require.Equal(t, "203.0.113.9", meta.ClientIP)
			return nil
		})
	require.NoError(t, err)
}
