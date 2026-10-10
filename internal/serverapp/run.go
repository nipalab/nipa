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
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/grpc/pb"
	grpcserver "github.com/nipalab/nipa/internal/grpc/server"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/obs"
	webui "github.com/nipalab/nipa/web/server"
)

const (
	shutdownTimeout = 15 * time.Second
	dispatcherDrain = 10 * time.Second
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
	readiness func(context.Context) error
	metrics   *obs.Metrics
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

// Run serves the REST API, the gRPC service and the embedded web UI on the
// configured address until the process is signalled to shut down. Background
// dispatchers are started before the listener and drained on shutdown.
func Run(cfg *config.Config, reg *Registry, dispatchers []Dispatcher, opts ...Option) error {
	var ro runOptions
	for _, opt := range opts {
		opt(&ro)
	}

	apiApp := api.NewAPI(reg, api.WithMetrics(ro.metrics))
	container := apiApp.SetupRoute()
	for _, dispatcher := range dispatchers {
		if dispatcher != nil {
			dispatcher.Start()
		}
	}

	address := fmt.Sprintf("%s:%d", cfg.ServerAddress, cfg.ServerPort)

	grpcInterceptor := grpcserver.NewInterceptor(reg.Auth())

	grpcRegistrar := grpc.NewServer(
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
	readyHandler := obs.ReadinessHandler(ro.readiness)
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

	handler := obs.RequestID(obs.AccessLog(func(r *http.Request) bool {
		return !isAPIPath(r.URL.Path)
	}, mainHandler))

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	httpServer := &http.Server{Addr: address, Handler: handler, Protocols: protocols}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		<-c
		slog.Info("shutting down server")
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
		os.Exit(0)
	}()

	slog.Info("server is running", "address", address)
	if err := httpServer.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
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
