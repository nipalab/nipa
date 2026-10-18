package obs

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestSetupLogs_Disabled(t *testing.T) {
	logs, err := SetupLogs(context.Background(), LogsConfig{})
	require.NoError(t, err)
	require.False(t, logs.Enabled())
	require.Nil(t, logs.Handler())
	require.NoError(t, logs.Shutdown(context.Background()))

	var nilLogs *Logs
	require.False(t, nilLogs.Enabled())
	require.Nil(t, nilLogs.Handler())
	require.NoError(t, nilLogs.Shutdown(context.Background()))
}

func TestSetupLogs_EnabledOTLP(t *testing.T) {
	logs, err := SetupLogs(context.Background(), LogsConfig{
		Enabled:        true,
		Endpoint:       "127.0.0.1:1",
		ServiceName:    "nipad-test",
		ServiceVersion: "v0.0.1",
	})
	require.NoError(t, err)
	require.True(t, logs.Enabled())

	handler := logs.Handler()
	require.NotNil(t, handler)
	require.True(t, handler.Enabled(context.Background(), slog.LevelInfo))

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	require.NoError(t, handler.Handle(context.Background(), record))

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_ = logs.Shutdown(ctx)
}

func TestSetupLogs_HeadersOptionAccepted(t *testing.T) {
	logs, err := SetupLogs(context.Background(), LogsConfig{
		Enabled:  true,
		Endpoint: "https://collector.example.com:4317",
		Headers:  ParseHeaders("Authorization=Basic dXNlcjpwYXNz"),
	})
	require.NoError(t, err)
	require.True(t, logs.Enabled())

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_ = logs.Shutdown(ctx)
}

// logsReceiver is an in-process OTLP log collector.
type logsReceiver struct {
	collogspb.UnimplementedLogsServiceServer

	mu       sync.Mutex
	requests []*collogspb.ExportLogsServiceRequest
	headers  []metadata.MD
}

func (r *logsReceiver) Export(ctx context.Context, req *collogspb.ExportLogsServiceRequest) (*collogspb.ExportLogsServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	r.headers = append(r.headers, md)
	return &collogspb.ExportLogsServiceResponse{}, nil
}

func TestSetupLogs_ExportsRecordsWithHeaders(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	receiver := &logsReceiver{}
	registrar := grpc.NewServer()
	collogspb.RegisterLogsServiceServer(registrar, receiver)
	go func() { _ = registrar.Serve(lis) }()
	t.Cleanup(registrar.Stop)

	logs, err := SetupLogs(context.Background(), LogsConfig{
		Enabled:     true,
		Endpoint:    lis.Addr().String(),
		Headers:     ParseHeaders("Authorization=Basic dXNlcjpwYXNz"),
		ServiceName: "nipad-test",
	})
	require.NoError(t, err)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	require.NoError(t, logs.Handler().Handle(context.Background(), record))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, logs.Shutdown(ctx))

	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	require.Len(t, receiver.requests, 1)
	resourceLogs := receiver.requests[0].GetResourceLogs()
	require.Len(t, resourceLogs, 1)

	var serviceName string
	for _, attr := range resourceLogs[0].GetResource().GetAttributes() {
		if attr.GetKey() == "service.name" {
			serviceName = attr.GetValue().GetStringValue()
		}
	}
	require.Equal(t, "nipad-test", serviceName)

	logRecords := resourceLogs[0].GetScopeLogs()[0].GetLogRecords()
	require.Len(t, logRecords, 1)
	require.Equal(t, "hello", logRecords[0].GetBody().GetStringValue())
	require.Contains(t, receiver.headers[0].Get("authorization"), "Basic dXNlcjpwYXNz")
}

func TestSetupLogs_HonorsLogsEnvEndpoint(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	receiver := &logsReceiver{}
	registrar := grpc.NewServer()
	collogspb.RegisterLogsServiceServer(registrar, receiver)
	go func() { _ = registrar.Serve(lis) }()
	t.Cleanup(registrar.Stop)

	t.Setenv(envOTLPLogsEndpoint, "http://"+lis.Addr().String())

	logs, err := SetupLogs(context.Background(), LogsConfig{Enabled: true})
	require.NoError(t, err)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	require.NoError(t, logs.Handler().Handle(context.Background(), record))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, logs.Shutdown(ctx))

	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	require.NotEmpty(t, receiver.requests,
		"an endpoint-less config must defer to OTEL_EXPORTER_OTLP_LOGS_ENDPOINT instead of localhost")
}

func TestSetupLogs_ExportsOverHTTP(t *testing.T) {
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

	logs, err := SetupLogs(context.Background(), LogsConfig{
		Enabled:     true,
		Protocol:    "http",
		Endpoint:    srv.URL + "/api/default",
		Headers:     ParseHeaders("Authorization=Basic dXNlcjpwYXNz"),
		ServiceName: "nipad-test",
	})
	require.NoError(t, err)

	record := slog.NewRecord(time.Now(), slog.LevelInfo, "hello", 0)
	require.NoError(t, logs.Handler().Handle(context.Background(), record))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, logs.Shutdown(ctx))

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"/api/default/v1/logs"}, paths, "the exporter must append /v1/logs to the endpoint path")
	require.Equal(t, "Basic dXNlcjpwYXNz", auths[0])

	var req collogspb.ExportLogsServiceRequest
	require.NoError(t, proto.Unmarshal(payloads[0], &req))
	body := req.GetResourceLogs()[0].GetScopeLogs()[0].GetLogRecords()[0].GetBody().GetStringValue()
	require.Equal(t, "hello", body)
}
