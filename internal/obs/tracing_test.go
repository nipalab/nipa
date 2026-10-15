package obs

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func withRecordingProvider(t *testing.T) (*Tracing, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	return &Tracing{provider: provider, propagator: propagation.TraceContext{}}, recorder
}

func TestSetupTracing_Disabled(t *testing.T) {
	tracing, err := SetupTracing(context.Background(), TracingConfig{})
	require.NoError(t, err)
	require.False(t, tracing.Enabled())
	require.Same(t, tracing, ActiveTracing())

	_, span := StartSpan(context.Background(), "test")
	require.False(t, span.IsRecording())
	FinishSpan(span, nil)

	require.NoError(t, tracing.Shutdown(context.Background()))
}

func TestSetupTracing_EnabledOTLP(t *testing.T) {
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	tracing, err := SetupTracing(context.Background(), TracingConfig{
		Enabled:        true,
		Endpoint:       "127.0.0.1:1",
		ServiceName:    "nipad-test",
		ServiceVersion: "v0.0.1",
	})
	require.NoError(t, err)
	require.True(t, tracing.Enabled())

	_, span := StartSpan(context.Background(), "test")
	require.True(t, span.IsRecording())
	FinishSpan(span, errors.New("boom"))

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_ = tracing.Shutdown(ctx)
}

func TestStartSpanAndFinish(t *testing.T) {
	tracing, recorder := withRecordingProvider(t)
	require.True(t, tracing.Enabled())

	_, span := StartSpan(context.Background(), "chunk.store",
		attribute.String(AttrChunkHash, "abcd"))
	FinishSpan(span, errors.New("boom"))

	ended := recorder.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, "chunk.store", ended[0].Name())
	require.Equal(t, codes.Error, ended[0].Status().Code)
	require.Equal(t, "boom", ended[0].Status().Description)
	require.Equal(t, "abcd", attributeValue(ended[0].Attributes(), AttrChunkHash))
}

func TestTracing_HTTPHandlerDisabled(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	handler := (&Tracing{}).HTTPHandler("nipa", next, nil)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil))
	require.True(t, called)
}

func TestTracing_HTTPHandlerRecordsRequests(t *testing.T) {
	tracing, recorder := withRecordingProvider(t)
	skip := func(r *http.Request) bool { return r.URL.Path == "/healthz" }
	handler := tracing.HTTPHandler("nipa", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), skip)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))

	ended := recorder.Ended()
	require.Len(t, ended, 1, "health probe requests must be skipped")
	require.Equal(t, "GET /api/v1/orgs", ended[0].Name())
	require.True(t, ended[0].Parent().IsValid(), "incoming traceparent must become the parent")
}

func TestTracing_GRPCOptionsWhenDisabled(t *testing.T) {
	tracing := &Tracing{}
	require.NotPanics(t, func() {
		_ = grpc.NewServer(tracing.GRPCServerOption())
		_, _ = grpc.NewClient("passthrough:///localhost:1", tracing.GRPCDialOption(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	})
}

func TestTracing_GRPCPropagation(t *testing.T) {
	serverRecorder := tracetest.NewSpanRecorder()
	serverTracing := &Tracing{
		provider:   sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(serverRecorder)),
		propagator: propagation.TraceContext{},
	}
	clientRecorder := tracetest.NewSpanRecorder()
	clientTracing := &Tracing{
		provider:   sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(clientRecorder)),
		propagator: propagation.TraceContext{},
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var serverTraceID atomic.Value
	registrar := grpc.NewServer(serverTracing.GRPCServerOption(), grpc.ChainUnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			serverTraceID.Store(trace.SpanContextFromContext(ctx).TraceID().String())
			return handler(ctx, req)
		}))
	healthpb.RegisterHealthServer(registrar, health.NewServer())
	go func() { _ = registrar.Serve(lis) }()
	t.Cleanup(registrar.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		clientTracing.GRPCDialOption(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, span := clientTracing.provider.Tracer("test").Start(context.Background(), "test.push")
	defer span.End()

	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)

	require.Equal(t, span.SpanContext().TraceID().String(), serverTraceID.Load(),
		"server span must continue the client trace")
	require.Eventually(t, func() bool {
		for _, ended := range serverRecorder.Ended() {
			if ended.SpanKind() != trace.SpanKindServer || ended.SpanContext().TraceID() != span.SpanContext().TraceID() {
				continue
			}
			if !strings.Contains(ended.Name(), "Health") {
				t.Errorf("server span name %q must carry the RPC method", ended.Name())
			}
			return true
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "server RPC span must be recorded")
}

func attributeValue(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}
