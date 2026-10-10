package server

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/obs"
)

// MetricsUnary records every unary RPC with its status code and duration.
func MetricsUnary(metrics *obs.Metrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		metrics.ObserveGRPC(info.FullMethod, status.Code(err).String(), time.Since(start))
		return resp, err
	}
}

// MetricsStream records every streaming RPC with its status code and duration.
func MetricsStream(metrics *obs.Metrics) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		metrics.ObserveGRPC(info.FullMethod, status.Code(err).String(), time.Since(start))
		return err
	}
}
