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

	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/grpc/pb"
	grpcserver "github.com/nipalab/nipa/internal/grpc/server"
	"github.com/nipalab/nipa/internal/http/api"
	webui "github.com/nipalab/nipa/web/server"
)

// Dispatcher is the background worker the server starts and drains around its
// lifecycle (the webhook delivery dispatcher).
type Dispatcher interface {
	Start()
	Stop(ctx context.Context) error
}

// Run serves the REST API, the gRPC service and the embedded web UI on the
// configured address until the process is signalled to shut down.
func Run(cfg *config.Config, reg *Registry, dispatcher Dispatcher) error {
	apiApp := api.NewAPI(reg)
	container := apiApp.SetupRoute()
	if dispatcher != nil {
		dispatcher.Start()
	}

	address := fmt.Sprintf("%s:%d", cfg.ServerAddress, cfg.ServerPort)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grpcInterceptor := grpcserver.NewInterceptor(reg.Auth())

	grpcRegistrar := grpc.NewServer(
		grpc.UnaryInterceptor(grpcInterceptor.JWTUnary()),
		grpc.StreamInterceptor(grpcInterceptor.JWTStream()),
	)
	nipaServer := grpcserver.New(reg)
	pb.RegisterNipaServiceServer(grpcRegistrar, nipaServer)

	webUI := webui.Handler()

	mainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("incoming connection", "content", r.Header.Get("Content-Type"), "method", r.Method, "url", r.URL.String(), "ProtoMajor", r.ProtoMajor)
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			slog.Info("grpc connection is coming")
			grpcRegistrar.ServeHTTP(w, r)
		} else if isAPIPath(r.URL.Path) {
			container.ServeHTTP(w, r)
		} else {
			webUI.ServeHTTP(w, r)
		}
	})

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	httpServer := &http.Server{Addr: address, Handler: mainHandler, Protocols: protocols}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		<-c
		slog.Info("shutting down server")
		if err := httpServer.Shutdown(ctx); err != nil {
			slog.Error("error shutting down server", "error", err)
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if dispatcher != nil {
			if err := dispatcher.Stop(shutdownCtx); err != nil {
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
