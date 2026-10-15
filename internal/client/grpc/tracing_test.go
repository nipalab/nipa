package grpc

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// withRecordingTracing installs a recording tracer provider and W3C
// propagation for the duration of the test.
func withRecordingTracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	})
	return recorder
}

func TestClient_UploadChunks_PropagatesTrace(t *testing.T) {
	h1 := testChunkHash(0x11)
	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)

	recorder := withRecordingTracing(t)

	var (
		mu      sync.Mutex
		headers []http.Header
	)
	client := NewClient(NewTransport(), &stubSession{accessToken: "tok"}, WithHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			mu.Lock()
			headers = append(headers, req.Header.Clone())
			mu.Unlock()
			return http.DefaultTransport.RoundTrip(req)
		}),
	}))
	require.NoError(t, client.Connect(context.Background(), addr))

	ctx, span := obs.StartSpan(context.Background(), "test.push")
	_, _, err := client.UploadChunks(ctx, domain.ChunkScope{Org: "org", Project: "proj"},
		[]*serverDomain.ChunkData{{Hash: h1, Data: []byte("aaaa")}})
	obs.FinishSpan(span, err)
	require.NoError(t, err)

	var uploadSpan sdktrace.ReadOnlySpan
	for _, ended := range recorder.Ended() {
		if ended.Name() == "chunk.upload.put" {
			uploadSpan = ended
		}
	}
	require.NotNil(t, uploadSpan, "expected a chunk.upload.put span")
	require.Equal(t, h1.String(), attributeValue(uploadSpan.Attributes(), obs.AttrChunkHash))
	require.Equal(t, span.SpanContext().TraceID(), uploadSpan.SpanContext().TraceID())

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, headers, 1)
	traceparent := headers[0].Get("traceparent")
	require.NotEmpty(t, traceparent, "chunk PUT must carry the trace context")

	extracted := otel.GetTextMapPropagator().Extract(context.Background(), propagation.HeaderCarrier(headers[0]))
	sc := trace.SpanContextFromContext(extracted)
	require.True(t, sc.IsValid(), "traceparent must parse")
	require.Equal(t, span.SpanContext().TraceID(), sc.TraceID())
}

func TestClient_DownloadChunks_PropagatesTrace(t *testing.T) {
	h1 := testChunkHash(0x12)
	srv := newChunkTestServer()
	srv.put(h1.String(), []byte("bbbb"))
	addr := startChunkTestServer(t, srv)

	recorder := withRecordingTracing(t)

	client := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, client.Connect(context.Background(), addr))

	contexts := 0
	err := client.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []serverDomain.Hash{h1},
		func(_ serverDomain.Hash, data []byte) error {
			contexts++
			require.Equal(t, []byte("bbbb"), data)
			return nil
		})
	require.NoError(t, err)
	require.Equal(t, 1, contexts)

	var downloadSpan sdktrace.ReadOnlySpan
	for _, ended := range recorder.Ended() {
		if ended.Name() == "chunk.download.get" {
			downloadSpan = ended
		}
	}
	require.NotNil(t, downloadSpan, "expected a chunk.download.get span")
	require.Equal(t, h1.String(), attributeValue(downloadSpan.Attributes(), obs.AttrChunkHash))
}

func attributeValue(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}
