package serverapp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/emicklei/go-restful/v3"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/config"
)

func TestSplitAllowlist(t *testing.T) {
	require.Nil(t, SplitAllowlist("   "))
	require.Equal(t, []string{"https://a.example"}, SplitAllowlist("https://a.example"))
	require.Equal(t, []string{"https://a.example", "https://b.example"}, SplitAllowlist("https://a.example,https://b.example"))
}

func TestIsAPIPath(t *testing.T) {
	require.True(t, isAPIPath("/auth/login"))
	require.True(t, isAPIPath("/docs"))
	require.True(t, isAPIPath("/api/v1/orgs"))
	require.False(t, isAPIPath("/repository/browse"))
}

func TestReadinessHandler_DrainsOnShutdown(t *testing.T) {
	draining := &atomic.Bool{}
	handler := readinessHandler(draining, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	draining.Store(true)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "draining")
}

func TestReadinessHandler_ReportsCheckFailure(t *testing.T) {
	draining := &atomic.Bool{}
	handler := readinessHandler(draining, func(context.Context) error {
		return errors.New("database is locked")
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// The listener closes as soon as Shutdown starts; the serve loop must not
// return before the drain finished or the process would exit mid-request.
func TestServeUntilShutdownWaitsForInFlightRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: mux}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	signals := make(chan os.Signal, 1)
	shutdownFinished := make(chan struct{})
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, srv.Shutdown(ctx))
		close(shutdownFinished)
	}

	done := make(chan error, 1)
	go func() { done <- serveUntilShutdown(ln, srv, signals, shutdown) }()

	client := &http.Client{Timeout: 10 * time.Second}
	status := make(chan int, 1)
	go func() {
		resp, err := client.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			status <- 0
			return
		}
		defer func() { _ = resp.Body.Close() }()
		status <- resp.StatusCode
	}()
	<-started

	signals <- syscall.SIGTERM

	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return false
		}
		return true
	}, 2*time.Second, 10*time.Millisecond, "the listener must close when the shutdown starts")

	select {
	case err := <-done:
		t.Fatalf("serveUntilShutdown returned before the in-flight request finished: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	close(release)
	require.Equal(t, http.StatusOK, <-status, "the in-flight request must complete during the drain")
	require.NoError(t, <-done)
	<-shutdownFinished
}

func TestRunReturnsListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	cfg := &config.Config{
		ServerAddress: "127.0.0.1",
		ServerPort:    ln.Addr().(*net.TCPAddr).Port,
	}

	require.Error(t, Run(cfg, NewRegistry(Usecases{}), nil,
		WithRoutes(func(ws *restful.WebService) {
			ws.Route(ws.GET("/extra").To(func(_ *restful.Request, resp *restful.Response) {
				resp.WriteHeader(204)
			}))
		}),
		WithPublicRoutes(func(ws *restful.WebService) {
			ws.Route(ws.GET("/extra-public").To(func(_ *restful.Request, resp *restful.Response) {
				resp.WriteHeader(204)
			}))
		}),
	))
}
