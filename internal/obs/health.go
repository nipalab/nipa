package obs

import (
	"context"
	"io"
	"net/http"
	"time"
)

const readinessTimeout = time.Second

// HealthHandler answers liveness probes: 200 while the process is serving.
func HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writePlain(w, http.StatusOK, "ok\n")
	})
}

// ReadinessHandler answers readiness probes by running check (typically a
// database ping) under a short timeout. A nil check is always ready.
func ReadinessHandler(check func(context.Context) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
			defer cancel()
			if err := check(ctx); err != nil {
				Log(r.Context()).Warn("readiness check failed", "error", err)
				writePlain(w, http.StatusServiceUnavailable, "not ready\n")
				return
			}
		}
		writePlain(w, http.StatusOK, "ready\n")
	})
}

func writePlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}
