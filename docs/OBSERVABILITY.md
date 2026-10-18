# Nipa Observability

Design and implementation plan for observability of the server binaries
(`nipad` free / `nipad-ee` enterprise): logging, request correlation, health
endpoints, runtime diagnostics, and metrics. The design keeps the existing
free/enterprise boundary (`internal/` never imports `ee/`) and wires everything
through the shared lifecycle in `internal/serverapp`, so both editions behave
identically.

**Scope decisions:**

- Server binaries only in this pass. The `nipa` CLI and `nipa serve` daemon are
  a later phase.
- Metrics use Prometheus (`/metrics` text format) as the primary stack; OTel
  tracing is an opt-in layer on top (see phase 3 and `docs/TRACING.md`).
- All runtime endpoints (`/metrics`, `/healthz`, `/readyz`, pprof) live on the
  existing server port. pprof is flag-gated and off by default.

**Status:** phase 1, phase 2, phase 3 and the phase 4 log shipping are
implemented (`internal/obs`, wired through `internal/serverapp` and both
mains); phase 5 adds direct OTLP metrics push. Tracing usage lives in
`docs/TRACING.md`; the OpenObserve deployment runbook is in phase 4 below.

---

## Current state (audit)

| Area | State |
|---|---|
| Logging | stdlib `log/slog`, default text handler to stderr; `LOG_LEVEL` is defined in `internal/config/config.go` but never read |
| Access logs | `internal/serverapp/run.go` logs every request at Info with the full URL (SPA assets, REST and gRPC all share one mux); no status, latency or request ID |
| gRPC | `internal/grpc/server/interceptor.go` logs only the method name at Info; keeps a stale `"/nipa.AuthService/health"` skip-list entry for an RPC that no longer exists |
| Metrics | none; no `/metrics`; no expvar; no `sql.DBStats` exposure |
| Tracing | none; `go.opentelemetry.io/otel` is only an indirect dependency of grpc/testcontainers |
| Health | no `/healthz` or `/readyz`; no gRPC health service |
| Diagnostics | no pprof; no dispatcher queue-depth/outcome visibility (webhook/mail dispatchers only `slog.Warn` on failures) |
| Shutdown | signal handler in `serverapp/run.go` calls `Shutdown` without a hard timeout and exits unconditionally |

Instrumentation seams (both binaries share them):

- `internal/serverapp/run.go` — mux, gRPC server construction, dispatcher
  lifecycle.
- `internal/http/api/api.go` — go-restful container; a container filter sees
  the selected route template, so metric labels stay low-cardinality.
- `internal/http/handler/chunk.go`, `internal/grpc/server/chunk.go`,
  `internal/usecase/chunk.go` — chunk transfer hot path.
- `internal/webhook/dispatcher.go`, `internal/mail/dispatcher.go` — background
  delivery.
- `db/sqlite.go` / `ee/db` — `*sql.DB` handles are available in both mains.

---

## Phase 1 — logging, request IDs, health, pprof (stdlib only)

### 1.1 New package `internal/obs`

Single injection seam for observability, shared by both mains:

- `logging.go` — `Setup(level, format, service, writer)`: parse `LOG_LEVEL`
  (unknown value warns and falls back to `info`), select handler from
  `LOG_FORMAT` (`console` text or `json`), install via `slog.SetDefault` once
  at startup. `service` is attached as a constant attribute (`nipad` /
  `nipad-ee`); a nil writer means stderr.
- `requestid.go` — `RequestID` middleware: accept an incoming `X-Request-ID`
  (trimmed and length-capped) or generate one (crypto/rand, 16 bytes hex),
  echo it on the response, and put `request_id` into the request context.
  `Log(ctx)` returns a logger carrying the request ID (falls back to the
  default logger outside a request).
- `accesslog.go` — `AccessLog(next http.Handler) http.Handler`: wraps the whole
  mux once, captures status and response bytes with a small hand-rolled
  `ResponseWriter` wrapper (no new dependency), and logs `method`, `path`
  (query string stripped — the signed chunk URLs must never be logged), `proto`,
  `remote`, `status`, `bytes`, `duration_ms`, `request_id`. Levels: Debug for
  non-API paths (SPA assets), Info for 2xx/3xx, Warn for 4xx, Error for 5xx.
  `/metrics`, `/healthz`, `/readyz` and `/debug/pprof*` are excluded.
- `health.go` — `/healthz` (liveness, always 200 while the process is up) and
  `/readyz` (readiness; runs an injectable checker, default `db.PingContext`
  with a 1s timeout).
- `pprof.go` — handler serving `net/http/pprof` routes
  (`/debug/pprof/{cmdline,profile,symbol,trace,heap,goroutine,allocs,block,mutex,threadcreate}`),
  enabled by `PPROF_ENABLED` (default false).

### 1.2 Configuration

`internal/config/config.go` gains:

- `LogLevel` (existing `LOG_LEVEL`) — now actually wired.
- `LogFormat` (`LOG_FORMAT`, default `console`, values `console|json`).
- `PprofEnabled` (`PPROF_ENABLED`, default false).
- `LogLevel` default `info` via `v.SetDefault`.

`config.yaml.sample` documents all of them.

### 1.3 Lifecycle wiring (`internal/serverapp/run.go`)

- `Run(cfg, reg, dispatchers []Dispatcher, opts ...Option)` gains options:
  `WithReadiness(func(context.Context) error)` (phase 1); `WithDBs(*sql.DB...)`
  and `WithMetrics(*obs.Metrics)` follow in phase 2.
- Each main calls `obs.Setup(cfg.LogLevel, cfg.LogFormat, service, os.Stderr)`
  right after loading the config, so migration and startup logs are formatted
  too.
- Mux branch order: health/pprof/metrics handlers → gRPC h2c sniff →
  `isAPIPath` REST container → embedded SPA. The `AccessLog` + `RequestID`
  middleware wraps the mux exactly once, covering all four.
- Replace the `slog.Info("incoming connection", ...)` line with the access log;
  drop the `slog.Info("grpc connection is coming")` line.
- Register the standard gRPC health service
  (`google.golang.org/grpc/health`) and mark it `Serving`.
- Fix graceful shutdown while touching the file: set a hard 15s deadline on
  `Shutdown`, drain dispatchers within the same deadline, and only `os.Exit`
  after the deadline (or on failure) — today the process exits before in-flight
  requests and dispatcher work are guaranteed to stop.

### 1.4 Call-site cleanups

- `internal/grpc/server/interceptor.go`: remove the per-RPC `slog.Info("method",
  ...)` line (the phase 2 interceptor replaces it with metrics + debug logging)
  and the stale `"/nipa.AuthService/health"` skip-list entries; update
  `interceptor_test.go` accordingly.
- `internal/http/api/model.go`: `HandleError` logs unknown errors through
  `obs.Log(ctx)` with `request_id`, method and path.

---

## Phase 2 — Prometheus metrics

New dependency: `github.com/prometheus/client_golang` (standard library for
`/metrics` scrapes; the OTel SDK in phase 3 will not replace it — both can
coexist).

### 2.1 `internal/obs/metrics.go`

A `Metrics` struct (no package globals, so tests can build isolated registries)
with the standard Go/process/build-info collectors and:

| Metric | Type | Labels | Instrumented at |
|---|---|---|---|
| `nipa_http_requests_total` | counter | `route`, `method`, `code` | container filter in `internal/http/api/metrics.go` |
| `nipa_http_request_duration_seconds` | histogram | `route`, `method` | same filter |
| `nipa_grpc_requests_total` | counter | `method`, `code` | `MetricsUnary` / `MetricsStream` interceptors in `internal/grpc/server/metrics_interceptor.go` |
| `nipa_grpc_request_duration_seconds` | histogram | `method` | same |
| `nipa_chunk_ops_total` | counter | `op=put\|upload\|get\|confirm\|presign_upload\|presign_download`, `result` | `usecase.Chunk` (single source for REST and gRPC) |
| `nipa_chunk_bytes_total` | counter | `op=put\|upload\|get\|confirm` | same |
| `nipa_chunk_verify_failures_total` | counter | `stage=store\|confirm\|download` | `usecase.Chunk` verification paths |
| `nipa_push_total` | counter | `result=ok\|error` | `usecase.Push.Push` |
| `nipa_webhook_deliveries_total` | counter | `result=delivered\|retry\|failed` | `internal/webhook/dispatcher.go` |
| `nipa_webhook_queue_depth` | gauge (func) | — | reads `len(dispatcher.jobs)` |
| `nipa_webhook_in_flight` | gauge (func) | — | reads the dispatcher in-flight set |
| `nipa_mail_deliveries_total` | counter | `result=delivered\|retry\|failed` | `internal/mail/dispatcher.go` |
| `nipa_mail_queue_depth` | gauge (func) | — | reads `len(dispatcher.jobs)` |
| `nipa_db_pool_open_connections` / `_idle` / `_in_use` | gauge | `db` | `Metrics.RegisterDB` (custom collector reading `sql.DBStats`) |
| `nipa_db_pool_wait_count_total` / `_wait_seconds_total` | counter (func) | `db` | same |

Cardinality rules: `route` is the go-restful route template
(`/orgs/{org}/projects/{project}/...`), never the raw URL; `code` is the HTTP
status or gRPC status code; `method` is the gRPC full method. Nothing
user-supplied (org names, branch names, paths, chunk hashes) becomes a label.

### 2.2 Endpoint and wiring

- `Metrics.Handler()` mounts at `/metrics` on the main port (unauthenticated,
  standard). `METRICS_ENABLED` (default true) toggles the route.
- The metrics object is created in each main (`obs.NewMetrics()`), the DB pool
  is registered with `metrics.RegisterDB("sqlite"|"postgres", dbConn)`, and
  `serverapp.Run(..., WithMetrics(metrics))` mounts the endpoint and
  instruments HTTP (`api.WithMetrics` container filter) and gRPC.
- gRPC: `grpc.ChainUnaryInterceptor(MetricsUnary, JWTUnary)` and the stream
  equivalent.
- `usecase.Chunk` and `usecase.Push` get nil-safe `WithMetrics(*obs.Metrics)`
  builders; the dispatchers get a `WithMetrics` builder that also registers the
  queue gauges.
- Both editions share all of it: EE gets postgres pool stats through the same
  `sql.DBStats` collector.

### 2.3 Non-goals for metrics (this pass)

- No per-project/per-user histograms (cardinality).
- No slow-query logging; `SQLITE_BUSY`/"database is locked" incidents are
  diagnosed through `nipa_db_pool_*` plus the `result` labels on chunk/push
  counters and the Warn/Error access logs.

---

## Phase 3 — OTel tracing (implemented)

See `docs/TRACING.md` for configuration and the span inventory. Summary:

- `internal/obs/tracing.go` builds the OTLP gRPC exporter + tracer provider,
  enabled by `TRACING_ENABLED` (client: `NIPA_TRACING`); a no-op otherwise.
- gRPC uses `google.golang.org/grpc/stats/opentelemetry` (in the already
  pinned grpc module) via `serverapp.WithTracing` and the client transport's
  dial option; HTTP uses `otelhttp`.
- Push/clone spans cover the CLI root operation, per-chunk HTTP transfers,
  `usecase.Push`/`usecase.Chunk` and the local/S3 chunk stores. W3C
  `traceparent` crosses both gRPC metadata and the chunk HTTP requests.
- Disabled tracing costs nothing at runtime: no exporter, no spans recorded,
  no headers injected.

---

## Phase 4 — log shipping over OTLP (implemented)

Structured logs are mirrored to an OTLP backend (OpenObserve in the SaaS
deployment) next to the unchanged console/JSON output. Traces and logs both
support **OTLP/gRPC** (default) and **OTLP/HTTP**.

- `internal/obs/otlp.go` — shared OTLP plumbing: endpoint resolution (same
  scheme/TLS rules as tracing, including the `OTEL_EXPORTER_OTLP_*` fallback),
  transport selection (`grpc`/`http`), `ParseHeaders` for comma-separated
  `KEY=VALUE` headers (split on the first `=`, so base64 padding survives), the
  signal-path normalization for HTTP endpoints (`/v1/traces`, `/v1/logs` are
  appended unless present) and the shared resource
  (`service.name`/`service.version`).
- `internal/obs/tracing.go` — `TracingConfig.Headers` feeds
  `otlptracegrpc.WithHeaders` / `otlptracehttp.WithHeaders`;
  `TRACING_OTLP_HEADERS` therefore authenticates traces (and, below, logs)
  against backends that require it, e.g. OpenObserve.
- `internal/obs/logs.go` — `SetupLogs` builds an OTLP log exporter (gRPC or
  HTTP) with a batch processor and the `contrib/bridges/otelslog` bridge; a
  disabled config is a nil-safe no-op (`Handler()` returns nil, `Shutdown`
  succeeds).
- `internal/obs/logging.go` — `Setup` accepts optional extra handlers and
  composes them with the console/JSON handler through a `fanoutHandler`; a
  `levelFilterHandler` applies `LOG_LEVEL` to the bridge, so every sink obeys
  the configured level.
- `internal/serverapp/run.go` — `WithLogs(*obs.Logs)` flushes the exporter on
  SIGTERM/SIGINT next to the tracer; both mains wire it.
- Configuration: `LOGS_OTLP_ENABLED` (default false), `LOGS_OTLP_ENDPOINT` and
  `LOGS_OTLP_PROTOCOL` (fall back to the `TRACING_*` values), headers shared
  via `TRACING_OTLP_HEADERS`.

Records emitted with a context (`obs.Log(ctx)`, `slog.*Context`, the access
log) carry the active `trace_id`/`span_id`, so logs and spans correlate in the
backend.

### OpenObserve runbook (SaaS)

1. Deploy OpenObserve (self-hosted example):
   `docker run -d --name openobserve -p 5080:5080 -p 5081:5081 -v openobserve-data:/data public.ecr.aws/zinclabs/openobserve:latest`
   with `ZO_ROOT_USER_EMAIL`/`ZO_ROOT_USER_PASSWORD` set. Port `5080` serves
   the UI/HTTP API, `5081` OTLP over gRPC; streams are created on first write
   (default organization `default`).
2. Grab credentials from **Data Sources → Ingestion** (basic auth,
   `base64(email:password)`) or create a service account under **IAM → Service
   Accounts** and use its token as `Bearer <token>`.
3. Configure `nipad-ee` (env or `config.yaml`/`.env`). Direct OTLP/gRPC (plaintext
   on the OpenObserve gRPC port; the organization travels in a header):

   ```sh
   TRACING_ENABLED=true
   TRACING_OTLP_ENDPOINT=openobserve.internal:5081
   TRACING_OTLP_HEADERS="Authorization=Basic <base64(email:password)>,organization=default"
   LOGS_OTLP_ENABLED=true   # endpoint, protocol and headers are shared with tracing
   METRICS_ENABLED=true     # keep /metrics exposed for the collector
   ```

   Or OTLP/HTTP through an HTTPS host (works behind ordinary HTTP proxies and
   CDNs; `/v1/traces` and `/v1/logs` are appended to the endpoint path):

   ```sh
   TRACING_ENABLED=true
   TRACING_OTLP_PROTOCOL=http
   TRACING_OTLP_ENDPOINT=https://openobserve.example.com/api/default
   TRACING_OTLP_HEADERS="Authorization=Basic <base64(email:password)>"
   LOGS_OTLP_ENABLED=true
   METRICS_ENABLED=true
   ```

   Do not point the gRPC exporter at the HTTP port or at a TLS terminator that
   does not speak gRPC (`5080`, or `443` behind an HTTP/1.1 proxy): the
   handshake fails with `tls: server selected unadvertised ALPN protocol`. Use
   the gRPC port directly or switch to `TRACING_OTLP_PROTOCOL=http`.

4. Metrics, pick one:
   - Push straight from `nipad-ee` (no Prometheus, collector or vmagent —
     phase 5): the registry is exported over OTLP on the same transport as
     traces/logs, `/v1/metrics` is appended to HTTP endpoints.

     ```sh
     METRICS_OTLP_ENABLED=true
     METRICS_OTLP_INTERVAL_SECONDS=30   # push cadence, default 30
     ```

   - Or scrape with an agent and remote-write to OpenObserve's Prometheus
     endpoint (`POST /api/{org}/prometheus/api/v1/write`, port `5080`
     self-hosted): point an OpenTelemetry Collector (or vmagent) at the
     server's `/metrics`:

   ```yaml
   receivers:
     prometheus:
       config:
         scrape_configs:
           - job_name: nipad-ee
             scrape_interval: 30s
             metrics_path: /metrics
             static_configs:
               - targets: ["nipad-ee.nipad.svc:6745"]
   exporters:
     prometheusremotewrite:
       endpoint: https://openobserve.example.com/api/default/prometheus/api/v1/write
       headers:
         Authorization: Basic <base64(email:password)>
   service:
     pipelines:
       metrics:
         receivers: [prometheus]
         exporters: [prometheusremotewrite]
   ```

   A Prometheus in agent mode (or VictoriaMetrics' vmagent) works the same way:

   ```yaml
   global:
     scrape_interval: 30s
   remote_write:
     - url: https://openobserve.example.com/api/default/prometheus/api/v1/write
       basic_auth:
         username: admin@example.com
         password: change-me
   scrape_configs:
     - job_name: nipad-ee
       metrics_path: /metrics
       static_configs:
         - targets: ["nipad-ee.nipad.svc:6745"]
   ```

5. Explore: filter logs by `service = 'nipad-ee'` or a `trace_id`; the trace
   stream carries the spans inventoried in `docs/TRACING.md`; dashboards and
   alerts run over the `nipa_*` series listed in phase 2.

---

## Phase 5 — direct metrics push (implemented)

`nipad`/`nipad-ee` can push their own metrics straight to an OTLP backend — no
Prometheus, collector or vmagent in between. `obs.SetupMetricsPush`
(`internal/obs/metrics_push.go`) feeds the existing Prometheus registry through
the OpenTelemetry Prometheus bridge
(`go.opentelemetry.io/contrib/bridges/prometheus`) into a periodic OTLP reader,
so metrics reuse the trace/log endpoint, protocol and header configuration.

- `METRICS_OTLP_ENABLED` (default false) turns the push on;
  `METRICS_OTLP_ENDPOINT` and `METRICS_OTLP_PROTOCOL` fall back to the
  `TRACING_*` values, headers are `TRACING_OTLP_HEADERS`, and
  `METRICS_OTLP_INTERVAL_SECONDS` (default 30) sets the push cadence.
- The registry is read in-process: the push also works with
  `METRICS_ENABLED=false`, and scraping `/metrics` can run in parallel.
- Counters are exported cumulatively and the exporter flushes on
  SIGTERM/SIGINT via `serverapp.WithMetricsPush`.
- The push complements the phase 4 collector/remote-write path; both produce
  the same `nipa_*` series in the backend.

---

## Configuration reference

| Variable | Default | Meaning |
|---|---|---|
| `LOG_LEVEL` | `info` | `debug\|info\|warn\|error`; unknown values warn and fall back to info |
| `LOG_FORMAT` | `console` | `console` (text) or `json` |
| `PPROF_ENABLED` | `false` | Serve `/debug/pprof/*` on the main port |
| `METRICS_ENABLED` | `true` | Serve `/metrics` on the main port |
| `TRACING_ENABLED` | `false` | Export OTLP spans (see `docs/TRACING.md`) |
| `TRACING_OTLP_ENDPOINT` | `localhost:4317` (gRPC) / `localhost:4318` (HTTP) | OTLP collector endpoint; an HTTP endpoint may carry a path prefix (e.g. OpenObserve's `/api/<org>`) |
| `TRACING_OTLP_PROTOCOL` | `grpc` | `grpc` or `http` |
| `TRACING_OTLP_HEADERS` | empty | Comma-separated `KEY=VALUE` headers on every OTLP request (traces and logs), e.g. an OpenObserve `Authorization` (and `organization` for gRPC) |
| `TRACING_STDOUT` | `false` | Pretty-print spans to stderr instead of exporting |
| `TRACING_SAMPLE_RATIO` | `1.0` | Root span sampling ratio in `(0, 1]` |
| `TRACING_SERVICE_NAME` | `nipad` / `nipad-ee` | OTel `service.name` |
| `LOGS_OTLP_ENABLED` | `false` | Ship logs over OTLP next to the console/JSON output |
| `LOGS_OTLP_ENDPOINT` | `TRACING_OTLP_ENDPOINT` | OTLP endpoint for logs |
| `LOGS_OTLP_PROTOCOL` | `TRACING_OTLP_PROTOCOL` | OTLP transport for logs |
| `METRICS_OTLP_ENABLED` | `false` | Push the Prometheus registry over OTLP (no agent needed) |
| `METRICS_OTLP_ENDPOINT` | `TRACING_OTLP_ENDPOINT` | OTLP endpoint for metrics |
| `METRICS_OTLP_PROTOCOL` | `TRACING_OTLP_PROTOCOL` | OTLP transport for metrics |
| `METRICS_OTLP_INTERVAL_SECONDS` | `30` | Metrics push cadence |

---

## Runtime cost and concurrency

Every signal is opt-in and costs nothing when disabled: no provider, no
goroutine, no connection. When enabled, the exporters are batch pipelines —
the request path formats and enqueues, background goroutines ship.

| Signal | Background worker | Caller-path cost | Queue | Cadence |
|---|---|---|---|---|
| Traces | one `BatchSpanProcessor` goroutine + one OTLP connection | span end + enqueue (mutex ring) | 2048 spans, export batches of 512 | every 5s |
| Logs | one log `BatchProcessor` goroutine + one OTLP connection | record conversion and enqueue (allocs + one lock), network is async | 2048 records (oldest overwritten when full) | every 1s |
| Metrics | one `PeriodicReader` ticker goroutine + one OTLP connection | none beyond the existing Prometheus counter/atomic updates | full registry gathered per tick | `METRICS_OTLP_INTERVAL_SECONDS` (30s) |

- Traces: ended spans are enqueued by the SDK's batch processor and exported
  in the background; a full queue drops spans instead of blocking the caller.
- Logs: the `otelslog` bridge converts each `slog.Record` to an OTLP record
  on the calling goroutine, then the batch processor queues it. `LOG_LEVEL`
  gates both the stderr and the OTLP sink, so debug verbosity is one knob.
- Metrics: the registry is gathered and converted on the ticker goroutine,
  never on request paths; counters are exported cumulatively (each push
  carries the running totals). A push slower than the interval skips ticks.
- Connections: at most one persistent gRPC/HTTP2 connection per enabled
  signal (three total). gRPC retries transient export failures in the
  background; failures surface through the `opentelemetry error` warn log.
- Shutdown flushes all enabled exporters within the 15s `serverapp` deadline.

Expected footprint: a few MB of memory and negligible CPU on an idle server.
The variable cost is serialization volume, controlled with `LOG_LEVEL`,
`TRACING_SAMPLE_RATIO` (and a collector-side sampler if needed) and, for
metrics, `METRICS_OTLP_INTERVAL_SECONDS`.

---

## Testing and verification

- `internal/obs` unit tests: level/format parsing, request-ID generation and
  pass-through, access-log levels and query sanitization, health/readiness
  handlers, pprof disabled returns 404, metrics counters/labels, OTLP header
  parsing, fanout level handling and real OTLP gRPC/HTTP receivers asserting
  log record and pushed metrics (`nipa_push_total`) delivery with the
  `Authorization` metadata.
- REST filter test with `httptest` asserting route-template labels; gRPC
  metrics interceptor tests in the style of `interceptor_test.go`; dispatcher
  counters via a recorder.
- `serverapp/run_test.go` keeps the listen-error test working through the new
  option signature.
- CI parity: `go build ./...`, `go vet ./...`, `go test ./...`, `make lint`.

## File-level change list

| File | Change |
|---|---|
| `internal/obs/*.go` (new) | logging, request ID, access log, health, pprof, metrics |
| `internal/obs/otlp.go`, `logs.go`, `metrics_push.go` (new) | shared OTLP helpers, OTLP log shipping, direct metrics push |
| `internal/obs/logging.go`, `tracing.go` | fanout logging, OTLP headers |
| `internal/config/config.go`, `config.yaml.sample` | new knobs, wire `LOG_LEVEL` |
| `internal/serverapp/run.go` | obs setup, mux routes, middleware, gRPC health, shutdown deadline, options |
| `internal/http/api/api.go`, `metrics.go` | metrics container filter |
| `internal/http/api/model.go` | request-ID-aware error logging |
| `internal/grpc/server/interceptor.go` (+ tests) | drop stale logs/skip entries |
| `internal/grpc/server/metrics_interceptor.go` (+ tests) | gRPC metrics interceptors |
| `internal/usecase/chunk.go`, `push.go` | nil-safe metrics, chunk/push counters |
| `internal/webhook/dispatcher.go`, `internal/mail/dispatcher.go` | delivery counters, queue/in-flight gauges |
| `cmd/nipad/main.go`, `ee/cmd/nipad/main.go` | `obs.Setup`, `WithReadiness`, `WithMetrics`, DB registration, OTLP logs + metrics |
| `go.mod` | `prometheus/client_golang`, `otlploggrpc`/`sdk/log`/`otelslog`, `otlpmetric*`/`sdk/metric`/`bridges/prometheus` |
| `docs/OBSERVABILITY.md` (this file) | design + runbook |

No SQL, proto or schema changes; free/EE parity preserved (`internal/` never
imports `ee/`).
