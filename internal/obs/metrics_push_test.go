package obs

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestSetupMetricsPush_Disabled(t *testing.T) {
	push, err := SetupMetricsPush(context.Background(), NewMetrics(), MetricsPushConfig{})
	require.NoError(t, err)
	require.False(t, push.Enabled())
	require.NoError(t, push.Shutdown(context.Background()))

	push, err = SetupMetricsPush(context.Background(), nil, MetricsPushConfig{Enabled: true})
	require.NoError(t, err)
	require.False(t, push.Enabled(), "a nil registry must disable the push")

	var nilPush *MetricsPush
	require.False(t, nilPush.Enabled())
	require.NoError(t, nilPush.Shutdown(context.Background()))
}

func TestSetupMetricsPush_UnknownProtocol(t *testing.T) {
	_, err := SetupMetricsPush(context.Background(), NewMetrics(), MetricsPushConfig{Enabled: true, Protocol: "udp"})
	require.Error(t, err)
}

// metricsReceiver is an in-process OTLP metrics collector.
type metricsReceiver struct {
	collmetricspb.UnimplementedMetricsServiceServer

	mu       sync.Mutex
	requests []*collmetricspb.ExportMetricsServiceRequest
	headers  []metadata.MD
}

func (r *metricsReceiver) Export(ctx context.Context, req *collmetricspb.ExportMetricsServiceRequest) (*collmetricspb.ExportMetricsServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	r.headers = append(r.headers, md)
	return &collmetricspb.ExportMetricsServiceResponse{}, nil
}

func (r *metricsReceiver) snapshot() ([]*collmetricspb.ExportMetricsServiceRequest, []metadata.MD) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*collmetricspb.ExportMetricsServiceRequest(nil), r.requests...), append([]metadata.MD(nil), r.headers...)
}

func TestSetupMetricsPush_ExportsRegistryOverGRPC(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	receiver := &metricsReceiver{}
	registrar := grpc.NewServer()
	collmetricspb.RegisterMetricsServiceServer(registrar, receiver)
	go func() { _ = registrar.Serve(lis) }()
	t.Cleanup(registrar.Stop)

	metrics := NewMetrics()
	metrics.ObservePush("ok")
	metrics.ObservePush("ok")

	push, err := SetupMetricsPush(context.Background(), metrics, MetricsPushConfig{
		Enabled:     true,
		Endpoint:    lis.Addr().String(),
		Headers:     ParseHeaders("Authorization=Basic dXNlcjpwYXNz"),
		ServiceName: "nipad-test",
		Interval:    10 * time.Millisecond,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		requests, _ := receiver.snapshot()
		for _, req := range requests {
			if value, ok := sumValue(req, "nipa_push_total", "result", "ok"); ok && value == 2 {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the registry must be pushed")

	_, headers := receiver.snapshot()
	require.Contains(t, headers[0].Get("authorization"), "Basic dXNlcjpwYXNz")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, push.Shutdown(ctx))
}

func TestSetupMetricsPush_ExportsRegistryOverHTTP(t *testing.T) {
	var (
		mu       sync.Mutex
		paths    []string
		auths    []string
		payloads [][]byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		auths = append(auths, r.Header.Get("Authorization"))
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	metrics := NewMetrics()
	metrics.ObservePush("ok")

	push, err := SetupMetricsPush(context.Background(), metrics, MetricsPushConfig{
		Enabled:     true,
		Protocol:    "http",
		Endpoint:    srv.URL + "/api/default",
		Headers:     ParseHeaders("Authorization=Basic dXNlcjpwYXNz"),
		ServiceName: "nipad-test",
		Interval:    10 * time.Millisecond,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, payload := range payloads {
			var req collmetricspb.ExportMetricsServiceRequest
			if proto.Unmarshal(payload, &req) != nil {
				continue
			}
			if value, ok := sumValue(&req, "nipa_push_total", "result", "ok"); ok && value == 1 {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)

	mu.Lock()
	require.Equal(t, "/api/default/v1/metrics", paths[0], "the exporter must append /v1/metrics to the endpoint path")
	require.Equal(t, "Basic dXNlcjpwYXNz", auths[0])
	mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, push.Shutdown(ctx))
}

// sumValue finds a sum metric by name and returns the data point value whose
// attribute matches.
func sumValue(req *collmetricspb.ExportMetricsServiceRequest, name, attrKey, attrValue string) (float64, bool) {
	for _, resourceMetrics := range req.GetResourceMetrics() {
		for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
			for _, m := range scopeMetrics.GetMetrics() {
				if m.GetName() != name {
					continue
				}
				for _, point := range m.GetSum().GetDataPoints() {
					if attributeMatches(point.GetAttributes(), attrKey, attrValue) {
						return point.GetAsDouble(), true
					}
				}
			}
		}
	}
	return 0, false
}

func attributeMatches(attrs []*commonpb.KeyValue, key, value string) bool {
	for _, attr := range attrs {
		if attr.GetKey() == key && attr.GetValue().GetStringValue() == value {
			return true
		}
	}
	return false
}
