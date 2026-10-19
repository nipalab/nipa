package serverapp

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/emicklei/go-restful/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/grpc/pb"
	grpcserver "github.com/nipalab/nipa/internal/grpc/server"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/version"
	webui "github.com/nipalab/nipa/web/server"
)

const (
	defaultShutdownTimeout = 15 * time.Second
	dispatcherDrain        = 10 * time.Second
)

// Dispatcher is the background worker the server starts and drains around its
// lifecycle (the webhook delivery dispatcher).
type Dispatcher interface {
	Start()
	Stop(ctx context.Context) error
}

// Option tunes optional Run behavior.
type Option func(*runOptions)

type runOptions struct {
	readiness    func(context.Context) error
	metrics      *obs.Metrics
	tracing      *obs.Tracing
	logs         *obs.Logs
	metricsPush  *obs.MetricsPush
	routes       []func(*restful.WebService)
	publicRoutes []func(*restful.WebService)
}

// WithReadiness configures the check behind /readyz (typically a database
// ping). Without one, /readyz is always ready.
func WithReadiness(check func(context.Context) error) Option {
	return func(o *runOptions) {
		o.readiness = check
	}
}

// WithMetrics enables the /metrics endpoint and instruments HTTP and gRPC
// traffic with it.
func WithMetrics(metrics *obs.Metrics) Option {
	return func(o *runOptions) {
		o.metrics = metrics
	}
}

// WithTracing instruments gRPC and HTTP traffic with distributed traces. A
// disabled handle (or none) leaves the server on the no-op provider.
func WithTracing(tracing *obs.Tracing) Option {
	return func(o *runOptions) {
		o.tracing = tracing
	}
}

// WithLogs flushes the OTLP log exporter on shutdown. A disabled handle (or
// none) is a no-op.
func WithLogs(logs *obs.Logs) Option {
	return func(o *runOptions) {
		o.logs = logs
	}
}

// WithMetricsPush flushes the OTLP metrics exporter on shutdown. A disabled
// handle (or none) is a no-op.
func WithMetricsPush(metricsPush *obs.MetricsPush) Option {
	return func(o *runOptions) {
		o.metricsPush = metricsPush
	}
}

// WithRoutes registers extra routes on the authenticated /api/v1 WebService,
// e.g. the enterprise storage quota API. go-restful rejects a second
// WebService with the same root path, so extensions share the main one.
func WithRoutes(register func(*restful.WebService)) Option {
	return func(o *runOptions) {
		o.routes = append(o.routes, register)
	}
}

// WithPublicRoutes registers extra routes on the unauthenticated
// /api/v1/auth WebService, e.g. the SaaS registration and OAuth flows. Routes
// registered here are reachable without a bearer token.
func WithPublicRoutes(register func(*restful.WebService)) Option {
	return func(o *runOptions) {
		o.publicRoutes = append(o.publicRoutes, register)
	}
}

// Run serves the REST API, the gRPC service and the embedded web UI on the
// configured address until the process is signalled to shut down. Background
// dispatchers are started before the listener and drained on shutdown.
func Run(cfg *config.Config, reg *Registry, dispatchers []Dispatcher, opts ...Option) error {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	return run(cfg, reg, dispatchers, c, opts...)
}

// run is Run with an injectable signal source. It does not return until the
// graceful shutdown finished: Serve returns as soon as Shutdown closes the
// listener, so the process must not exit before the in-flight requests
// drained.
func run(cfg *config.Config, reg *Registry, dispatchers []Dispatcher, signals <-chan os.Signal, opts ...Option) error {
	var ro runOptions
	for _, opt := range opts {
		opt(&ro)
	}

	apiOpts := []api.Option{api.WithMetrics(ro.metrics)}
	for _, register := range ro.routes {
		apiOpts = append(apiOpts, api.WithRoutes(register))
	}
	for _, register := range ro.publicRoutes {
		apiOpts = append(apiOpts, api.WithPublicRoutes(register))
	}
	apiApp := api.NewAPI(reg, apiOpts...)
	container := apiApp.SetupRoute()
	for _, dispatcher := range dispatchers {
		if dispatcher != nil {
			dispatcher.Start()
		}
	}

	address := fmt.Sprintf("%s:%d", cfg.ServerAddress, cfg.ServerPort)
	shutdownTimeout := defaultShutdownTimeout
	if cfg.ShutdownTimeoutSeconds > 0 {
		shutdownTimeout = time.Duration(cfg.ShutdownTimeoutSeconds) * time.Second
	}

	grpcInterceptor := grpcserver.NewInterceptor(reg.Auth())

	grpcRegistrar := grpc.NewServer(
		ro.tracing.GRPCServerOption(),
		grpc.ChainUnaryInterceptor(grpcserver.MetricsUnary(ro.metrics), grpcInterceptor.JWTUnary()),
		grpc.ChainStreamInterceptor(grpcserver.MetricsStream(ro.metrics), grpcInterceptor.JWTStream()),
	)
	nipaServer := grpcserver.New(reg)
	pb.RegisterNipaServiceServer(grpcRegistrar, nipaServer)
	grpcHealth := health.NewServer()
	healthpb.RegisterHealthServer(grpcRegistrar, grpcHealth)
	grpcHealth.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)

	webUI := webui.Handler()

	healthHandler := obs.HealthHandler()
	draining := &atomic.Bool{}
	readyHandler := readinessHandler(draining, ro.readiness)
	versionHandler := version.Handler()
	pprofHandler := http.NotFoundHandler()
	if cfg.PprofEnabled {
		pprofHandler = obs.PprofHandler()
	}
	metricsHandler := http.NotFoundHandler()
	if cfg.MetricsEnabled {
		metricsHandler = ro.metrics.Handler()
	}

	mainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if obs.IsGRPCRequest(r) {
			grpcRegistrar.ServeHTTP(w, r)
			return
		}
		switch {
		case r.URL.Path == "/healthz":
			healthHandler.ServeHTTP(w, r)
		case r.URL.Path == "/readyz":
			readyHandler.ServeHTTP(w, r)
		case r.URL.Path == "/version":
			versionHandler.ServeHTTP(w, r)
		case r.URL.Path == "/metrics":
			metricsHandler.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/debug/pprof"):
			pprofHandler.ServeHTTP(w, r)
		case isAPIPath(r.URL.Path):
			container.ServeHTTP(w, r)
		default:
			webUI.ServeHTTP(w, r)
		}
	})

	handler := obs.RequestID(ro.tracing.HTTPHandler("nipa", obs.AccessLog(func(r *http.Request) bool {
		return !isAPIPath(r.URL.Path)
	}, mainHandler), skipTraceRequest))

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	httpServer := &http.Server{Addr: address, Handler: handler, Protocols: protocols}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	shutdown := func() {
		slog.Info("shutting down server")
		draining.Store(true)
		grpcHealth.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown incomplete", "error", err)
		}
		drainCtx, drainCancel := context.WithTimeout(context.Background(), dispatcherDrain)
		defer drainCancel()
		for _, dispatcher := range dispatchers {
			if dispatcher == nil {
				continue
			}
			if err := dispatcher.Stop(drainCtx); err != nil {
				slog.Warn("background dispatcher shutdown incomplete", "error", err)
			}
		}
		traceCtx, traceCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := ro.tracing.Shutdown(traceCtx); err != nil {
			slog.Warn("tracing shutdown incomplete", "error", err)
		}
		traceCancel()
		logsCtx, logsCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := ro.logs.Shutdown(logsCtx); err != nil {
			slog.Warn("logs shutdown incomplete", "error", err)
		}
		logsCancel()
		metricsCtx, metricsCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		if err := ro.metricsPush.Shutdown(metricsCtx); err != nil {
			slog.Warn("metrics push shutdown incomplete", "error", err)
		}
		metricsCancel()
	}

	slog.Info("server is running", "address", address, "version", version.Resolve().Version)
	return serveUntilShutdown(ln, httpServer, signals, shutdown)
}

// serveUntilShutdown serves on ln until a signal arrives, runs the graceful
// shutdown and only then returns. Serve returns as soon as Shutdown closes the
// listener, so returning at that point would exit the process while in-flight
// requests are still draining.
func serveUntilShutdown(ln net.Listener, srv *http.Server, signals <-chan os.Signal, shutdown func()) error {
	shutdownDone := make(chan struct{})
	go func() {
		<-signals
		shutdown()
		close(shutdownDone)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	<-shutdownDone
	return nil
}

// readinessHandler wraps the readiness check so /readyz answers 503 as soon as
// shutdown starts: proxies and load balancers polling it stop routing new
// traffic to the draining instance. Liveness (/healthz) stays 200 until the
// process actually exits.
func readinessHandler(draining *atomic.Bool, check func(context.Context) error) http.Handler {
	ready := obs.ReadinessHandler(check)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if draining.Load() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		ready.ServeHTTP(w, r)
	})
}

// SplitAllowlist turns the comma-separated WEBHOOK_EGRESS_ALLOWLIST into
// entries. Empty means no egress restriction.
func SplitAllowlist(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/auth") ||
		strings.HasPrefix(p, "/docs") ||
		strings.HasPrefix(p, "/api")
}

func skipTraceRequest(r *http.Request) bool {
	if obs.IsGRPCRequest(r) {
		return true
	}
	switch r.URL.Path {
	case "/healthz", "/readyz", "/version", "/metrics":
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/debug/pprof")
}
