package obs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func useDiscardLogger(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

func TestHealthHandler(t *testing.T) {
	rr := httptest.NewRecorder()
	HealthHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ok\n", rr.Body.String())
}

func TestReadinessHandler_NilCheck(t *testing.T) {
	rr := httptest.NewRecorder()
	ReadinessHandler(nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ready\n", rr.Body.String())
}

func TestReadinessHandler_Check(t *testing.T) {
	useDiscardLogger(t)

	ok := ReadinessHandler(func(context.Context) error { return nil })
	rr := httptest.NewRecorder()
	ok.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	failing := ReadinessHandler(func(context.Context) error { return errors.New("database is locked") })
	rr = httptest.NewRecorder()
	failing.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Equal(t, "not ready\n", rr.Body.String())
}
