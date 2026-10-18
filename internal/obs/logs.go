package obs

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// LogsConfig configures SetupLogs.
type LogsConfig struct {
	Enabled        bool
	Endpoint       string
	Protocol       string
	Headers        map[string]string
	ServiceName    string
	ServiceVersion string
}

// Logs owns the OpenTelemetry logger provider that ships structured logs over
// OTLP. The zero value (and a nil receiver) is a disabled no-op so callers
// never need to check.
type Logs struct {
	provider *sdklog.LoggerProvider
	handler  slog.Handler
}

// SetupLogs installs the OTLP log exporter and returns the slog bridge that
// feeds it. A disabled config returns a no-op handle.
func SetupLogs(ctx context.Context, cfg LogsConfig) (*Logs, error) {
	if !cfg.Enabled {
		return &Logs{}, nil
	}

	protocol, err := resolveOTLPProtocol(cfg.Protocol)
	if err != nil {
		return nil, err
	}

	var exporter sdklog.Exporter
	if protocol == otlpProtocolHTTP {
		exporter, err = otlploghttp.New(ctx, resolveOTLPEndpoint(cfg.Endpoint, defaultOTLPHTTPEndpoint, envOTLPLogsEndpoint).logHTTPOptions(cfg.Headers)...)
	} else {
		exporter, err = otlploggrpc.New(ctx, resolveOTLPEndpoint(cfg.Endpoint, defaultOTLPEndpoint, envOTLPLogsEndpoint).logOptions(cfg.Headers)...)
	}
	if err != nil {
		return nil, err
	}

	res, err := telemetryResource(resolveServiceName(cfg.ServiceName), cfg.ServiceVersion)
	if err != nil {
		return nil, err
	}

	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)
	return &Logs{
		provider: provider,
		handler:  otelslog.NewHandler(tracerScope, otelslog.WithLoggerProvider(provider)),
	}, nil
}

// Enabled reports whether log records are shipped over OTLP.
func (l *Logs) Enabled() bool {
	return l != nil && l.provider != nil
}

// Handler returns the slog handler that ships records over OTLP, or nil when
// disabled. Records logged through a context carry its trace and span IDs.
func (l *Logs) Handler() slog.Handler {
	if !l.Enabled() {
		return nil
	}
	return l.handler
}

// Shutdown flushes pending records and releases the provider.
func (l *Logs) Shutdown(ctx context.Context) error {
	if !l.Enabled() {
		return nil
	}
	return l.provider.Shutdown(ctx)
}
