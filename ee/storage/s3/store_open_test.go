package s3

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	return recorder
}

func endedSpans(recorder *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			out = append(out, span)
		}
	}
	return out
}

func TestStore_OpenStreamsWithOneGet(t *testing.T) {
	recorder := recordSpans(t)
	store, fake := newTestStore(t)
	ctx := context.Background()

	data := bytes.Repeat([]byte("stream me "), 10_000)
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject, hash, data))
	fake.gets.Store(0)
	fake.heads.Store(0)

	r, err := store.Open(ctx, testProject, hash)
	require.NoError(t, err)
	require.Empty(t, endedSpans(recorder, "storage.s3.get_object"), "the span lasts until the reader is closed")
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.NoError(t, r.Close(), "closing twice is harmless")
	require.Equal(t, data, got)
	require.Equal(t, int64(1), fake.gets.Load())
	require.Zero(t, fake.heads.Load(), "opening must not add a HEAD")

	spans := endedSpans(recorder, "storage.s3.get_object")
	require.Len(t, spans, 1, "the span ends once")
	require.NotEqual(t, codes.Error, spans[0].Status().Code)
}

func TestStore_OpenMissingIsNotFound(t *testing.T) {
	recorder := recordSpans(t)
	store, fake := newTestStore(t)

	_, err := store.Open(context.Background(), testProject, chunker.Sum([]byte("absent")))
	require.True(t, domain.IsErrorNotFound(err), "got %v", err)
	require.Equal(t, int64(1), fake.gets.Load())
	require.Len(t, endedSpans(recorder, "storage.s3.get_object"), 1)
}

func TestStore_OpenError(t *testing.T) {
	recorder := recordSpans(t)
	store, fake := newTestStore(t)
	fake.failGet.Store(true)

	_, err := store.Open(context.Background(), testProject, chunker.Sum([]byte("x")))
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
	spans := endedSpans(recorder, "storage.s3.get_object")
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code)
}

func TestStore_OpenTruncatedBodyFailsTheRead(t *testing.T) {
	recorder := recordSpans(t)
	store, fake := newTestStore(t)
	ctx := context.Background()

	data := bytes.Repeat([]byte("truncated "), 1000)
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject, hash, data))
	fake.truncateGet.Store(true)

	r, err := store.Open(ctx, testProject, hash)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.Error(t, err, "a body shorter than its Content-Length is a read error, not a short chunk")
	require.False(t, domain.IsErrorNotFound(err))
	require.NoError(t, r.Close())

	spans := endedSpans(recorder, "storage.s3.get_object")
	require.Len(t, spans, 1)
	require.Equal(t, codes.Error, spans[0].Status().Code)
}
