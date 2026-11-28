package obs

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func scrapeMetrics(t *testing.T, m *Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	return rr.Body.String()
}

func TestMetrics_ObserveHTTP(t *testing.T) {
	m := NewMetrics()
	m.ObserveHTTP("/api/v1/orgs/{org}", "GET", 200, 15*time.Millisecond)
	m.ObserveHTTP("/api/v1/orgs/{org}", "GET", 500, 2*time.Second)

	require.Equal(t, 1.0, testutil.ToFloat64(m.httpRequests.WithLabelValues("/api/v1/orgs/{org}", "GET", "200")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.httpRequests.WithLabelValues("/api/v1/orgs/{org}", "GET", "500")))

	body := scrapeMetrics(t, m)
	require.Contains(t, body, "nipa_http_request_duration_seconds_bucket")
	require.Contains(t, body, "nipa_http_requests_total")
}

func TestMetrics_ObserveGRPC(t *testing.T) {
	m := NewMetrics()
	m.ObserveGRPC("/greet.NipaService/GetCommit", "OK", 10*time.Millisecond)
	m.ObserveGRPC("/greet.NipaService/GetCommit", "NotFound", 5*time.Millisecond)

	require.Equal(t, 1.0, testutil.ToFloat64(m.grpcRequests.WithLabelValues("/greet.NipaService/GetCommit", "OK")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.grpcRequests.WithLabelValues("/greet.NipaService/GetCommit", "NotFound")))

	body := scrapeMetrics(t, m)
	require.Contains(t, body, "nipa_grpc_request_duration_seconds_count")
}

func TestMetrics_ObserveChunkPushMail(t *testing.T) {
	m := NewMetrics()
	m.ObserveChunkOp("put", "stored")
	m.ObserveChunkOp("get", "error")
	m.ObserveChunkBytes("put", 4096)
	m.ObserveChunkBytes("put", 0)
	m.ObserveChunkVerifyFailure("store")
	m.ObservePush("ok")
	m.ObserveWebhookDelivery("retry")
	m.ObserveMailDelivery("delivered")
	m.ObserveIssueTrackerRequest("jira", "link", "ok")
	m.SetIssueTrackerJobs("pending", 4)

	require.Equal(t, 1.0, testutil.ToFloat64(m.issueRequests.WithLabelValues("jira", "link", "ok")))
	require.Equal(t, 4.0, testutil.ToFloat64(m.issueJobs.WithLabelValues("pending")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.chunkOps.WithLabelValues("put", "stored")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.chunkOps.WithLabelValues("get", "error")))
	require.Equal(t, 4096.0, testutil.ToFloat64(m.chunkBytes.WithLabelValues("put")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.chunkVerifyFailures.WithLabelValues("store")))
	m.ObserveChunkGC("deleted", 3, 300)
	require.Equal(t, 3.0, testutil.ToFloat64(m.chunkGCChunks.WithLabelValues("deleted")))
	require.Equal(t, 300.0, testutil.ToFloat64(m.chunkGCBytes.WithLabelValues("deleted")))
	var nilMetrics *Metrics
	nilMetrics.ObserveChunkGC("deleted", 1, 1)
	require.Equal(t, 1.0, testutil.ToFloat64(m.push.WithLabelValues("ok")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.webhookDeliveries.WithLabelValues("retry")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.mailDeliveries.WithLabelValues("delivered")))
}

func TestMetrics_VisibleChunks(t *testing.T) {
	m := NewMetrics()
	m.ObserveVisibleChunksLookup("miss")
	m.ObserveVisibleChunksLookup("oversize")
	m.ObserveVisibleChunksLookup("oversize")
	m.ObserveVisibleChunksCompute("ok", 2*time.Second)

	require.Equal(t, 1.0, testutil.ToFloat64(m.visibleChunks.WithLabelValues("miss")))
	require.Equal(t, 2.0, testutil.ToFloat64(m.visibleChunks.WithLabelValues("oversize")))
	body := scrapeMetrics(t, m)
	require.Contains(t, body, `nipa_visible_chunks_cache_total{result="oversize"} 2`)
	require.Contains(t, body, `nipa_visible_chunks_compute_seconds_count{result="ok"} 1`)
}

func TestMetrics_RegisterGauges(t *testing.T) {
	m := NewMetrics()
	m.RegisterWebhookQueue(func() float64 { return 7 })
	m.RegisterWebhookInFlight(func() float64 { return 2 })
	m.RegisterMailQueue(func() float64 { return 3 })

	body := scrapeMetrics(t, m)
	require.Contains(t, body, "nipa_webhook_queue_depth 7")
	require.Contains(t, body, "nipa_webhook_in_flight 2")
	require.Contains(t, body, "nipa_mail_queue_depth 3")
}

func TestMetrics_RegisterDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.Ping())

	m := NewMetrics()
	m.RegisterDB("sqlite", db)

	body := scrapeMetrics(t, m)
	require.Contains(t, body, `nipa_db_pool_open_connections{db="sqlite"}`)
	require.Contains(t, body, `nipa_db_pool_wait_seconds_total{db="sqlite"}`)
}

func TestMetrics_NilSafety(t *testing.T) {
	var m *Metrics
	require.NotPanics(t, func() {
		m.ObserveHTTP("/", "GET", 200, time.Millisecond)
		m.ObserveGRPC("/x", "OK", time.Millisecond)
		m.ObserveChunkOp("put", "ok")
		m.ObserveChunkBytes("put", 1)
		m.ObserveChunkVerifyFailure("store")
		m.ObserveVisibleChunksLookup("hit")
		m.ObserveVisibleChunksCompute("ok", time.Second)
		m.ObservePush("ok")
		m.ObserveWebhookDelivery("delivered")
		m.ObserveMailDelivery("delivered")
		m.ObserveIssueTrackerRequest("jira", "link", "ok")
		m.SetIssueTrackerJobs("failed", 1)
		m.RegisterWebhookQueue(func() float64 { return 0 })
		m.RegisterWebhookInFlight(func() float64 { return 0 })
		m.RegisterMailQueue(func() float64 { return 0 })
		m.RegisterDB("sqlite", nil)
	})

	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestMetrics_ObserveRateLimit(t *testing.T) {
	m := NewMetrics()
	m.ObserveRateLimit("auth_ip", "limited")
	m.ObserveRateLimit("auth_ip", "limited")
	require.Equal(t, 2.0, testutil.ToFloat64(m.rateLimit.WithLabelValues("auth_ip", "limited")))
	require.Contains(t, scrapeMetrics(t, m), "nipa_ratelimit_total")

	var nilMetrics *Metrics
	require.NotPanics(t, func() { nilMetrics.ObserveRateLimit("x", "y") })
}

func TestMetrics_Maintenance(t *testing.T) {
	m := NewMetrics()
	m.SetMaintenanceServerMode("read_only", 1)
	m.SetMaintenanceServerMode("off", 0)
	m.SetMaintenanceWindows("project", "full", 2)
	m.ObserveMaintenancePollError()
	m.ObserveMaintenanceRejected("grpc", "write", "server")
	require.Equal(t, 1.0, testutil.ToFloat64(m.maintenanceRejects.WithLabelValues("grpc", "write", "server")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.maintenanceMode.WithLabelValues("read_only")))
	require.Equal(t, 2.0, testutil.ToFloat64(m.maintenanceWindows.WithLabelValues("project", "full")))
	require.Equal(t, 1.0, testutil.ToFloat64(m.maintenancePollErrs))
	body := scrapeMetrics(t, m)
	require.Contains(t, body, `nipa_maintenance_server_mode{mode="read_only"} 1`)
	require.Contains(t, body, `nipa_maintenance_windows{mode="full",scope="project"} 2`)
	require.Contains(t, body, "nipa_maintenance_poll_errors_total 1")

	var nilMetrics *Metrics
	require.NotPanics(t, func() {
		nilMetrics.SetMaintenanceServerMode("off", 1)
		nilMetrics.SetMaintenanceWindows("org", "full", 1)
		nilMetrics.ObserveMaintenancePollError()
		nilMetrics.ObserveMaintenanceRejected("http", "read", "org")
	})
}
