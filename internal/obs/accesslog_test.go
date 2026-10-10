package obs

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordStore struct {
	mu      sync.Mutex
	records []slog.Record
}

type recordCollector struct {
	store *recordStore
	attrs []slog.Attr
}

func newRecordCollector() *recordCollector {
	return &recordCollector{store: &recordStore{}}
}

func (c *recordCollector) Enabled(context.Context, slog.Level) bool { return true }

func (c *recordCollector) Handle(_ context.Context, r slog.Record) error {
	r.AddAttrs(c.attrs...)
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	c.store.records = append(c.store.records, r.Clone())
	return nil
}

func (c *recordCollector) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordCollector{store: c.store, attrs: append(append([]slog.Attr{}, c.attrs...), attrs...)}
}

func (c *recordCollector) WithGroup(string) slog.Handler { return c }

func (c *recordCollector) records() []slog.Record {
	c.store.mu.Lock()
	defer c.store.mu.Unlock()
	return append([]slog.Record{}, c.store.records...)
}

func attrsOf(r slog.Record) map[string]any {
	attrs := map[string]any{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	return attrs
}

func installCollector(t *testing.T) *recordCollector {
	t.Helper()
	collector := newRecordCollector()
	prev := slog.Default()
	slog.SetDefault(slog.New(collector))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return collector
}

func TestAccessLog_Levels(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   slog.Level
	}{
		{"success", http.StatusCreated, slog.LevelInfo},
		{"client error", http.StatusNotFound, slog.LevelWarn},
		{"server error", http.StatusInternalServerError, slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := installCollector(t)
			h := AccessLog(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			}))

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/orgs?token=secret", nil))

			records := collector.records()
			require.Len(t, records, 1)
			require.Equal(t, tt.want, records[0].Level)

			attrs := attrsOf(records[0])
			require.Equal(t, "GET", attrs["method"])
			require.Equal(t, "/api/v1/orgs", attrs["path"])
			require.Equal(t, int64(tt.status), attrs["status"])
			require.Equal(t, int64(0), attrs["bytes"])
		})
	}
}

func TestAccessLog_CountsBytesAndDuration(t *testing.T) {
	collector := installCollector(t)
	h := AccessLog(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))

	attrs := attrsOf(collector.records()[0])
	require.Equal(t, int64(5), attrs["bytes"])
	require.Equal(t, int64(http.StatusOK), attrs["status"])
	require.Contains(t, attrs, "duration_ms")
}

func TestAccessLog_QuietAtDebug(t *testing.T) {
	collector := installCollector(t)
	h := AccessLog(func(*http.Request) bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/index.js", nil))

	records := collector.records()
	require.Len(t, records, 1)
	require.Equal(t, slog.LevelDebug, records[0].Level)
}

func TestAccessLog_QuietDoesNotHideErrors(t *testing.T) {
	collector := installCollector(t)
	h := AccessLog(func(*http.Request) bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/assets/index.js", nil))

	require.Equal(t, slog.LevelError, collector.records()[0].Level)
}

func TestAccessLog_Skips(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		headers map[string]string
	}{
		{"healthz", "/healthz", nil},
		{"readyz", "/readyz", nil},
		{"metrics", "/metrics", nil},
		{"pprof", "/debug/pprof/heap", nil},
		{"grpc", "/greet.NipaService/GetCommit", map[string]string{"Content-Type": "application/grpc+proto"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := installCollector(t)
			called := false
			h := AccessLog(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))

			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)

			require.True(t, called)
			require.Empty(t, collector.records())
		})
	}
}

func TestAccessLog_RequestIDFromContext(t *testing.T) {
	collector := installCollector(t)
	h := RequestID(AccessLog(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/x", nil)
	req.Header.Set(HeaderRequestID, "req-99")
	h.ServeHTTP(httptest.NewRecorder(), req)

	attrs := attrsOf(collector.records()[0])
	require.Equal(t, "req-99", attrs["request_id"])
}
