package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/obs"
)

func scrapeGRPCMetrics(t *testing.T, metrics *obs.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	return rr.Body.String()
}

func TestMetricsUnary(t *testing.T) {
	metrics := obs.NewMetrics()
	interceptor := MetricsUnary(metrics)
	info := &grpc.UnaryServerInfo{FullMethod: "/greet.NipaService/GetCommit"}

	_, err := interceptor(context.Background(), nil, info, func(context.Context, interface{}) (interface{}, error) {
		return "ok", nil
	})
	require.NoError(t, err)

	_, err = interceptor(context.Background(), nil, info, func(context.Context, interface{}) (interface{}, error) {
		return nil, status.Error(codes.NotFound, "nope")
	})
	require.Error(t, err)

	body := scrapeGRPCMetrics(t, metrics)
	require.Contains(t, body, `nipa_grpc_requests_total{code="OK",method="/greet.NipaService/GetCommit"} 1`)
	require.Contains(t, body, `nipa_grpc_requests_total{code="NotFound",method="/greet.NipaService/GetCommit"} 1`)
	require.Contains(t, body, "nipa_grpc_request_duration_seconds_count")
}

func TestMetricsStream(t *testing.T) {
	metrics := obs.NewMetrics()
	interceptor := MetricsStream(metrics)
	info := &grpc.StreamServerInfo{FullMethod: "/greet.NipaService/Push"}

	err := interceptor(nil, &fakeServerStream{ctx: context.Background()}, info, func(interface{}, grpc.ServerStream) error {
		return nil
	})
	require.NoError(t, err)

	err = interceptor(nil, &fakeServerStream{ctx: context.Background()}, info, func(interface{}, grpc.ServerStream) error {
		return status.Error(codes.Internal, "boom")
	})
	require.Error(t, err)

	body := scrapeGRPCMetrics(t, metrics)
	require.Contains(t, body, `nipa_grpc_requests_total{code="OK",method="/greet.NipaService/Push"} 1`)
	require.Contains(t, body, `nipa_grpc_requests_total{code="Internal",method="/greet.NipaService/Push"} 1`)
}
