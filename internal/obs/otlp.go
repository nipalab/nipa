package obs

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.33.0"
)

const (
	defaultOTLPEndpoint     = "localhost:4317"
	defaultOTLPHTTPEndpoint = "localhost:4318"
)

// OTLP transport protocols.
const (
	otlpProtocolGRPC = "grpc"
	otlpProtocolHTTP = "http"
)

// resolveOTLPProtocol normalizes the configured OTLP transport. Empty means
// gRPC.
func resolveOTLPProtocol(configured string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(configured)) {
	case "", otlpProtocolGRPC:
		return otlpProtocolGRPC, nil
	case otlpProtocolHTTP:
		return otlpProtocolHTTP, nil
	default:
		return "", fmt.Errorf("unknown OTLP protocol %q (want grpc or http)", configured)
	}
}

// ParseHeaders parses a comma-separated list of KEY=VALUE OTLP headers, e.g.
// "Authorization=Basic dXNlcjpwYXNz,X-Scope=team". Only the first '=' in each
// entry separates key from value, so base64-padded values survive. Invalid or
// empty entries are skipped; an empty input yields nil.
func ParseHeaders(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var headers map[string]string
	for _, entry := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			continue
		}
		if headers == nil {
			headers = make(map[string]string, 1)
		}
		headers[key] = value
	}
	return headers
}

func otlpEndpointFromEnv() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// otlpEndpointTarget is a resolved collector target. An empty endpoint leaves
// the export configuration to the standard OTEL_* environment variables.
type otlpEndpointTarget struct {
	endpoint string
	insecure bool
}

// resolveOTLPEndpoint picks the collector target: the configured endpoint
// wins (a URL's scheme decides TLS, a bare host:port is plaintext), then the
// standard OTEL_* variables, then the fallback default. HTTP endpoints may
// carry a path prefix (OpenObserve uses /api/<org>); the exporters append
// /v1/traces or /v1/logs.
func resolveOTLPEndpoint(configured, fallback string) otlpEndpointTarget {
	configured = strings.TrimSpace(configured)
	switch {
	case configured == "":
		if otlpEndpointFromEnv() {
			return otlpEndpointTarget{}
		}
		return otlpEndpointTarget{endpoint: fallback, insecure: true}
	case strings.Contains(configured, "://"):
		return otlpEndpointTarget{endpoint: configured}
	default:
		return otlpEndpointTarget{endpoint: configured, insecure: true}
	}
}

// traceOptions builds the OTLP gRPC trace exporter options for the target,
// attaching the configured headers (e.g. an OpenObserve Authorization header).
func (t otlpEndpointTarget) traceOptions(headers map[string]string) []otlptracegrpc.Option {
	var opts []otlptracegrpc.Option
	switch {
	case t.endpoint == "":
	case t.insecure:
		opts = []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(t.endpoint), otlptracegrpc.WithInsecure()}
	default:
		opts = []otlptracegrpc.Option{otlptracegrpc.WithEndpointURL(t.endpoint)}
	}
	if len(headers) > 0 {
		opts = append(opts, otlptracegrpc.WithHeaders(headers))
	}
	return opts
}

// httpSignalURL appends the OTLP signal path (/v1/traces or /v1/logs) to a
// configured HTTP endpoint URL, following the OpenTelemetry collector
// convention so OpenObserve endpoints look like https://host/api/<org>. A
// trailing slash is trimmed and URLs that already end with the signal path
// are kept as-is.
func httpSignalURL(endpoint, signal string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	path := strings.TrimSuffix(u.Path, "/")
	suffix := "/v1/" + signal
	if !strings.HasSuffix(path, suffix) {
		path += suffix
	}
	u.Path = path
	return u.String()
}

// traceHTTPOptions builds the OTLP/HTTP trace exporter options for the target.
func (t otlpEndpointTarget) traceHTTPOptions(headers map[string]string) []otlptracehttp.Option {
	var opts []otlptracehttp.Option
	switch {
	case t.endpoint == "":
	case t.insecure:
		opts = []otlptracehttp.Option{otlptracehttp.WithEndpoint(t.endpoint), otlptracehttp.WithInsecure()}
	default:
		opts = []otlptracehttp.Option{otlptracehttp.WithEndpointURL(httpSignalURL(t.endpoint, "traces"))}
	}
	if len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	}
	return opts
}

// logOptions builds the OTLP gRPC log exporter options for the target.
func (t otlpEndpointTarget) logOptions(headers map[string]string) []otlploggrpc.Option {
	var opts []otlploggrpc.Option
	switch {
	case t.endpoint == "":
	case t.insecure:
		opts = []otlploggrpc.Option{otlploggrpc.WithEndpoint(t.endpoint), otlploggrpc.WithInsecure()}
	default:
		opts = []otlploggrpc.Option{otlploggrpc.WithEndpointURL(t.endpoint)}
	}
	if len(headers) > 0 {
		opts = append(opts, otlploggrpc.WithHeaders(headers))
	}
	return opts
}

// logHTTPOptions builds the OTLP/HTTP log exporter options for the target.
func (t otlpEndpointTarget) logHTTPOptions(headers map[string]string) []otlploghttp.Option {
	var opts []otlploghttp.Option
	switch {
	case t.endpoint == "":
	case t.insecure:
		opts = []otlploghttp.Option{otlploghttp.WithEndpoint(t.endpoint), otlploghttp.WithInsecure()}
	default:
		opts = []otlploghttp.Option{otlploghttp.WithEndpointURL(httpSignalURL(t.endpoint, "logs"))}
	}
	if len(headers) > 0 {
		opts = append(opts, otlploghttp.WithHeaders(headers))
	}
	return opts
}

// metricOptions builds the OTLP gRPC metrics exporter options for the target.
func (t otlpEndpointTarget) metricOptions(headers map[string]string) []otlpmetricgrpc.Option {
	var opts []otlpmetricgrpc.Option
	switch {
	case t.endpoint == "":
	case t.insecure:
		opts = []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(t.endpoint), otlpmetricgrpc.WithInsecure()}
	default:
		opts = []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpointURL(t.endpoint)}
	}
	if len(headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(headers))
	}
	return opts
}

// metricHTTPOptions builds the OTLP/HTTP metrics exporter options for the
// target.
func (t otlpEndpointTarget) metricHTTPOptions(headers map[string]string) []otlpmetrichttp.Option {
	var opts []otlpmetrichttp.Option
	switch {
	case t.endpoint == "":
	case t.insecure:
		opts = []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(t.endpoint), otlpmetrichttp.WithInsecure()}
	default:
		opts = []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(httpSignalURL(t.endpoint, "metrics"))}
	}
	if len(headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(headers))
	}
	return opts
}

// resolveServiceName defaults the telemetry service.name to "nipa" so both
// signals agree on one service identity.
func resolveServiceName(serviceName string) string {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return "nipa"
	}
	return serviceName
}

// telemetryResource builds the resource shared by the trace and log
// providers.
func telemetryResource(serviceName, serviceVersion string) (*resource.Resource, error) {
	return resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	))
}
