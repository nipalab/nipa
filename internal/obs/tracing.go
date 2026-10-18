package obs

import (
	"context"
	"net/http"
	"os"
	"sync/atomic"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	experimental "google.golang.org/grpc/experimental/opentelemetry"
	otelrpcs "google.golang.org/grpc/stats/opentelemetry"
)

const tracerScope = "github.com/nipalab/nipa"

// Span attribute names shared by client and server instrumentation.
const (
	AttrChunkHash    = "nipa.chunk.hash"
	AttrChunkSize    = "nipa.chunk.size"
	AttrChunkCount   = "nipa.chunk.count"
	AttrChunkBytes   = "nipa.chunk.bytes"
	AttrChunkMissing = "nipa.chunk.missing"
	AttrOrg          = "nipa.org"
	AttrProject      = "nipa.project"
	AttrProjectID    = "nipa.project.id"
	AttrBranch       = "nipa.branch"
	AttrBackend      = "nipa.storage.backend"
	AttrPushFiles    = "nipa.push.files"
	AttrPushRemoved  = "nipa.push.removed"
)

// TracingConfig configures SetupTracing.
type TracingConfig struct {
	Enabled        bool
	Endpoint       string
	Protocol       string
	Headers        map[string]string
	Stdout         bool
	SampleRatio    float64
	ServiceName    string
	ServiceVersion string
}

// Tracing owns the process-wide OpenTelemetry tracer provider. The zero value
// (and a nil receiver) is a disabled no-op so callers never need to check.
type Tracing struct {
	provider   *sdktrace.TracerProvider
	propagator propagation.TextMapPropagator
}

var activeTracing atomic.Pointer[Tracing]

// SetupTracing installs the process-wide tracer provider and W3C trace
// propagation, returning a handle for middleware and shutdown. A disabled
// config leaves the process on the no-op provider.
func SetupTracing(ctx context.Context, cfg TracingConfig) (*Tracing, error) {
	if !cfg.Enabled {
		t := &Tracing{}
		activeTracing.Store(t)
		return t, nil
	}

	serviceName := resolveServiceName(cfg.ServiceName)

	protocol, err := resolveOTLPProtocol(cfg.Protocol)
	if err != nil {
		return nil, err
	}

	var exporter sdktrace.SpanExporter
	if cfg.Stdout {
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint(), stdouttrace.WithWriter(os.Stderr))
	} else if protocol == otlpProtocolHTTP {
		exporter, err = otlptracehttp.New(ctx, resolveOTLPEndpoint(cfg.Endpoint, defaultOTLPHTTPEndpoint).traceHTTPOptions(cfg.Headers)...)
	} else {
		exporter, err = otlptracegrpc.New(ctx, resolveOTLPEndpoint(cfg.Endpoint, defaultOTLPEndpoint).traceOptions(cfg.Headers)...)
	}
	if err != nil {
		return nil, err
	}

	res, err := telemetryResource(serviceName, cfg.ServiceVersion)
	if err != nil {
		return nil, err
	}

	ratio := cfg.SampleRatio
	if ratio <= 0 || ratio > 1 {
		ratio = 1
	}

	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagator)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		Log(ctx).Warn("opentelemetry error", "error", err)
	}))

	t := &Tracing{provider: provider, propagator: propagator}
	activeTracing.Store(t)
	return t, nil
}

// ActiveTracing returns the process-wide tracing handle installed by
// SetupTracing, or a disabled handle when tracing was never configured.
func ActiveTracing() *Tracing {
	if t := activeTracing.Load(); t != nil {
		return t
	}
	return &Tracing{}
}

// Enabled reports whether spans are recorded.
func (t *Tracing) Enabled() bool {
	return t != nil && t.provider != nil
}

// GRPCServerOption instruments the gRPC server with the configured provider.
func (t *Tracing) GRPCServerOption() grpc.ServerOption {
	var opts otelrpcs.Options
	if t.Enabled() {
		opts.TraceOptions = experimental.TraceOptions{TracerProvider: t.provider, TextMapPropagator: t.propagator}
		// The stats handler names server spans "<Recv.><method>" but only
		// learns the method from the metrics handler's tag, so a no-op meter
		// provider is required for usable span names.
		opts.MetricsOptions = otelrpcs.MetricsOptions{MeterProvider: metricnoop.NewMeterProvider()}
	}
	return otelrpcs.ServerOption(opts)
}

// GRPCDialOption instruments gRPC client connections with the configured
// provider so outgoing calls carry the W3C trace context.
func (t *Tracing) GRPCDialOption() grpc.DialOption {
	var opts experimental.TraceOptions
	if t.Enabled() {
		opts = experimental.TraceOptions{TracerProvider: t.provider, TextMapPropagator: t.propagator}
	}
	return otelrpcs.DialOption(otelrpcs.Options{TraceOptions: opts})
}

// HTTPHandler wraps next so each request gets a server span. Requests for
// which skip returns true are served untouched. A disabled handle returns next.
func (t *Tracing) HTTPHandler(operation string, next http.Handler, skip func(*http.Request) bool) http.Handler {
	if !t.Enabled() {
		return next
	}
	opts := []otelhttp.Option{
		otelhttp.WithTracerProvider(t.provider),
		otelhttp.WithPropagators(t.propagator),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	}
	if skip != nil {
		opts = append(opts, otelhttp.WithFilter(func(r *http.Request) bool { return !skip(r) }))
	}
	return otelhttp.NewHandler(next, operation, opts...)
}

// Shutdown flushes pending spans and releases the provider.
func (t *Tracing) Shutdown(ctx context.Context) error {
	if !t.Enabled() {
		return nil
	}
	return t.provider.Shutdown(ctx)
}

// StartSpan starts a span from the process-wide provider; no-op when tracing
// is disabled.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(tracerScope).Start(ctx, name, trace.WithAttributes(attrs...))
}

// FinishSpan records err (when non-nil) and ends the span.
func FinishSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
