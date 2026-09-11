package server

import (
	"context"
	"log/slog"

	"github.com/nipalab/nipa/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type tokenValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*domain.Claims, error)
}

type Interceptor struct {
	tokenValidator tokenValidator
}

func NewInterceptor(tokenValidator tokenValidator) *Interceptor {
	return &Interceptor{
		tokenValidator: tokenValidator,
	}
}

func (i *Interceptor) JWTUnary() func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {

		slog.Info("method", "name", info.FullMethod)
		if info.FullMethod == "/greet.NipaService/LoginWithUsernamePassword" || info.FullMethod == "/greet.NipaService/LoginWithRefreshToken" || info.FullMethod == "/nipa.AuthService/health" {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeader := md["authorization"]
		if len(authHeader) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		tokenString := authHeader[0]
		if len(tokenString) < 7 || tokenString[:7] != "Bearer " {
			return nil, status.Error(codes.Unauthenticated, "invalid authorization header")
		}
		tokenString = tokenString[7:]

		claims, err := i.tokenValidator.ValidateToken(ctx, tokenString)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		ctx = domain.ContextWithClaim(ctx, *claims)

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
		if info.FullMethod == "/greet.NipaService/LoginWithUsernamePassword" || info.FullMethod == "/greet.NipaService/LoginWithRefreshToken" || info.FullMethod == "/nipa.AuthService/health" {
			return handler(srv, ss)
		}

		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeader := md["authorization"]
		if len(authHeader) == 0 {
			return status.Error(codes.Unauthenticated, "missing authorization header")
		}

		tokenString := authHeader[0]
		if len(tokenString) < 7 || tokenString[:7] != "Bearer " {
			return status.Error(codes.Unauthenticated, "invalid authorization header")
		}
		tokenString = tokenString[7:]

		claims, err := i.tokenValidator.ValidateToken(ss.Context(), tokenString)
		if err != nil {
			return status.Error(codes.Unauthenticated, "invalid token")
		}

		ctx := domain.ContextWithClaim(ss.Context(), *claims)

		return handler(srv, &wrappedStream{ServerStream: ss, ctx: ctx})
	}
}
