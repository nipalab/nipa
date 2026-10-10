package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// HeaderRequestID is the header carrying the request correlation ID.
const HeaderRequestID = "X-Request-ID"

const maxRequestIDLen = 128

type contextKey int

const (
	requestIDKey contextKey = iota
	loggerKey
)

// RequestID attaches a correlation ID to every request. An acceptable incoming
// X-Request-ID is honored so a fronting proxy can seed the ID; otherwise one is
// generated. The ID is echoed on the response and stored on the request
// context for logs and handlers.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(HeaderRequestID))
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set(HeaderRequestID, id)

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, loggerKey, slog.Default().With("request_id", id))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestIDFromContext returns the request correlation ID, or "" outside a
// request.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// Log returns the request-scoped logger (with the request ID attached), or the
// default logger when called outside a request.
func Log(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if logger, ok := ctx.Value(loggerKey).(*slog.Logger); ok && logger != nil {
			return logger
		}
	}
	return slog.Default()
}

func sanitizeRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxRequestIDLen {
		return ""
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return raw
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
