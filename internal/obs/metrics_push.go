package obs

import (
	"context"
	"time"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const defaultMetricsPushInterval = 30 * time.Second

// MetricsPushConfig configures SetupMetricsPush.
type MetricsPushConfig struct {
	Enabled        bool
	Endpoint       string
	Protocol       string
	Headers        map[string]string
	ServiceName    string
	ServiceVersion string
	Interval       time.Duration
}

// MetricsPush owns the OTLP metrics exporter that periodically pushes the
// Prometheus registry to a backend (OpenObserve). The zero value (and a nil
// receiver) is a disabled no-op.
type MetricsPush struct {
	provider *sdkmetric.MeterProvider
}

// SetupMetricsPush installs a periodic OTLP reader fed by the Prometheus
// registry through the OpenTelemetry Prometheus bridge. A disabled config (or
// a nil registry) returns a no-op handle.
func SetupMetricsPush(ctx context.Context, metrics *Metrics, cfg MetricsPushConfig) (*MetricsPush, error) {
	if !cfg.Enabled || metrics == nil {
		return &MetricsPush{}, nil
	}

	protocol, err := resolveOTLPProtocol(cfg.Protocol)
	if err != nil {
		return nil, err
	}

	var exporter sdkmetric.Exporter
	if protocol == otlpProtocolHTTP {
		exporter, err = otlpmetrichttp.New(ctx, resolveOTLPEndpoint(cfg.Endpoint, defaultOTLPHTTPEndpoint).metricHTTPOptions(cfg.Headers)...)
	} else {
		exporter, err = otlpmetricgrpc.New(ctx, resolveOTLPEndpoint(cfg.Endpoint, defaultOTLPEndpoint).metricOptions(cfg.Headers)...)
	}
	if err != nil {
		return nil, err
	}

	res, err := telemetryResource(resolveServiceName(cfg.ServiceName), cfg.ServiceVersion)
	if err != nil {
		return nil, err
	}

	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultMetricsPushInterval
	}
	reader := sdkmetric.NewPeriodicReader(exporter,
		sdkmetric.WithInterval(interval),
		sdkmetric.WithProducer(prombridge.NewMetricProducer(prombridge.WithGatherer(metrics.registry))),
	)
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))
	return &MetricsPush{provider: provider}, nil
}

// Enabled reports whether the registry is pushed over OTLP.
func (m *MetricsPush) Enabled() bool {
	return m != nil && m.provider != nil
}

// Shutdown flushes the pending push and releases the provider.
func (m *MetricsPush) Shutdown(ctx context.Context) error {
	if !m.Enabled() {
		return nil
	}
	return m.provider.Shutdown(ctx)
}
