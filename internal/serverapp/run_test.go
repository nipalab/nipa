package serverapp

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/nipalab/nipa/internal/obs"
)

func TestSplitAllowlist(t *testing.T) {
	require.Nil(t, SplitAllowlist("   "))
	require.Equal(t, []string{"https://a.example"}, SplitAllowlist("https://a.example"))
	require.Equal(t, []string{"https://a.example", "https://b.example"}, SplitAllowlist("https://a.example,https://b.example"))
}

func TestIsAPIPath(t *testing.T) {
	require.True(t, isAPIPath("/auth"))
	require.True(t, isAPIPath("/auth/login"))
	require.True(t, isAPIPath("/docs"))
	require.True(t, isAPIPath("/docs/"))
	require.True(t, isAPIPath("/api"))
	require.True(t, isAPIPath("/api/v1/orgs"))
	require.False(t, isAPIPath("/author"))
	require.False(t, isAPIPath("/apiary"))
	require.False(t, isAPIPath("/docsmith"))
	require.False(t, isAPIPath("/repository/browse"))
}

func TestIsPprofPath(t *testing.T) {
	require.True(t, isPprofPath("/debug/pprof"))
	require.True(t, isPprofPath("/debug/pprof/"))
	require.True(t, isPprofPath("/debug/pprof/cmdline"))
	require.False(t, isPprofPath("/debug"))
	require.False(t, isPprofPath("/debug/pprofx"))
	require.False(t, isPprofPath("/repository/browse"))
}

func TestIsMarketingPath(t *testing.T) {
	require.True(t, isMarketingPath("/"))
	require.True(t, isMarketingPath("/web"))
	require.True(t, isMarketingPath("/web/"))
	require.True(t, isMarketingPath("/web/pricing"))
	require.False(t, isMarketingPath("/website"))
	require.False(t, isMarketingPath("/webhooks"))
	require.False(t, isMarketingPath("/acme"))
	require.False(t, isMarketingPath("/dashboard"))
}

func TestNewUIHandlerRoutesMarketing(t *testing.T) {
	var marketingHits, webHits []string
	marketing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		marketingHits = append(marketingHits, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	web := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		webHits = append(webHits, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	handler := newUIHandler(web, marketing)

	for _, path := range []string{"/", "/web", "/web/", "/web/pricing", "/web/assets/app.js"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNoContent, rec.Code)
	}
	for _, path := range []string{"/acme", "/acme/game", "/dashboard", "/login"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNoContent, rec.Code)
	}

	require.Equal(t, []string{"/", "/web", "/web/", "/web/pricing", "/web/assets/app.js"}, marketingHits)
	require.Equal(t, []string{"/acme", "/acme/game", "/dashboard", "/login"}, webHits)
}

func TestNewUIHandlerWithoutMarketing(t *testing.T) {
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	newUIHandler(web, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusNoContent, rec.Code)
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
		WithWebService(new(restful.WebService).Path("/api/webhooks/extra")),
	))
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

func TestNewHTTPServerTimeouts(t *testing.T) {
	srv := newHTTPServer(&config.Config{}, "127.0.0.1:1", http.NotFoundHandler())
	require.Equal(t, 30*time.Second, srv.ReadHeaderTimeout)
	require.Equal(t, 5*time.Minute, srv.IdleTimeout)
	require.Zero(t, srv.ReadTimeout, "a read timeout would cut chunk uploads and gRPC streams")
	require.Zero(t, srv.WriteTimeout, "a write timeout would cut chunk downloads and gRPC streams")
	require.True(t, srv.Protocols.UnencryptedHTTP2())
	require.True(t, srv.Protocols.HTTP1())

	srv = newHTTPServer(&config.Config{HTTPReadHeaderTimeoutSeconds: 7, HTTPIdleTimeoutSeconds: 120}, "127.0.0.1:1", http.NotFoundHandler())
	require.Equal(t, 7*time.Second, srv.ReadHeaderTimeout)
	require.Equal(t, 2*time.Minute, srv.IdleTimeout)
}

// On h2c the idle timeout closes a connection only once it carries no stream:
// a request longer than the timeout completes, the idle connection then closes.
func TestHTTPServerIdleTimeoutOnH2C(t *testing.T) {
	srv := newHTTPServer(&config.Config{}, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(600 * time.Millisecond)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.IdleTimeout = 200 * time.Millisecond
	var closed atomic.Int32
	srv.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed.Add(1)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: protocols}, Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)

	resp, err := client.Get("http://" + ln.Addr().String() + "/slow")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, 2, resp.ProtoMajor)
	require.Zero(t, closed.Load(), "an active stream keeps the connection open past the idle timeout")

	require.Eventually(t, func() bool { return closed.Load() == 1 }, 3*time.Second, 20*time.Millisecond)

	resp, err = client.Get("http://" + ln.Addr().String() + "/")
	require.NoError(t, err, "clients reconnect transparently after an idle close")
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestObservabilityRoutes(t *testing.T) {
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	pprof := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	status := func(h http.Handler, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}

	main, side := observabilityRoutes(metrics, pprof, false)
	require.Nil(t, side)
	require.Equal(t, http.StatusOK, status(main, "/metrics"))
	require.Equal(t, http.StatusAccepted, status(main, "/debug/pprof/"))
	require.Equal(t, http.StatusNotFound, status(main, "/other"))

	main, side = observabilityRoutes(metrics, pprof, true)
	require.Equal(t, http.StatusNotFound, status(main, "/metrics"))
	require.Equal(t, http.StatusNotFound, status(main, "/debug/pprof/"))
	require.Equal(t, http.StatusOK, status(side, "/metrics"))
	require.Equal(t, http.StatusAccepted, status(side, "/debug/pprof/cmdline"))
	require.Equal(t, http.StatusNotFound, status(side, "/healthz"))
}

func TestRunRejectsInvalidMetricsAddress(t *testing.T) {
	cfg := &config.Config{ServerAddress: "127.0.0.1", ServerPort: freePort(t), MetricsAddress: "not-an-address"}
	require.ErrorContains(t, Run(cfg, NewRegistry(Usecases{}), nil), "METRICS_ADDRESS")
}

func TestRunReturnsMetricsListenError(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = busy.Close() }()

	port := freePort(t)
	cfg := &config.Config{ServerAddress: "127.0.0.1", ServerPort: port, MetricsAddress: busy.Addr().String()}
	require.ErrorContains(t, Run(cfg, NewRegistry(Usecases{}), nil), "metrics listener")

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err, "the main listener is released when the metrics listener fails")
	_ = ln.Close()
}

func TestRunServesObservabilityOnSeparateListener(t *testing.T) {
	mainPort, metricsPort := freePort(t), freePort(t)
	cfg := &config.Config{
		ServerAddress:  "127.0.0.1",
		ServerPort:     mainPort,
		MetricsEnabled: true,
		PprofEnabled:   true,
		MetricsAddress: fmt.Sprint(metricsPort),
		PublicBaseURL:  "https://app.example.com",
	}
	signals := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() { done <- run(cfg, NewRegistry(Usecases{}), nil, signals, WithMetrics(obs.NewMetrics())) }()

	mainURL := fmt.Sprintf("http://127.0.0.1:%d", mainPort)
	metricsURL := fmt.Sprintf("http://127.0.0.1:%d", metricsPort)
	client := &http.Client{Timeout: 5 * time.Second}
	get := func(url string) int {
		resp, err := client.Get(url)
		if err != nil {
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	require.Eventually(t, func() bool { return get(mainURL+"/healthz") == http.StatusOK }, 5*time.Second, 20*time.Millisecond)

	require.Equal(t, http.StatusNotFound, get(mainURL+"/metrics"))
	require.Equal(t, http.StatusNotFound, get(mainURL+"/debug/pprof/"))
	require.Equal(t, http.StatusOK, get(metricsURL+"/metrics"))
	require.Equal(t, http.StatusOK, get(metricsURL+"/debug/pprof/"))
	require.Equal(t, http.StatusNotFound, get(metricsURL+"/healthz"))

	req, err := http.NewRequest(http.MethodOptions, mainURL+"/api/v1/auth/login", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, "https://app.example.com", resp.Header.Get("Access-Control-Allow-Origin"), "CORS follows PUBLIC_BASE_URL")

	signals <- syscall.SIGTERM
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after the shutdown signal")
	}
	_, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", metricsPort), 200*time.Millisecond)
	require.Error(t, err, "the metrics listener closes on shutdown")
}

func TestServeMetricsReturnsOnListenerFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, ln.Close())
	done := make(chan struct{})
	go func() {
		serveMetrics(&http.Server{Handler: http.NotFoundHandler()}, ln)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serveMetrics did not return on a closed listener")
	}
}
