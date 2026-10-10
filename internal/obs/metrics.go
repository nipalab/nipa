package obs

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsNamespace = "nipa"

// Metrics owns the server's Prometheus registry and instruments. All methods
// are nil-safe, so call sites can use a missing *Metrics as "disabled".
type Metrics struct {
	registry *prometheus.Registry

	httpRequests        *prometheus.CounterVec
	httpDuration        *prometheus.HistogramVec
	grpcRequests        *prometheus.CounterVec
	grpcDuration        *prometheus.HistogramVec
	chunkOps            *prometheus.CounterVec
	chunkBytes          *prometheus.CounterVec
	chunkVerifyFailures *prometheus.CounterVec
	push                *prometheus.CounterVec
	webhookDeliveries   *prometheus.CounterVec
	mailDeliveries      *prometheus.CounterVec
}

// NewMetrics builds an isolated registry with the standard Go and process
// collectors plus the Nipa collectors.
func NewMetrics() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Total HTTP requests by route template, method and status code.",
		}, []string{"route", "method", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request latency by route template and method.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"route", "method"}),
		grpcRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "grpc",
			Name:      "requests_total",
			Help:      "Total gRPC calls by full method and status code.",
		}, []string{"method", "code"}),
		grpcDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: "grpc",
			Name:      "request_duration_seconds",
			Help:      "gRPC call duration by full method.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method"}),
		chunkOps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "chunk",
			Name:      "ops_total",
			Help:      "Chunk transfer operations by operation and result.",
		}, []string{"op", "result"}),
		chunkBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "chunk",
			Name:      "bytes_total",
			Help:      "Chunk bytes stored, served or confirmed by operation.",
		}, []string{"op"}),
		chunkVerifyFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "chunk",
			Name:      "verify_failures_total",
			Help:      "Chunk content that failed hash verification, by stage.",
		}, []string{"stage"}),
		push: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "push",
			Name:      "total",
			Help:      "Pushes by result.",
		}, []string{"result"}),
		webhookDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "webhook",
			Name:      "deliveries_total",
			Help:      "Webhook deliveries by result.",
		}, []string{"result"}),
		mailDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "mail",
			Name:      "deliveries_total",
			Help:      "Email deliveries by result.",
		}, []string{"result"}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.httpRequests,
		m.httpDuration,
		m.grpcRequests,
		m.grpcDuration,
		m.chunkOps,
		m.chunkBytes,
		m.chunkVerifyFailures,
		m.push,
		m.webhookDeliveries,
		m.mailDeliveries,
	)
	return m
}

// Handler serves the Prometheus text exposition. A nil Metrics serves 404.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// ObserveHTTP records one HTTP request.
func (m *Metrics) ObserveHTTP(route, method string, code int, d time.Duration) {
	if m == nil {
		return
	}
	m.httpRequests.WithLabelValues(route, method, strconv.Itoa(code)).Inc()
	m.httpDuration.WithLabelValues(route, method).Observe(d.Seconds())
}

// ObserveGRPC records one unary or streaming gRPC call.
func (m *Metrics) ObserveGRPC(method, code string, d time.Duration) {
	if m == nil {
		return
	}
	m.grpcRequests.WithLabelValues(method, code).Inc()
	m.grpcDuration.WithLabelValues(method).Observe(d.Seconds())
}

// ObserveChunkOp records one chunk operation result.
func (m *Metrics) ObserveChunkOp(op, result string) {
	if m == nil {
		return
	}
	m.chunkOps.WithLabelValues(op, result).Inc()
}

// ObserveChunkBytes adds transferred chunk bytes for an operation.
func (m *Metrics) ObserveChunkBytes(op string, n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.chunkBytes.WithLabelValues(op).Add(float64(n))
}

// ObserveChunkVerifyFailure records chunk content that failed verification.
func (m *Metrics) ObserveChunkVerifyFailure(stage string) {
	if m == nil {
		return
	}
	m.chunkVerifyFailures.WithLabelValues(stage).Inc()
}

// ObservePush records one push result.
func (m *Metrics) ObservePush(result string) {
	if m == nil {
		return
	}
	m.push.WithLabelValues(result).Inc()
}

// ObserveWebhookDelivery records one webhook delivery result.
func (m *Metrics) ObserveWebhookDelivery(result string) {
	if m == nil {
		return
	}
	m.webhookDeliveries.WithLabelValues(result).Inc()
}

// ObserveMailDelivery records one email delivery result.
func (m *Metrics) ObserveMailDelivery(result string) {
	if m == nil {
		return
	}
	m.mailDeliveries.WithLabelValues(result).Inc()
}

// RegisterWebhookQueue exposes the webhook job queue depth.
func (m *Metrics) RegisterWebhookQueue(fn func() float64) {
	m.registerGaugeFunc("webhook_queue_depth", "Webhook deliveries waiting in the in-memory queue.", fn)
}

// RegisterWebhookInFlight exposes the number of webhook deliveries in flight.
func (m *Metrics) RegisterWebhookInFlight(fn func() float64) {
	m.registerGaugeFunc("webhook_in_flight", "Webhook deliveries currently being attempted.", fn)
}

// RegisterMailQueue exposes the email job queue depth.
func (m *Metrics) RegisterMailQueue(fn func() float64) {
	m.registerGaugeFunc("mail_queue_depth", "Email deliveries waiting in the in-memory queue.", fn)
}

// RegisterDB exposes database pool statistics for a named handle.
func (m *Metrics) RegisterDB(name string, db *sql.DB) {
	if m == nil || name == "" || db == nil {
		return
	}
	m.registry.MustRegister(newDBPoolCollector(name, db))
}

func (m *Metrics) registerGaugeFunc(name, help string, fn func() float64) {
	if m == nil || fn == nil {
		return
	}
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      name,
		Help:      help,
	}, fn))
}

type dbPoolCollector struct {
	db          *sql.DB
	open        *prometheus.Desc
	idle        *prometheus.Desc
	inUse       *prometheus.Desc
	waitCount   *prometheus.Desc
	waitSeconds *prometheus.Desc
}

func newDBPoolCollector(name string, db *sql.DB) *dbPoolCollector {
	labels := prometheus.Labels{"db": name}
	return &dbPoolCollector{
		db: db,
		open: prometheus.NewDesc(metricsNamespace+"_db_pool_open_connections",
			"Open connections in the database pool.", nil, labels),
		idle: prometheus.NewDesc(metricsNamespace+"_db_pool_idle_connections",
			"Idle connections in the database pool.", nil, labels),
		inUse: prometheus.NewDesc(metricsNamespace+"_db_pool_in_use_connections",
			"Connections currently in use.", nil, labels),
		waitCount: prometheus.NewDesc(metricsNamespace+"_db_pool_wait_count_total",
			"Total waits for a free database connection.", nil, labels),
		waitSeconds: prometheus.NewDesc(metricsNamespace+"_db_pool_wait_seconds_total",
			"Total time spent waiting for a free database connection.", nil, labels),
	}
}

func (c *dbPoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.open
	ch <- c.idle
	ch <- c.inUse
	ch <- c.waitCount
	ch <- c.waitSeconds
}

func (c *dbPoolCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.db.Stats()
	ch <- prometheus.MustNewConstMetric(c.open, prometheus.GaugeValue, float64(stats.OpenConnections))
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(stats.Idle))
	ch <- prometheus.MustNewConstMetric(c.inUse, prometheus.GaugeValue, float64(stats.InUse))
	ch <- prometheus.MustNewConstMetric(c.waitCount, prometheus.CounterValue, float64(stats.WaitCount))
	ch <- prometheus.MustNewConstMetric(c.waitSeconds, prometheus.CounterValue, stats.WaitDuration.Seconds())
}
