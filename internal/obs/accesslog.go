package obs

import (
	"net/http"
	"strings"
	"time"
)

// AccessLog logs one line per HTTP request. Requests carrying gRPC content,
// health probes, metrics scrapes and pprof access are skipped. When quiet
// reports true for a successful request (the embedded SPA assets), the line is
// emitted at debug instead of info.
func AccessLog(quiet func(*http.Request) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skipAccessLog(r) {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		logger := Log(r.Context())
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"proto", r.Proto,
			"remote", r.RemoteAddr,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		}
		switch {
		case rec.status >= http.StatusInternalServerError:
			logger.Error("http request", attrs...)
		case rec.status >= http.StatusBadRequest:
			logger.Warn("http request", attrs...)
		case quiet != nil && quiet(r):
			logger.Debug("http request", attrs...)
		default:
			logger.Info("http request", attrs...)
		}
	})
}

// IsGRPCRequest reports whether the shared mux dispatches the request to the
// gRPC handler: h2c traffic with a gRPC content type. HTTP/1.1 requests never
// reach gRPC even when they carry the content type.
func IsGRPCRequest(r *http.Request) bool {
	return r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc")
}

func skipAccessLog(r *http.Request) bool {
	if IsGRPCRequest(r) {
		return true
	}
	p := r.URL.Path
	if p == "/healthz" || p == "/readyz" || p == "/metrics" {
		return true
	}
	return strings.HasPrefix(p, "/debug/pprof")
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.status = http.StatusOK
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Flush exposes the underlying flusher so streaming responses over HTTP/1.1
// and HTTP/2 keep working through the wrapper.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer (flush,
// deadline and hijack helpers).
func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
