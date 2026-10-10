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
  tracing is deferred (see phase 3) and only needs to attach to the hooks
  created here.
- All runtime endpoints (`/metrics`, `/healthz`, `/readyz`, pprof) live on the
  existing server port. pprof is flag-gated and off by default.

**Status:** phase 1 is implemented (`internal/obs`, wired through
`internal/serverapp` and both mains). Phases 2 and 3 are planned.

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
| `nipa_http_requests_total` | counter | `route`, `method`, `code` | container filter in `internal/http/api/api.go` |
| `nipa_http_request_duration_seconds` | histogram | `route`, `method` | same filter |
| `nipa_grpc_requests_total` | counter | `method`, `code` | new `MetricsUnary` / `MetricsStream` interceptors in `internal/grpc/server` |
| `nipa_grpc_stream_duration_seconds` | histogram | `method` | same |
| `nipa_chunk_ops_total` | counter | `op=put\|get\|confirm\|presign`, `result` | REST chunk handler, gRPC chunk service, `usecase.Chunk` |
| `nipa_chunk_bytes_total` | counter | `op=put\|get\|confirm` | same |
| `nipa_chunk_verify_failures_total` | counter | `stage=store\|confirm` | `usecase.Chunk` verification paths |
| `nipa_push_total` | counter | `result` | `internal/grpc/server/push.go` |
| `nipa_webhook_deliveries_total` | counter | `result=delivered\|retry\|failed` | `internal/webhook/dispatcher.go` |
| `nipa_webhook_queue_depth` | gauge (func) | — | reads `len(dispatcher.jobs)` |
| `nipa_webhook_in_flight` | gauge (func) | — | reads the dispatcher in-flight set |
| `nipa_mail_deliveries_total` | counter | `result=delivered\|retry\|failed` | `internal/mail/dispatcher.go` |
| `nipa_mail_queue_depth` | gauge (func) | — | reads `len(dispatcher.jobs)` |
| `nipa_db_pool_open_connections` / `_idle` / `_in_use` | gauge (func) | `db` | `WithDBs` handles via `sql.DBStats` |
| `nipa_db_pool_wait_count_total` / `_wait_seconds_total` | counter (func) | `db` | same |

Cardinality rules: `route` is the go-restful route template
(`/orgs/{org}/projects/{project}/...`), never the raw URL; `code` is the HTTP
status or gRPC status code; `method` is the gRPC full method. Nothing
user-supplied (org names, branch names, paths, chunk hashes) becomes a label.

### 2.2 Endpoint and wiring

- `Metrics.Handler()` mounts at `/metrics` on the main port (unauthenticated,
  standard). `METRICS_ENABLED` (default true) toggles the route.
- The metrics object is created in each main (`obs.NewMetrics()`), passed to
  `serverapp.Run` via `WithMetrics`, and stored on the `serverapp.Registry` so
  `api.NewAPI(reg)` can add the container filter without a signature change.
- gRPC: `grpc.ChainUnaryInterceptor(metricsUnary, jwtUnary)` and the stream
  equivalent.
- `usecase.Chunk` gets a nil-safe `WithMetrics(*obs.Metrics)` builder; the
  dispatchers get a `WithMetrics` option read at construction.
- Both editions share all of it: EE gets postgres pool stats through the same
  `DBStats` collector.

### 2.3 Non-goals for metrics (this pass)

- No per-project/per-user histograms (cardinality).
- No slow-query logging; `SQLITE_BUSY`/"database is locked" incidents are
  diagnosed through `nipa_db_pool_*` plus the `result` labels on chunk/push
  counters and the Warn/Error access logs.

---

## Phase 3 — OTel tracing (deferred)

When enabled later, tracing attaches to the hooks phase 1/2 already provide:

- Add `otel/sdk` + OTLP exporter, enabled only when
  `OTEL_EXPORTER_OTLP_ENDPOINT` (or the standard `OTEL_*` variables) is set —
  otherwise a no-op.
- The request-ID middleware does W3C `traceparent` extraction/injection in the
  same slot; the request ID is kept as a span attribute for log correlation.
- The gRPC interceptor chain gains the otel interceptor in front of the JWT
  and metrics interceptors.
- No phase 1/2 code needs to move: this is additive.

---

## Configuration reference

| Variable | Default | Meaning |
|---|---|---|
| `LOG_LEVEL` | `info` | `debug\|info\|warn\|error`; unknown values warn and fall back to info |
| `LOG_FORMAT` | `console` | `console` (text) or `json` |
| `PPROF_ENABLED` | `false` | Serve `/debug/pprof/*` on the main port |
| `METRICS_ENABLED` | `true` | Serve `/metrics` on the main port |

---

## Testing and verification

- `internal/obs` unit tests: level/format parsing, request-ID generation and
  pass-through, access-log levels and query sanitization, health/readiness
  handlers, pprof disabled returns 404, metrics counters/labels.
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
| `internal/config/config.go`, `config.yaml.sample` | new knobs, wire `LOG_LEVEL` |
| `internal/serverapp/run.go` | obs setup, mux routes, middleware, gRPC health, shutdown deadline, options |
| `internal/serverapp/registry.go` | optional metrics handle |
| `internal/http/api/api.go` | metrics container filter |
| `internal/http/api/model.go` | request-ID-aware error logging |
| `internal/http/handler/chunk.go` | chunk op/byte metrics |
| `internal/grpc/server/interceptor.go` (+ tests) | drop stale logs/skip entries; metrics interceptors |
| `internal/grpc/server/chunk.go`, `internal/grpc/server/push.go` | gRPC-side chunk/push metrics |
| `internal/usecase/chunk.go` | nil-safe metrics, verify-failure counters |
| `internal/webhook/dispatcher.go`, `internal/mail/dispatcher.go` | delivery counters, queue/in-flight gauges |
| `cmd/nipad/main.go`, `ee/cmd/nipad/main.go` | `obs.Setup` name, `WithReadiness` |
| `go.mod` | `prometheus/client_golang` |
| `docs/OBSERVABILITY.md` (this file) | design + runbook |

No SQL, proto or schema changes; free/EE parity preserved (`internal/` never
imports `ee/`).
