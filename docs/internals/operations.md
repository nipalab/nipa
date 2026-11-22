# Operations internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

Flow for rate limiting (design in `docs/RATE_LIMIT.md`): `internal/ratelimit`
holds the `Store` seam (atomic token-bucket `Take`), the `Limiter` (named scope
rules, fail-open on store errors except the auth scopes, which fall back to a
lazily created in-process `Memory` store, nil limiter allows everything), the `Memory`
store (free `nipad`, also selectable on `nipad-ee`) and `ClientIP`
(`X-Forwarded-For` honoured only from `RATE_LIMIT_TRUSTED_PROXIES`, right-most
untrusted hop). `ee/ratelimit.Redis` is the shared store (one Lua script);
`RATE_LIMIT_STORE=memory|redis` is resolved by `serverapp.BuildRateLimiter`
(`config.RateLimitStoreName`: the free main passes no redis factory, so `redis`
is a startup error there; on `nipad-ee` an unset store picks redis when
`RATE_LIMIT_REDIS_ADDR` is set). One limiter feeds `UsecaseDeps.RateLimiter`
(`usecase.Auth.WithLimiter`: `auth_id` per email + client IP from
`domain.RequestMeta` (email alone without one), then the higher email-only
`auth_email` ceiling, for password and SSH logins; `usecase.User.WithLimiter`
counts `ChangePassword` under `auth_id` `user:<id>` before the old-password
check; `ee/registration` uses `register_email`; 429 via
`domain.NewErrorTooManyRequests`) and `serverapp.WithRateLimit`
(`api.RateLimit`: global/chunk middleware inside the access log, `auth_ip`
filter plus `RequestMetaFilter` on the `/api/v1/auth` WebService, `api_user` filter after the bearer
filter; `grpcserver.RateLimiter` chained after JWT: public login RPCs per IP,
the rest per user). REST answers 429 with `Retry-After`; gRPC answers
`ResourceExhausted` with the `rate limit exceeded` prefix, which
`client/grpc.toDomainError` maps back to 429 (other `ResourceExhausted` stays
402). Metric: `nipa_ratelimit_total{scope,result}`.

Flow for the audit log (design in `docs/AUDIT_LOG.md`, both editions): an
append-only `audit_log` table (no foreign keys, metadata JSON TEXT on sqlite /
JSONB on postgres) written by `usecase.Audit` (`AuditRecorder`), wired in
`serverapp.BuildUsecases` and attached to the org, group, permission, branch
protection, file lock (force release only), user, project and webhook
usecases through `WithAudit` (nil = off). Each audited write and its record run
in one `WithinTx` via `auditedWrite`, so a failed record rolls the change back;
repositories touched by audited writes must be `dbtx`-aware (no private
`BeginTx`). Action names live in `internal/domain/audit_actions.go`; reads are
never audited and metadata never carries secrets. The REST auth filter and the
gRPC interceptors attach `domain.RequestMeta` (client IP through
`ratelimit.ClientIP` + `RATE_LIMIT_TRUSTED_PROXIES`, user agent); entries
without a claim are `system` (SaaS signup/OAuth). Super admins read it at
`GET /api/v1/admin/audit` (actor/org/project/action prefix/target/since/until,
keyset `after`, `next_cursor`). On `nipad-ee`, `ee/billing.Service` and
`ee/storageusage.Ledger` take the same recorder (`WithAudit`, wired after
`BuildUsecases` in `ee/cmd/nipad/main.go`) for the `billing.*` actions:
quota override set/cleared and owner transfers (through the exported
`usecase.AuditedWrite`), checkout, plan change, sync, replay, and
`billing.subscription.applied` inside `Apply`'s transaction only when the
upsert wrote (claim actor when present, otherwise `provider`/`paddle` with the
event id; old values read from the live subscription and
`Ledger.ComputedQuota`). `billing.seat.denied` is the exception: written after
`Admit` rolls back on a `dbtx.Detach`ed context, failures only logged. Login
entries are best-effort too (`Auth.recordLogin`, never fail a login):
`auth.login.succeeded` from `LoginResultVia` (method `password`/`ssh_key`/
`oauth`/`email_verification`; plain `LoginResult`, used for the reissue after
a password change, records nothing) and `auth.login.failed` (actor `system`,
normalized email, method, reason, target user when the account exists) only
for attempts that passed the throttle. `user.password.changed` is a regular
`auditedWrite`. The JWT interceptors also attach `RequestMeta` for the public
login RPCs.

Flow for maintenance mode (operator
guide, database move runbooks and the hard-downtime Caddy page in
`docs/MAINTENANCE.md`: `deploy/caddy/Caddyfile` `handle_errors 502 503 504`
answers gRPC with the server `full` refusal, `/api`/`/auth` with its JSON and
browsers with `deploy/caddy/maintenance/index.html`, kept in sync by
`internal/maintenance/caddy_test.go`): windows
have three scopes (server, org, project). The pure `internal/maintenance` (no
repo/usecase imports) holds `Mode` (`off|read_only|full|frozen`, `Stricter`),
`Class` and the rule table (`Allowed`/`AllowedMethod`: control stays open in
`frozen` for GET/HEAD only), `Window` (`Scope()`/`Key()`)/`Status`, `ParseFile`
(override file: empty = `full`, else JSON `{mode, message, ends_at}`),
`Service` (an `atomic.Pointer[Snapshot]` with `Server`, `Orgs` by org slug and
`Projects` by "org/project"; `For(org, project)` = the strictest active
server/org/project window, `project` may be empty for org targets, ties report
the broadest scope; `Frozen`; `Apply` runs the `OnResume` hooks before
publishing a snapshot that leaves `frozen` and keeps the frozen one if a hook
fails), `Poller` (a `serverapp.Dispatcher`: reloads windows + file, a failed
load keeps the stored windows, an invalid file publishes `full` with
`Snapshot.FileError`) and the shared rejection `Info`/`Error` (`organization
acme is in maintenance (closed): ...`). Storage: `maintenance_windows` (sqlite
`000024`, postgres `000009`; snow id, nullable `org_id`/`project_id` never both
set, `frozen` only for the server window, one server window and one per org
and project through partial unique indexes, upserts keep `id`/`created_by`),
cleaned by triggers on project/org soft delete
(`maintenance_windows_project_cleanup`/`_org_cleanup`, the org one also drops
its projects' windows; sqlite also `_project_delete`/`_org_delete` for hard
deletes, postgres FKs cascade); queries in `maintenance.sql` of both dialects
skip deleted orgs/projects; recipients: `RecipientsServer` (verified owners of
live orgs), `RecipientsOrg` (owners plus root admin rules org-wide or on any
live project of the org, direct or through live groups; org-wide admins count
without projects) and `RecipientsProject`.
`sqlite|postgres.NewMaintenanceRepository` (`dbtx`-aware) is wired as
`usecase.Repositories.Maintenance` in both mains and both e2e harnesses.
`usecase.Maintenance` (`maintenance.go`, gomock-tested): `Status` (public
server window + `source`), `AdminStatus` (super admin: server, org and project
windows), `OrgStatus` (org members via `Org.IsOrgMember`; owners also get the
org's project windows), `ProjectStatus` (project read), `SetServer`/`SetOrg`/
`SetProject` and `EndServer`/`EndOrg`/`EndProject` (server: super admin; org
and project: `IsOrgOwner` or super admin; no project admins). Validation runs
before the write: mode, `frozen` only for the server, message <= 500 runes,
`ends_at > starts_at`, `starts_at` <= now + 90 days, a missing `starts_at`
keeps an existing window's start, else now; a server write while the snapshot
`source` is `file` is a 409 `maintenance is controlled by MAINTENANCE_FILE`;
ending a missing window is a 404. Writes run in `auditedWrite`
(`maintenance.scheduled|updated|ended`, target `maintenance_window`, metadata
`scope`, `org_id`, `project_id`, `mode`, `starts_at`, `ends_at`, `notify`,
`notified`) together with the optional `MaintenanceMailer` (`WithMailer`):
with `notify` the usecase loads the scope's recipients in the transaction and
calls `Announce(ctx, MaintenanceAnnouncement{Notice, Window, OrgName,
Recipients}) (queued, error)`; notices are `scheduled` (new window or first
announcement), `updated` (an announced window changed) and `finished` (end of
a window stored with `notify`); `notified` = at least one message queued, and
`Kick` runs after the commit. `usecase.MaintenanceEmailer`
(`maintenance_notify.go`, wired in `BuildUsecases` with the email dispatcher
as kicker, and in the subcommand when `mail.NewFromConfig` gives a sender and
the opener sets `Repositories.Email`, without kicker) dedupes by user and
address, drops invalid addresses, ignores `notify_email`, writes one outbox
row per recipient (event `maintenance.<notice>`, thread key
`maintenance:<window id>`, `ProjectID` only for project windows; later notices
reply to the per-recipient root `Message-ID`) with plain text + HTML (scope,
mode in words, UTC start and estimated end, message, `EMAIL_BASE_URL` link).
After the commit `apply` rebuilds the snapshot from `List` (keeping a
current file override) through `Service.Apply`. `WithSystemActor()` is the
CLI variant: no permission checks, `created_by` 0, actor `system`, `via: cli`.
REST (`internal/http/api/maintenance.go`, handlers/DTOs in
`handler|model/maintenance.go`): `GET /api/maintenance` on its own WebService
(no bearer, exempt from the rate limit like `/version`),
`GET|PUT|DELETE /api/v1/admin/maintenance`, `.../orgs/{org}/maintenance` and
`.../orgs/{org}/projects/{project}/maintenance` (PUT body `{mode, message,
starts_at?, ends_at?, notify}`, answer the stored window with `notified`;
DELETE 204). Config `MAINTENANCE_FILE` / `MAINTENANCE_POLL_SECONDS` (default
5, min 1; `Config.MaintenanceFilePath`/`MaintenancePollInterval`).
`serverapp.NewMaintenance(cfg, repo, metrics)` builds the `Service` (its
`OnPublish` hook sets `nipa_maintenance_server_mode{mode}` and
`nipa_maintenance_windows{scope,mode}`) and the `Poller` (`WithErrorHook` →
`nipa_maintenance_poll_errors_total`); both mains pass the service as
`UsecaseDeps.Maintenance` and `serverapp.WithMaintenance(svc, poller)`, which
polls once before serving and registers the poller as a dispatcher (the e2e
harnesses poll once). `serverapp.RunMaintenanceCommand` (`maintenance_cmd.go`)
runs right after `version.Flag` in both mains: `maintenance status`, `set
<read_only|full|frozen> [--org <slug> | --project <org/project>] [-m]
[--start] [--end] [--notify]`, `end [--org|--project]`; it loads the config,
opens the database through the main's `MaintenanceOpener` (sqlite or the ee
postgres opener, never migrating, `Email` included for `--notify`; a missing
table is `schema too old, start the server once`), prints `notified: true|false`
with `--notify` (a note when it queued mail for an active frozen window), reads the override file and uses `snow.ReservedCLINode`
(255): `ee/nodelease` leases 0..254 only and `snow.NewServerNode` (both mains'
static `SNOWFLAKE_NODE_ID`) refuses 255.
Enforcement goes through `Service.Check(class, method, org, project)` (nil
service = allow), no bypass for anyone. gRPC:
`grpcserver.Maintenance` (`maintenance_interceptor.go`, chained Metrics → JWT
→ Maintenance → RateLimit in `serverapp.run` and both e2e harnesses) classifies
by `methodClass` (`maintenance_classes.go`, every `NipaService` and health
method; unknown = write) and targets `GetContext()` org/project, else
`GetOrg()`, else server only (`serverScopeOnly`: `ListMyProjects`); tests walk
the ServiceDescs and instantiate every read/write request through
`protoregistry`. A refusal is `Error.GRPCError(now)`: `Unavailable` +
`errdetails.ErrorInfo{Reason: MAINTENANCE, Domain: nipa, Metadata}` + a
`RetryInfo` while `ends_at` is ahead; `maintenance.FromGRPC` reads it back
(the client `offlineError` passes it through instead of "content not cached
locally"). The public, rate-limit-exempt `GetMaintenanceStatus`
(`grpc/server/maintenance.go`) answers `{server, org, project, source}`; org
and project only for a valid bearer token through `ProjectStatus`/`OrgStatus`.
REST: `maintenanceFilter` (`http/api/maintenance_filter.go`) is a WebService
filter on `/api/v1/auth`, `/api/v1` (after the bearer filter, before the rate
limit), `/api/chunks`, `/api/maintenance` and every `api.WithWebService`
(the Paddle webhook is a write, so 503); `RouteClass(method, path)` = the
`routeClasses` public/auth/control entries (trailing `/` trimmed), else
GET/HEAD/OPTIONS read and the rest write; target = `{org}`/`{project}` path
parameters. A refusal is 503 with `Retry-After`, `X-Nipa-Maintenance:
<scope>;<mode>` and `{error, maintenance: Info}`. `testdata/
maintenance_routes.golden` lists the class of every registered route
(`NIPA_UPDATE_GOLDEN=1` rewrites it); ee route classes are tested in
`ee/oauth` and `ee/registration`. `/healthz`, `/readyz`, `/version`,
`/metrics`, `/docs` and the SPA never pass the filter.
`nipa_maintenance_rejected_total{transport,class,scope}` counts refusals. A
file override has no `starts_at` (omitted from JSON, metadata and proto).
`frozen` (server window only, for database moves) keeps reads, public and
control GET/HEAD open and refuses writes, sign-in, refresh, logout and the
maintenance PUT/DELETE routes (end it with `nipad maintenance end`), and the
server writes nothing: `serverapp.run` calls `PauseWhileFrozen(svc,
dispatchers...)`, which hands `svc.Frozen` to every `serverapp.Pausable`
(`SetPause`) dispatcher (the poller is not one); the webhook, mail and issue
tracker dispatchers skip their sweeps/claims/retention trims and workers hold a
claimed job in `dispatch.Pause.Wait` (`PauseCheckInterval`) until resume, the
ee `quotaReconcileJob` waits before its run and `TermsRefreshJob` skips
ticks; `ServiceToken.WithPause` skips the `last_used_at` touch;
`Ledger.OrgStorage` resolves the billing owner read-only
(`lookupBillingOwner`). `ee/nodelease.Manager.SetPause(svc.Frozen)` stops
renewal and lease-loss detection, `Release` keeps the row while frozen, and
`Manager.Resume` is the `Service.OnResume` hard gate (ee main): renew, else
reclaim the same id, else `OnLost` + error; any error keeps the frozen
snapshot and the next poll retries (counted by
`nipa_maintenance_poll_errors_total`). Startup writes (migrations, the
`nipad-ee` lease claim) still happen. Client: `client/grpc.toDomainError`
turns a refusal into a 503 `domain.Error` with `Maintenance`
(`MaintenanceInfo`), a per-mode hint and the gRPC error as `Cause`
(`domain.IsMaintenance`); `isRefreshRejection` is false for it, so
`Session.Refresh` keeps the stored tokens without `reauth`. SPA
`api/client.ts`: `errorFromResponse` fills `ApiError.maintenance` from a 503
body (`isMaintenanceError`), `refresh()` stops retrying on it, `apiFetch`
rethrows it after a 401 without dropping the session and `bootstrap()`
answers false without the logout event. Tests: `internal/e2e/
maintenance_frozen_test.go` (zero writes through `PRAGMA data_version`,
paused webhook/email delivered after `maintenance end`, a failing resume hook
keeps frozen, kept session, `VACUUM INTO` copy starts frozen), the REST
`maintenance frozen` subtest (`total_changes()`), `ee/e2e/maintenance_test.go`
(read workload on a `default_transaction_read_only=on` pool).
Client surfaces (PR 6): `client/domain` holds `NewMaintenanceError(info,
cause)` (nil cause = the gRPC refusal, so `maintenance.FromGRPC` works on
every maintenance error), `MaintenanceHint` and `MaintenanceStatus`/
`MaintenanceWindow` (`Refusal`, `NextStart`, `Describe`). The chunk HTTP
transfers read a 503 with `X-Nipa-Maintenance` (`httpMaintenanceError`, body
or header) and never retry it (`httpRetryHint`); unary calls only repeat rate
limit refusals. `Transport/Client.GetMaintenanceStatus(org, project)` sends
the stored access token when there is one (no refresh, never a prompt).
`Session.reauth` returns a maintenance cause or SSH refusal and, before a
password prompt, `signInPaused` (`usecase/maintenance.go`) probes the server
window and returns the refusal while it is frozen; `Auth.loginInteractively`
and `nipa login` do the same. CLI (`cli/maintenance.go`): `cloneMaintenance`
(2 s timeout, failures ignored) feeds one `maintenance: <Describe>` line per
window to `nipa status` (also `maintenance` in `--json`) and `nipa version
--server` (`maintenance: off` when none, `maintenance` in JSON);
`maintenanceGate` makes `nipa push` (not `--dry-run`) refuse before any work
during an active window and print `maintenance starts in <d>` within 24 h;
`cmd/nipa` prints errors through `cli.ErrorText` (estimated end + `hint:`)
and `output.WriteError` adds `maintenance`; exit code 1. Daemon:
`teamWatch.pollMaintenance` (`daemon/maintenance.go`) asks
`GetMaintenanceStatus` every poll, fills `RepoActivity.maintenance`
(`MaintenanceState`), appends `maintenance` `TeamEvent`s
(`scheduled|started|updated|ended` in `message`, the window in
`maintenance`) and a refusal of the other poll calls never sets `error`;
`OpFailure` is 503 with `maintenance` (`opFailure`); the JSON bridge adds
`maintenance` (Info) to error bodies and stream error lines. MCP `toolError`
adds `next: retry after <ends_at>`. Suite: `tests/suite/maintenance_test.go`
(project read-only/closed, org-owner rights, frozen session through the
`MAINTENANCE_FILE` that `tests/run.sh` configures as
`NIPA_TEST_MAINTENANCE_FILE`, daemon events and bridge, MCP hint, the
announcement emails).
Desktop (PR 8, `docs/DESKTOP.md`): `service/maintenance.go` maps
`RepoActivity.maintenance`/`TeamEvent.maintenance` to `MaintenanceWindow`
(scope from the slot, else `project` → `org` → server) and refusals to
`MaintenanceError` (`OpFailure.maintenance`, `cleanError`; the Wails error
`cause` is `{message, maintenance}`, read by `lib/maintenance.ts`
`maintenanceOf` and rendered by `errorMessage`). `MaintenanceBanner` +
`hooks/useMaintenance.ts` (`MaintenanceProvider` in `RepoView`, `writes`/
`sync` notes) disable writes in any active window and Update/Switch/tag
checkout in `full`/`frozen`; the clone dialog says sign-in is paused when
frozen; notification kind `notify.maintenance` (default on) and tray counts.

Flow for tracing (design in `docs/TRACING.md`): `internal/obs/tracing.go`
builds an OTLP gRPC tracer provider (`SetupTracing`), installed globally with
the W3C propagator only when enabled — otherwise every path stays on the OTel
no-op provider (no exporter, no spans, no headers). Server: config
`TRACING_ENABLED` / `TRACING_OTLP_ENDPOINT` / `TRACING_STDOUT` /
`TRACING_SAMPLE_RATIO` / `TRACING_SERVICE_NAME`, wired in both mains and
`serverapp.WithTracing` (gRPC via the in-module
`google.golang.org/grpc/stats/opentelemetry` server option, HTTP via
`otelhttp` around the non-gRPC mux, flush before `os.Exit` in the signal
handler). Client: env-only `NIPA_TRACING` (`1` or `stdout`),
`NIPA_TRACING_OTLP_ENDPOINT`, `NIPA_TRACING_SAMPLE_RATIO`;
`obs.ActiveTracing().GRPCDialOption()` is appended in
`Transport.Connect`, chunk PUT/POST/GET requests inject `traceparent`
manually (`injectTraceContext`) so a single trace spans CLI → gRPC →
presigned HTTP → S3/local store. Manual spans go through
`obs.StartSpan`/`obs.FinishSpan` with the shared `nipa.*` attribute
constants: client `nipa.push`/`push.upload`/`push.commit`/`nipa.clone`/
`nipa.update`/`nipa.switch`/`nipa.sync`/`chunk.upload.*`/`chunk.download.get`,
server `push.apply`, `chunk.presign_*`/`chunk.confirm`/`chunk.store`/
`chunk.verify`/`chunk.download` and the per-object `storage.local.*` /
`storage.s3.*` store spans.

Flow for observability to OpenObserve (SaaS; design in `docs/OBSERVABILITY.md`):
`internal/obs/otlp.go` holds the shared OTLP endpoint resolution
(`otlpEndpointTarget.traceOptions`/`logOptions`, plus the
`traceHTTPOptions`/`logHTTPOptions` variants), transport selection
(`TRACING_OTLP_PROTOCOL` = `grpc` default / `http`; the OTLP/HTTP exporters get
the `/v1/traces` or `/v1/logs` signal path appended to a configured URL prefix
by `httpSignalURL` before `WithEndpointURL` uses it as-is), `ParseHeaders`
(comma-separated `KEY=VALUE`, first `=` only so base64 padding survives) and the
shared resource; `TRACING_OTLP_HEADERS` feeds the trace exporters.
`obs.SetupLogs` (`internal/obs/logs.go`, OTLP + otelslog bridge)
ships slog records over OTLP when `LOGS_OTLP_ENABLED` (endpoint and protocol
fall back to the `TRACING_*` values, same headers), fanned out next to the
console/JSON output via `fanoutHandler` in `obs.Setup` with a
`levelFilterHandler` so every sink obeys `LOG_LEVEL`; both exporters flush on
shutdown via `serverapp.WithLogs`. Metrics are pull-based (`/metrics` on the main port or
`METRICS_ADDRESS`, `nipa_*` series) or pushed directly with `METRICS_OTLP_ENABLED`: `obs.SetupMetricsPush`
(`internal/obs/metrics_push.go`) bridges the Prometheus registry through
`contrib/bridges/prometheus` into a periodic OTLP reader (endpoint/protocol
fall back to `TRACING_*`, headers shared, `METRICS_OTLP_INTERVAL_SECONDS`
cadence), flushed via `serverapp.WithMetricsPush` — no collector required
(scraping and remote-write agents remain supported in docs/OBSERVABILITY.md).

Server merge-request diffs reuse the same engine: `usecase.MergeRequest.Diff`
resolves the source and target heads, `GetMergeBase`, and calls
`Branch.TreeDiffBetween` (`internal/usecase/browser.go`), which walks the two
commit trees in lockstep (`Branch.changedTreeEntries`, `tree_walk.go`): a pair
of directories with the same stored hash is skipped without a query, the others
are loaded with each side pruned by the caller's read filter, and the flattened
entries go to `diff.TreeDiffEntries(baseEntries, headEntries, chunkStore.Get)`
— a three-dot diff like GitHub/GitLab, exposed at
`GET /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}/diff`. The web
UI commit pages diff a commit against its first parent through the same
`CommitDiff` walk (`?path=` filters to a file/dir). A zero hash counts as
"unknown" and is never skipped. `commitChanges` (merge/fast-forward write
checks, binary lock planning) and `VisibleChunks` (which also skips directories
already visited for an earlier commit, keyed by path and hash) share the idea.

- `make bench-upload mb=512` (or `NIPA_BENCH_MB=512 go test ./internal/e2e
  -run TestEndToEnd_UploadThroughput -v`) measures upload throughput against an
  in-process server: it pushes a repetitive text file, a random binary file and
  a packed-asset `.png`, logs MiB/s, encoding, chunk and HTTP PUT counts, then
  re-clones and verifies the bytes. Skipped unless `NIPA_BENCH_MB` is set;
  `NIPA_BENCH_WORKERS` pins transfer concurrency (otherwise the adaptive tuner
  is exercised).
- Performance tooling (see `docs/PERFORMANCE.md`): `make bench-download mb=512`
  times a fresh clone plus a no-op update (`TestEndToEnd_CloneThroughput`),
  `make bench-scale gb=2` pushes/clones one N-GiB file and asserts peak heap
  growth stays below the file size (`NIPA_BENCH_GB`,
  `TestEndToEnd_GBScaleMemoryCeiling`), `make bench-unit` runs the
  chunker/diff/treehash micro-benchmarks, and `make load-test args='...'` runs
  the `tests/load` scenarios (push/clone/mixed, one branch and worktree per
  worker, client-side p50/p95/p99 plus a `/metrics` delta) against a running
  server. Requested metrics must be namespace-filtered (`nipa_*`), bench and
  load code stays env-gated so CI never runs it.
- `NIPA_E2E_LARGE_MANIFEST=1 go test ./internal/e2e -run
  TestEndToEnd_ManifestsOverGRPCDefault -v` runs the streamed push/clone/update
  e2e with 25000 files (manifests over the 4 MiB gRPC default); CI runs it with
  1000.

Docker image of the free server (`docs/DOCKER.md`): the root `Dockerfile`
builds the SPA and `cmd/nipad` on `$BUILDPLATFORM` (cross-compiled, no
emulation) into `gcr.io/distroless/static-debian12:nonroot`, with
`/app/config.yaml` derived from `config.yaml.sample` (data in the `/data`
volume, uid 65532) and no baked signing keys. `.github/workflows/docker.yml`
builds and smoke-tests it on `v*` tags (and PRs touching it), then pushes
`linux/amd64,linux/arm64` to `ghcr.io/nipalab/nipad` and, when the
`DOCKERHUB_USERNAME`/`DOCKERHUB_TOKEN` secrets exist, `docker.io/nipalab/nipad`;
`latest` and `major.minor` skip prerelease tags.

## Deployment (zero-downtime SaaS)

`docs/DEPLOY.md` is the runbook for running the SaaS on a VPS without Docker:
two `nipad-ee` colors (`nipad-ee@blue`/`@green`) behind Caddy, which terminates
TLS for the single h2c port and proxies to the active color. Assets live in
`deploy/` (`nipad-ee@.service` template, `nipad-ee.env.sample`, `caddy/Caddyfile`,
`caddy/caddy.env.sample` + `caddy/caddy.service.d/nipa.conf` for `MARKETING_UPSTREAM`,
`ci-deploy.sh` driven by `.github/workflows/server-deploy.yml` for the dev (main) and
production (`v*` or server-only `server-v*` tag) GitHub environments,
`upgrade.sh`). The upgrade script installs the new binary into the idle color,
waits for `/readyz`, rewrites the Caddyfile upstream + `systemctl reload caddy`
(a reload never drops connections), waits for the old color to drain, then
SIGTERMs it. `internal/serverapp` drains gracefully: on SIGTERM `/readyz`
answers 503 (`readinessHandler` + drain flag), the gRPC health service reports
NOT_SERVING, and one `SHUTDOWN_TIMEOUT_SECONDS` deadline (config default 15,
unit's `TimeoutStopSec` must stay above it) bounds the whole shutdown:
`httpServer.Shutdown`, the dispatcher stop and the exporter flushes. `Run`
returns only after the drain finished (`serveUntilShutdown`): `Serve` returns
the moment the listener closes, so returning then would exit the process
mid-request. Both colors may run
concurrently during a flip:
snowflake node ids are leased from postgres (never pin `SNOWFLAKE_NODE_ID`),
email deliveries are claimed atomically, but webhooks can double-deliver in the
overlap window (consumers must be idempotent). Migrations must stay forward
compatible for one release. The startup tree hash backfill
(`serverapp.BackfillTreeHashes`) covers tree nodes up to the highest id at its
start; rows the old color writes afterwards (including pushes that took their
ids before the pass and committed after it, rescanned through a 2h overlap
window, and kept-directory references carrying a hash the pass rewrote, which
read empty until repaired from `tree_hash_rewrites`) are fixed by the next
start of any new-color pod, so restart one once the old color is gone.

Transport hardening: neither server terminates TLS (one cleartext HTTP/1.1 +
h2c port), so every deployment, the free `nipad` included, needs a TLS proxy in
front (`docs/DEPLOY.md`). `newHTTPServer` (`internal/serverapp/run.go`) sets
only `ReadHeaderTimeout`/`IdleTimeout` (`HTTP_READ_HEADER_TIMEOUT_SECONDS` 30,
`HTTP_IDLE_TIMEOUT_SECONDS` 300; `Config.HTTPReadHeaderTimeout`/
`HTTPIdleTimeout`, <=0 = default) and never a read/write timeout, which would
cut chunk transfers and gRPC streams; on h2c the idle timer only runs while a
connection has no stream. `METRICS_ADDRESS` (`Config.MetricsListenAddress`,
bare port = `127.0.0.1`, per-color env bound) moves `/metrics` and
`/debug/pprof` to a second listener (`observabilityRoutes`; the main port then
404s them), opened before route setup, closed at the end of the shutdown;
empty keeps them on the main port. CORS (`internal/http/api/cors.go`) is a
container filter, so preflights reach it without OPTIONS routes, granting only
`Config.CORSAllowedOrigins` (the origin of `PUBLIC_BASE_URL`, bare host =
https; empty or invalid = none); other origins get no CORS headers and are not
rejected there (`sameOriginFilter` still 403s cross-origin writes). Client:
`Auth.WithCleartextWarning(os.Stderr)` (CLI main only) makes `nipa login` and
the implicit password/SSH login behind other commands print one stderr warning
per invocation when the dialed address (`grpc.Client.Address`) is not
`https://` and not loopback (`domain.IsCleartextRemote`); refreshes, the daemon
and `nipa mcp` stay silent.

