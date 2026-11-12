package server

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/ratelimit"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// isPublicMethod lists the RPCs that skip JWT authentication: the login calls,
// the version probe and the standard gRPC health service.
func isPublicMethod(method string) bool {
	switch method {
	case "/greet.NipaService/LoginWithUsernamePassword",
		"/greet.NipaService/LoginWithRefreshToken",
		"/greet.NipaService/BeginLoginSSHKey",
		"/greet.NipaService/CompleteLoginSSHKey",
		"/greet.NipaService/GetServerVersion",
		"/grpc.health.v1.Health/Check",
		"/grpc.health.v1.Health/Watch":
		return true
	default:
		return false
	}
}

type tokenValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*domain.Claims, error)
}

type Interceptor struct {
	tokenValidator tokenValidator
	proxies        []netip.Prefix
}

func NewInterceptor(tokenValidator tokenValidator) *Interceptor {
	return &Interceptor{
		tokenValidator: tokenValidator,
	}
}

// WithTrustedProxies sets the proxies whose x-forwarded-for is honoured for
// the client IP attached to the call.
func (i *Interceptor) WithTrustedProxies(proxies []netip.Prefix) *Interceptor {
	i.proxies = proxies
	return i
}

// requestMeta resolves the caller's client IP and user agent from the peer
// address and the incoming metadata.
func requestMeta(ctx context.Context, md metadata.MD, proxies []netip.Prefix) domain.RequestMeta {
	var remote string
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		remote = p.Addr.String()
	}
	return domain.RequestMeta{
		ClientIP:  ratelimit.ClientIP(remote, strings.Join(md.Get("x-forwarded-for"), ","), proxies),
		UserAgent: strings.Join(md.Get("user-agent"), " "),
	}
}

func (i *Interceptor) JWTUnary() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if isPublicMethod(info.FullMethod) {
			md, _ := metadata.FromIncomingContext(ctx)
			return handler(domain.ContextWithRequestMeta(ctx, requestMeta(ctx, md, i.proxies)), req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeader := md["authorization"]
		if len(authHeader) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		tokenString, ok := bearerToken(authHeader[0])
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "invalid authorization header")
		}

		claims, err := i.tokenValidator.ValidateToken(ctx, tokenString)
		if err != nil {
			return nil, tokenError(err)
		}

		ctx = domain.ContextWithClaim(ctx, *claims)
		ctx = domain.ContextWithRequestMeta(ctx, requestMeta(ctx, md, i.proxies))

		return handler(ctx, req)
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}

func (i *Interceptor) JWTStream() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isPublicMethod(info.FullMethod) {
			md, _ := metadata.FromIncomingContext(ss.Context())
			ctx := domain.ContextWithRequestMeta(ss.Context(), requestMeta(ss.Context(), md, i.proxies))
			return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
		}

		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeader := md["authorization"]
		if len(authHeader) == 0 {
			return status.Error(codes.Unauthenticated, "missing authorization header")
		}

		tokenString, ok := bearerToken(authHeader[0])
		if !ok {
			return status.Error(codes.Unauthenticated, "invalid authorization header")
		}

		claims, err := i.tokenValidator.ValidateToken(ss.Context(), tokenString)
		if err != nil {
			return tokenError(err)
		}

		ctx := domain.ContextWithClaim(ss.Context(), *claims)
		ctx = domain.ContextWithRequestMeta(ctx, requestMeta(ctx, md, i.proxies))

		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

func tokenError(err error) error {
	if domain.IsServerError(err) {
		slog.Error("token validation failed", "error", err)
		return status.Error(codes.Internal, "internal server error")
	}
	return status.Error(codes.Unauthenticated, "invalid token")
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return "", false
	}
	return token, true
}
