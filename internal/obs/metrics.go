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
	chunkGCChunks       *prometheus.CounterVec
	chunkGCBytes        *prometheus.CounterVec
	visibleChunks       *prometheus.CounterVec
	visibleCompute      *prometheus.HistogramVec
	push                *prometheus.CounterVec
	webhookDeliveries   *prometheus.CounterVec
	mailDeliveries      *prometheus.CounterVec
	issueRequests       *prometheus.CounterVec
	issueJobs           *prometheus.GaugeVec
	rateLimit           *prometheus.CounterVec
	maintenanceMode     *prometheus.GaugeVec
	maintenanceWindows  *prometheus.GaugeVec
	maintenancePollErrs prometheus.Counter
	maintenanceRejects  *prometheus.CounterVec
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
		chunkGCChunks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "chunk_gc",
			Name:      "chunks_total",
			Help:      "Chunks the garbage collector marked, adopted (orphan objects), deleted or would delete (dry run), by phase.",
		}, []string{"phase"}),
		chunkGCBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "chunk_gc",
			Name:      "bytes_total",
			Help:      "Stored chunk bytes the garbage collector marked, adopted, deleted or would delete (dry run), by phase.",
		}, []string{"phase"}),
		visibleChunks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "visible_chunks",
			Name:      "cache_total",
			Help:      "Chunk download visibility cache lookups by result (hit, miss, oversize, error).",
		}, []string{"result"}),
		visibleCompute: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: metricsNamespace,
			Subsystem: "visible_chunks",
			Name:      "compute_seconds",
			Help:      "Time to compute one visible chunk set (tree walk) by result.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		}, []string{"result"}),
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
		issueRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "issue_tracker",
			Name:      "requests_total",
			Help:      "Issue tracker calls made by the outbox dispatcher by tracker kind, operation and result.",
		}, []string{"kind", "op", "result"}),
		issueJobs: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: "issue_tracker",
			Name:      "jobs",
			Help:      "Issue tracker outbox jobs by state.",
		}, []string{"state"}),
		rateLimit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "ratelimit",
			Name:      "total",
			Help:      "Rate limit decisions by scope and result.",
		}, []string{"scope", "result"}),
		maintenanceMode: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: "maintenance",
			Name:      "server_mode",
			Help:      "1 for the effective server maintenance mode (off included), 0 for the others.",
		}, []string{"mode"}),
		maintenanceWindows: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Subsystem: "maintenance",
			Name:      "windows",
			Help:      "Active org and project maintenance windows by scope and mode.",
		}, []string{"scope", "mode"}),
		maintenancePollErrs: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "maintenance",
			Name:      "poll_errors_total",
			Help:      "Failed maintenance state reloads.",
		}),
		maintenanceRejects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricsNamespace,
			Subsystem: "maintenance",
			Name:      "rejected_total",
			Help:      "Requests refused by maintenance by transport, class and scope.",
		}, []string{"transport", "class", "scope"}),
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
		m.chunkGCChunks,
		m.chunkGCBytes,
		m.visibleChunks,
		m.visibleCompute,
		m.push,
		m.webhookDeliveries,
		m.mailDeliveries,
		m.issueRequests,
		m.issueJobs,
		m.rateLimit,
		m.maintenanceMode,
		m.maintenanceWindows,
		m.maintenancePollErrs,
		m.maintenanceRejects,
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

// ObserveVisibleChunksLookup records one visibility cache lookup: hit, miss,
// oversize (computed but too big to keep) or error.
func (m *Metrics) ObserveVisibleChunksLookup(result string) {
	if m == nil {
		return
	}
	m.visibleChunks.WithLabelValues(result).Inc()
}

// ObserveVisibleChunksCompute records how long one visible set took to
// compute, by result (ok, error, timeout).
func (m *Metrics) ObserveVisibleChunksCompute(result string, d time.Duration) {
	if m == nil {
		return
	}
	m.visibleCompute.WithLabelValues(result).Observe(d.Seconds())
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

// ObserveChunkGC records chunks and bytes one garbage collector phase
// handled: marked, adopted, deleted or dry_run.
func (m *Metrics) ObserveChunkGC(phase string, chunks, bytes int64) {
	if m == nil {
		return
	}
	m.chunkGCChunks.WithLabelValues(phase).Add(float64(chunks))
	m.chunkGCBytes.WithLabelValues(phase).Add(float64(bytes))
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

// ObserveIssueTrackerRequest records one issue tracker call result.
func (m *Metrics) ObserveIssueTrackerRequest(kind, op, result string) {
	if m == nil {
		return
	}
	m.issueRequests.WithLabelValues(kind, op, result).Inc()
}

// SetIssueTrackerJobs sets the number of outbox jobs in a state.
func (m *Metrics) SetIssueTrackerJobs(state string, n int64) {
	if m == nil {
		return
	}
	m.issueJobs.WithLabelValues(state).Set(float64(n))
}

// ObserveRateLimit records one rate limit decision.
func (m *Metrics) ObserveRateLimit(scope, result string) {
	if m == nil {
		return
	}
	m.rateLimit.WithLabelValues(scope, result).Inc()
}

// SetMaintenanceServerMode sets the server mode gauge of one mode.
func (m *Metrics) SetMaintenanceServerMode(mode string, value float64) {
	if m == nil {
		return
	}
	m.maintenanceMode.WithLabelValues(mode).Set(value)
}

// SetMaintenanceWindows sets the number of active windows of a scope and mode.
func (m *Metrics) SetMaintenanceWindows(scope, mode string, n int) {
	if m == nil {
		return
	}
	m.maintenanceWindows.WithLabelValues(scope, mode).Set(float64(n))
}

// ObserveMaintenancePollError counts one failed maintenance reload.
func (m *Metrics) ObserveMaintenancePollError() {
	if m == nil {
		return
	}
	m.maintenancePollErrs.Inc()
}

// ObserveMaintenanceRejected counts one request refused by maintenance.
func (m *Metrics) ObserveMaintenanceRejected(transport, class, scope string) {
	if m == nil {
		return
	}
	m.maintenanceRejects.WithLabelValues(transport, class, scope).Inc()
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
