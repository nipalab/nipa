<!--
Handoff notes for the implementing session:
- Both editions: everything lives under internal/ (sqlite + postgres repos); ee/ only gets the
  nodelease pause, the ee main wiring and ee/e2e tests.
- Global rules: never commit to git; new code needs >=80% test coverage; keep comments minimal.
- Decisions taken with the user (2026-11-14): no admin bypass, manual end only, Paddle webhooks
  refused, email announcement and per-project scope in scope, a `frozen` mode for moving the
  database, engine plugins and an upgrade.sh flag out of scope.
- One point still to confirm: the announcement email ignores `notify_email` (like the MFA notice).
-->

# Maintenance mode: design and implementation plan

## 1. Goal

Let an operator put a Nipa server (free `nipad` or `nipad-ee`), or a single project, into
maintenance without stopping it, so users see a clear message instead of random failures, and
move the database to another host with writes paused for minutes only.

| mode | for | reads | user writes | sign-in | DB writes by the server |
|---|---|---|---|---|---|
| `read_only` | storage work, backups, repairs; server or project | ✓ | ✗ | ✓ | yes (sessions, audit, outboxes) |
| `full` | closed to users; server or project | ✗ | ✗ | ✓ | yes |
| `frozen` | copying or moving the database; server only | ✓ | ✗ | ✗ | **none** |

Out of scope: downtime where the process is stopped. That case is handled by a static Caddy
page (phase 8). Also out of scope: Unity/Godot UI (they keep showing the error message) and an
`upgrade.sh` flag.

## 2. Concepts

```
scope    server | project
mode     read_only | full | frozen          (frozen: server scope only)
window   { id, project_id?, mode, message, starts_at, ends_at?, notify,
           created_by, updated_at }
```

- **One window per scope.** At most one window for the server and one per project. History
  is kept in the audit log.
- **Active** when `now >= starts_at`. Before that the window is only an announcement
  (`scheduled`).
- **Effective mode** for a request is the stricter of the active server window and the active
  window of the targeted project. Order: `frozen` > `full` > `read_only` > `off`.
- **Manual end only.** `ends_at` is an estimate. It is shown to users and sent as
  `Retry-After`, but it never ends maintenance on its own.
- **No bypass.** Super admins, org owners and service tokens get the same limits as everyone
  else. Only the classes `public`, `auth` and `control` below stay open.
- **Override file** (`MAINTENANCE_FILE`, server scope only). When the file exists it replaces
  the server window from the DB:
  - an empty file means `full`;
  - otherwise it holds JSON `{mode, message, ends_at}`.

  It works over ssh without the API and keeps working when the DB is slow. Project windows
  still apply next to it.

### 2.1 Propagation (blue/green, multi-instance)

`maintenance.Service` holds an immutable `Snapshot` in an `atomic.Pointer`:

```go
type Snapshot struct {
    Server   *Window             // DB or file
    Source   string              // "db" | "file"
    Projects map[string]*Window  // "org/project" slug key
    ByID     map[snow.ID]*Window
    LoadedAt time.Time
}
```

`maintenance.Poller` is a `serverapp.Dispatcher`. Every `MAINTENANCE_POLL_SECONDS` (default 5)
it:

1. loads every window with one query (`MaintenanceList`, joined with
   `projects`/`organizations` for the current slugs, live projects only);
2. stats and parses the override file;
3. builds a new snapshot.

A failed load keeps the last snapshot (logged, metric) and never falls back to `off`. The
admin API and the CLI apply the new state locally right away; other instances catch up within
one poll. Requests only read the pointer, so the hot path has no DB query.

**Leaving `frozen`** (2.3): the poller calls the registered resume hooks before it publishes
a snapshot that is no longer frozen. If a hook fails, the old snapshot is kept and the
hook's own failure path runs.

### 2.2 Classes and rule table (`maintenance.Allowed(class, mode)`)

| class   | off | read_only | full | frozen |
|---------|-----|-----------|------|--------|
| public  | ✓   | ✓         | ✓    | ✓      |
| auth    | ✓   | ✓         | ✓    | ✗      |
| control | ✓   | ✓         | ✓    | GET only |
| read    | ✓   | ✓         | ✗    | ✓      |
| write   | ✓   | ✗         | ✗    | ✗      |

- `public`: `GetServerVersion`, `GetMaintenanceStatus`, the gRPC health service,
  `GET /api/maintenance`, `GET /api/v1/auth/capabilities`.
- `auth`: the login RPCs; `POST /auth/login|refresh|logout|mfa/verify`; OAuth
  `start`/`callback`/`complete`. `register` and `verify` are `write`.
- `control`:
  - the maintenance admin routes;
  - the project maintenance routes;
  - `GET /me` and `GET /permissions/me`, which the SPA shell needs to render the admin page.
- A project window only affects requests that target that project. Requests without a project
  (`ListMyProjects`, org settings, profile, other projects) see the server window only.

### 2.3 What `frozen` stops

| writer | behaviour in `frozen` |
|---|---|
| user writes, login, refresh, logout, register | refused (rule table) |
| login audit, sweeps of expired rows, service token `last_used_at` | not reached (their requests are refused) |
| webhook and email dispatchers | `run`/`sweep` loops skip claiming while `frozen()` |
| `quotaReconcileJob`, `TermsRefreshJob` | skipped while frozen |
| `ee/nodelease` renewal | paused while frozen. On resume the manager renews or reclaims before the poller publishes the unfrozen snapshot; a failed resume calls `onLost` (exit) |
| `maintenance.Poller` | reads only |

Why the lease must be renewed on resume: while renewal is paused, another instance that
starts may claim the same node id once its lease expires. Making the resume a hard gate
guarantees that no two processes mint ids with one node id. (`claim` only takes expired or
free ids, so the instance that resumes second loses and exits.)

During `frozen`, clients keep their stored sessions: the CLI `Session.Refresh` and the SPA
`refresh()` treat a maintenance 503 as "try later", never as a lost session.

## 3. Enforcement

### 3.1 gRPC: `internal/grpc/server/maintenance_interceptor.go`

- `type Maintenance struct{ svc *maintenance.Service; metrics *obs.Metrics }` with `Unary()`
  and `Stream()`. Interceptor chain: `Metrics → JWT → Maintenance → RateLimit`.
- `maintenance_classes.go`: `var methodClass = map[string]maintenance.Class{...}`, one entry per
  `NipaService` method, plus a set of `serverScopeOnly` methods.
- Target project: `req.(interface{ GetContext() *pb.ProjectContext })` → `ctx.Org` /
  `ctx.Project`; a nil context means server scope.
- `Stream()`: server scope only (`NipaService` has no streams today; health streams are
  `public`).
- Rejection, `maintenanceStatusError(w Window, scope, project)`:
  - `codes.Unavailable` with the readable message `server is in maintenance (read-only):
    <message>` or `project acme/game is in maintenance (closed): <message>`;
  - details `errdetails.ErrorInfo{Reason: "MAINTENANCE", Domain: "nipa", Metadata: {scope,
    mode, message, starts_at, ends_at, project}}`;
  - `errdetails.RetryInfo` when `ends_at` is in the future.
- Tests:
  - every method in `pb.NipaService_ServiceDesc.Methods` has a class;
  - every `read`/`write` method either has a `GetContext()` request type (checked by
    instantiating the request through `protoregistry`) or is listed in `serverScopeOnly`;
  - a table test of the rule over (class × mode × scope);
  - the error details round-trip.

### 3.2 REST: `internal/http/api/maintenance_filter.go`

- `maintenanceFilter(svc, metrics) restful.FilterFunction`, added to `authWs`, `apiWs` (after
  the bearer filter, so the selected route is known) and `chunkWs`. The paddle webhook
  WebService is registered in ee through `api.WithWebService`; `api.WithWebService` wraps it
  with the same filter as `write`.
- Class:
  - `routeClass[method + " " + req.SelectedRoutePath()]` for the `public`, `auth` and
    `control` routes;
  - otherwise `GET`/`HEAD`/`OPTIONS` = `read`, anything else = `write`.
- Target: `req.PathParameter("org")` + `req.PathParameter("project")` when both are set.
- Answer `503`:
  - headers `Retry-After` (seconds until `ends_at`, when set) and `X-Nipa-Maintenance:
    <scope>;<mode>`;
  - body `{"error": msg, "maintenance": {scope, mode, message, starts_at, ends_at,
    project}}`;
  - written directly with `resp.WriteHeaderAndJson`, never through a handler.
- Tests:
  - every route registered in the container resolves to a class (non-GET routes that are
    not `write` must be listed);
  - the allow/deny table per mode;
  - headers and body;
  - a project window on A leaves B untouched;
  - chunk PUT is refused in `read_only` and GET is refused in `full`.
- `/healthz`, `/readyz`, `/version`, `/metrics` and the static SPA/marketing files are
  untouched. `/readyz` stays 200 in every mode.
- Known gap (documented): presigned S3 GET URLs issued before a `full` window keep working
  until `CHUNK_PRESIGN_TTL_SECONDS`.

## 4. Surfaces

### 4.1 REST

| route | access | class |
|---|---|---|
| `GET /api/maintenance` (own WebService, no bearer, no rate limit) | anyone | public |
| `GET /api/v1/admin/maintenance` | super admin | control |
| `PUT /api/v1/admin/maintenance` | super admin | control |
| `DELETE /api/v1/admin/maintenance` | super admin | control |
| `GET /api/v1/orgs/{org}/projects/{project}/maintenance` | project read | control |
| `PUT /api/v1/orgs/{org}/projects/{project}/maintenance` | org owner or super admin | control |
| `DELETE /api/v1/orgs/{org}/projects/{project}/maintenance` | org owner or super admin | control |

- `GET /api/maintenance` answers `{server: Status|null, source}` and never returns project
  windows.
- The admin `GET` adds a `projects: [Status]` list of active and scheduled project windows.
- The project `GET` answers `{server, project}`.
- `PUT` body: `{mode, message, starts_at?, ends_at?, notify}`.
- `Status`: `{mode, active, message, starts_at, ends_at, project?, notified?}`.

### 4.2 gRPC

```proto
message MaintenanceWindow { string mode = 1; bool active = 2; string message = 3;
  google.protobuf.Timestamp starts_at = 4; google.protobuf.Timestamp ends_at = 5; }
message GetMaintenanceStatusRequest { ProjectContext context = 1; }
message GetMaintenanceStatusResponse { MaintenanceWindow server = 1; MaintenanceWindow project = 2; }
rpc GetMaintenanceStatus(GetMaintenanceStatusRequest) returns (GetMaintenanceStatusResponse);
```

`GetMaintenanceStatus` is `public` (added to `isPublicMethod` and `exemptFromRateLimit`).
`project` is filled only when the call carries a valid bearer token with read on that
project. The handler validates the token itself through the usual `Auth.ValidateToken` when
present. An invalid token or no access just leaves `project` empty.

### 4.3 Audit, metrics, config

- `internal/domain/audit_actions.go`: `maintenance.scheduled`, `maintenance.updated`,
  `maintenance.ended`. Metadata: `scope`, `project_id`, `mode`, `starts_at`, `ends_at`,
  `notify`. The CLI subcommand records the actor `system` with `via: "cli"`.
- `obs.Metrics`:
  - `nipa_maintenance_server_mode{mode}`: gauge, 1 for the effective server mode,
    `off` included;
  - `nipa_maintenance_project_windows{mode}`: gauge of active project windows;
  - `nipa_maintenance_rejected_total{transport,class,scope}`;
  - `nipa_maintenance_poll_errors_total`.
- `internal/config`:
  - `MAINTENANCE_FILE` (default empty);
  - `MAINTENANCE_POLL_SECONDS` (default 5, values below 1 become 1);
  - add both to the `config_test.go` key list.

### 4.4 Email announcement

- `usecase.MaintenanceMailer` (`internal/usecase/maintenance_notify.go`), built like
  `MFAMailer`:
  - writes `domain.EmailDelivery` rows with event `maintenance`;
  - `ThreadKey = "maintenance:<window id>"`;
  - `ProjectID` set for project windows;
  - `WithKicker(deps.EmailDispatcher)`.
- `PUT` with `notify: true` enqueues inside the window's transaction:
  - a new window sends "Scheduled";
  - a changed window sends "Updated".
- `DELETE` of a window that had `notify` set sends "Finished".
- Recipients (new queries):
  - `MaintenanceRecipientsServer`: active, verified users who own at least one live org;
  - `MaintenanceRecipientsProject(project_id)`: the owners of the project's org plus the
    users holding an admin rule on the project, with groups expanded.

  Both are distinct and ignore `notify_email`, since this is a service notice.
- Content: scope (server or `org/project`), mode in plain words, start and estimated end in
  UTC, the message, and what still works in that mode. It links to `EMAIL_BASE_URL` (or to
  the project for project windows).
- With no mailer wired (`EMAIL_SENDER=off`), `notify` is stored but nothing is queued, and the
  answer carries `notified: false`.

### 4.5 Clients

- **Transport** (`internal/client/grpc/grpc.go`):
  - `toDomainError` reads the `ErrorInfo` detail (`Reason == "MAINTENANCE"`) and returns
    `&domain.Error{Code: 503, Message, Hint, Maintenance: &domain.MaintenanceInfo{...}}`;
  - new `domain.IsMaintenance(err)` (`internal/client/domain`);
  - the hint per mode: read-only "read-only commands still work", closed "try again after
    ~<ends_at local>", frozen "sign-in is paused; commands that read work while your session
    lasts".
- **Retry** (`internal/client/grpc/retry.go`): a 503 with `X-Nipa-Maintenance` is not
  retryable (`httpRetryHint`), and neither is a gRPC maintenance error in the unary retry.
- **Session** (`internal/client/usecase/session.go`): `isRefreshRejection` returns false for
  `IsMaintenance`, so `Refresh` returns the error and keeps the stored tokens without a
  `reauth`. The same applies inside `reauth` for SSH/password logins.
- **Pristine** (`usecase/pristine.go`): `offlineError` passes maintenance errors through
  unchanged. They are checked before `codes.Unavailable`, through `status.FromError` details.
- **CLI**:
  - `nipa version --server` prints `maintenance: <mode> (<active|scheduled …>)`;
  - `nipa status` prints one line when the server or the clone's project has an active or
    scheduled window;
  - `nipa push` prints `maintenance starts in 12m` to stderr before uploading when one is
    scheduled within 24 h;
  - all three use one `GetMaintenanceStatus` call per command; a failure is ignored;
  - errors exit with the usual exit code, and the message and hint come from `domain.Error`.
- **MCP**: `toolError` adds `next: retry after <ends_at>` for maintenance errors.
- **Daemon** (`internal/client/daemon/events.go`):
  - `teamWatch.pollMaintenance` calls `GetMaintenanceStatus` with the root's context on every
    poll and fills `RepoActivity.maintenance` (new field 5, message `MaintenanceState{server,
    project}` reusing `greet.MaintenanceWindow`);
  - a change between polls appends a `TeamEvent` of kind `maintenance` with
    `scheduled|started|ended` in its text;
  - a maintenance error on the other poll calls doesn't set `RepoActivity.error`, so the GUI
    doesn't show "offline".
- **JSON bridge**: already maps `Unavailable` → 503. Its body adds the `maintenance` object
  from the details.

### 4.6 Desktop

- `desktop/internal/service/activity.go` carries `Maintenance` into the `Activity` binding
  (bindings regenerated).
- `components/MaintenanceBanner.tsx` sits at the top of `RepoView`:
  - scheduled (info), read-only (attention), closed or frozen (danger);
  - names the scope and shows the estimate.
- `hooks/useMaintenance.ts` gives the effective mode for the current clone.
- Disabled while active, with a tooltip:
  - in `read_only`: Push in `CommitBox`, lock/unlock, MR create/merge/review/comment, branch
    and tag create/delete;
  - in `full`/`frozen`: those plus Update, Switch and Clone.

  Stage, shelve, diff and local file actions stay enabled.
- A desktop notification on `maintenance` events, under a new notification kind
  `maintenance` in Settings (on by default).

### 4.7 Web UI

- `web/src/maintenance.ts`: a context store holding `{server, project?}`.
  - `useMaintenance()`;
  - `noteMaintenance(body)`, called from `apiFetch` when a response carries
    `X-Nipa-Maintenance`;
  - polls `GET /api/maintenance` every 60 s, plus the project endpoint on project routes.
- `web/src/api/client.ts`:
  - `apiFetch` calls `noteMaintenance`;
  - `refresh()` treats a maintenance 503 as "keep the session" and does not call
    `handleSessionLost()`;
  - `errorFromResponse` keeps the message.
- `components/MaintenanceBanner.tsx` in `AppShell` above `TermsGate`. `Banner` requires a
  `title`. Variants: scheduled (info), read-only (warning), closed or frozen (critical).
- `pages/MaintenancePage.tsx` replaces the routed content:
  - server `full`/`frozen`: every route except `/login` and `/admin/maintenance`;
  - project `full`: the project routes except the settings Maintenance tab.
- Read-only: the MR merge box and "New merge request" are disabled with a note. Other
  controls rely on the server's 503 and the banner.
- `pages/AdminMaintenancePage.tsx` at `/admin/maintenance`, linked next to `/admin/users`:
  - the current server window with its source (a file-override notice when it comes from the
    file);
  - a list of project windows;
  - a form: mode, message, start now or at a time, estimated end, "Email org owners";
  - "End maintenance".
- Project settings tab `maintenance` (org owners): the same form without `frozen`, with "Email
  org owners and project admins".
- `api/endpoints.ts`: `getMaintenance`, `getAdminMaintenance`, `setServerMaintenance`,
  `endServerMaintenance`, `getProjectMaintenance`, `setProjectMaintenance`,
  `endProjectMaintenance`, plus types.
- `docs/WEB_UI.md` routes.

## 5. Storage

### 5.1 Schema (sqlite `000024_maintenance`, postgres `000009_maintenance`)

```sql
CREATE TABLE maintenance_windows (
    id         BIGINT    PRIMARY KEY,
    project_id BIGINT,                       -- postgres: REFERENCES projects(id) ON DELETE CASCADE
    mode       TEXT      NOT NULL CHECK (mode IN ('read_only','full','frozen')),
    message    TEXT      NOT NULL DEFAULT '',
    starts_at  TIMESTAMP NOT NULL,           -- postgres: TIMESTAMPTZ
    ends_at    TIMESTAMP,
    notify     BOOLEAN   NOT NULL DEFAULT FALSE,
    created_by BIGINT    NOT NULL,           -- 0 = cli
    updated_at TIMESTAMP NOT NULL,
    CHECK (project_id IS NULL OR mode <> 'frozen')
);
CREATE UNIQUE INDEX maintenance_windows_project ON maintenance_windows (project_id) WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX maintenance_windows_server  ON maintenance_windows ((project_id IS NULL)) WHERE project_id IS NULL;
```

- Soft deletes clean up through triggers, so no usecase changes (same pattern as
  `file_lock_requests_cleanup`; the server sqlite connection runs without foreign keys):
  - `maintenance_windows_project_cleanup`: `AFTER UPDATE OF deleted ON projects WHEN
    NEW.deleted` deletes the project's window (sqlite trigger; postgres trigger + plpgsql
    function of the same name);
  - `maintenance_windows_org_cleanup`: `AFTER UPDATE OF deleted ON organizations WHEN
    NEW.deleted` deletes the windows of the org's projects (both dialects);
  - sqlite `maintenance_windows_project_delete` covers a hard `DELETE` (postgres has the
    `ON DELETE CASCADE` foreign key).
- `MaintenanceList`/`MaintenanceGetProject` also skip windows of deleted projects or orgs.
- Migrations are numbered like the existing ones (`000024`/`000009`), not with the
  timestamp names `make migrate-create` would produce.
- Down migrations drop the triggers, functions and table.

### 5.2 Queries (`db/queries/sqlite/maintenance.sql`, `ee/repository/postgres/queries/maintenance.sql`)

- `MaintenanceList`: windows joined with project and org slugs.
- `MaintenanceGetServer` and `MaintenanceGetProject`.
- `MaintenanceUpsertServer` and `MaintenanceUpsertProject`: `INSERT … ON CONFLICT DO
  UPDATE`. On sqlite every parameter is named with `sqlc.arg`.
- `MaintenanceDelete WHERE id = sqlc.arg(id)`.
- `MaintenanceRecipientsServer` and `MaintenanceRecipientsProject`: live, verified users;
  a project admin is a user or group member with a root (`path_prefix = ''`) rule carrying
  `PermissionAdmin` on the project or org-wide in its org (deleted groups ignored).
- Run `make sqlc` afterwards.

### 5.3 Repository

- `usecase.maintenanceRepository` interface (`internal/usecase/maintenance.go`): `List`,
  `GetServer`, `GetProject`, `UpsertServer`, `UpsertProject`, `Delete` (bool = a row was
  removed), `RecipientsServer`, `RecipientsProject`, over `domain.MaintenanceWindow` (with
  `OrgSlug`/`ProjectSlug` on reads) and `domain.MaintenanceRecipient`. An upsert keeps the
  stored `id` and `created_by` of an existing window.
- `sqlite.NewMaintenanceRepository(db)` and `postgres.NewMaintenanceRepository(db)`, both
  built through `dbtx.New`.
- Added as `Maintenance` in `usecase.Repositories` and in both mains and `internal/e2e`.

## 6. Code layout

- `internal/maintenance` (pure: no repo or usecase imports):
  - `mode.go`: `Mode` (`off` plus the three window modes), `ParseMode`, `Stricter`, `Class`,
    `Allowed(class, mode)` and `AllowedMethod(class, mode, method)` (control in `frozen`
    is open for `GET`/`HEAD` only; `Allowed` treats it as a write);
  - `window.go`: `Window`, `Status`, `Active(now)`, `FromDomain`;
  - `file.go`: `ParseFile` (empty = `full`, unknown fields or modes are errors);
  - `service.go`: `Service`, `Snapshot` (+ `FileError`), `NewSnapshot(windows, at)`,
    `For(org, project) (Window, Scope)` (mode `off` and empty scope when nothing is active,
    a tie keeps the server scope), `Frozen()`, `Apply(ctx, *Snapshot) error`,
    `OnResume(func(ctx) error)`. The resume gate lives in `Apply`, so the poller and the
    usecase's local apply both run the hooks before leaving `frozen`;
  - `poller.go`: `NewPoller(svc, load, file, interval)` with `WithClock`/`WithErrorHook`,
    `Poll(ctx)`, `Start`/`Stop` over `dispatch.Runner`. A failed load reuses the stored
    windows of the current snapshot (the file is still read); an unreadable or invalid file
    publishes `full` with the error in `Snapshot.FileError`;
  - `info.go`: the shared wire shape `Info` (+ `Metadata`/`InfoFromMetadata` for the gRPC
    `ErrorInfo`, `RetryAfter`); `errors.go`: `Error` with the readable message and
    `AsError`.
- `internal/usecase/maintenance.go`: `Maintenance` with `Status`, `AdminStatus`,
  `SetServer`, `SetProject`, `EndServer`, `EndProject`, `ProjectStatus`. It also has
  `WithAudit`, `WithMailer` and `WithService` (applies the new state locally after the
  commit) plus a `WithSystemActor()` variant for the CLI.
  - Permissions: `IsSuperAdmin` for the server window; `orgs.IsOrgOwner` or super admin for a
    project window.
  - Validation: message ≤ 500 runes, `ends_at > starts_at`, `starts_at` ≤ now + 90 days,
    `frozen` only for the server.
  - A server `PUT` while the snapshot source is `file` → 409.
- `internal/serverapp`:
  - `UsecaseDeps.Maintenance *maintenance.Service`, built in both mains from config;
  - `BuildUsecases` wires the usecase;
  - `WithMaintenance(svc, poller)` passes the service to `api.WithMaintenance`, the gRPC
    interceptor and the poller dispatcher.
- `internal/serverapp/maintenance_cmd.go`: `RunMaintenanceCommand(args, open func()
  (Repositories, *sql.DB, error), node snow.Node, out io.Writer) (handled bool, err error)`,
  called first in both mains (after `version.Flag`).
  - Parses `maintenance status|set|end` (std `flag`) and runs the usecase with the system
    actor.
  - Never runs migrations: it fails with "schema too old, start the server once" when the
    table is missing.
- Snow node for the subcommand: `snow.ReservedCLINode = 255`.
  - `ee/nodelease` never claims it (candidate range 0..254).
  - The free main refuses `SNOWFLAKE_NODE_ID=255`.
  - The subcommand uses it, so its ids never collide with a running server.

## 7. Moving the database (operator runbooks, in `docs/DEPLOY.md`)

### 7.1 Postgres (`nipad-ee`)

1. **Before.** Set up a streaming replica on the new VPS with `pg_basebackup -R`, same major
   version. Check `pg_stat_replication`. Schedule the window with `nipad-ee maintenance set
   frozen --start <T0> --end <T0+30m> --notify -m "..."` (or through the admin page).
   - For a major-version upgrade, use logical replication. Then, before promotion, sync every
     `GENERATED … AS IDENTITY` sequence: `SELECT setval(pg_get_serial_sequence(t, 'id'),
     max(id))` for each such table. The runbook lists the tables.
2. **T0.** The window activates. Wait one poll plus `SHUTDOWN_TIMEOUT_SECONDS` for in-flight
   requests. Guard the old primary: `ALTER SYSTEM SET default_transaction_read_only = on;
   SELECT pg_reload_conf();`.
3. Wait until `pg_current_wal_lsn()` on the old primary equals `replay_lsn` for the replica.
   Then run `SELECT pg_promote();` on the new host.
4. Change `DATABASE_DSN` in the shared env file. Start the idle color: it runs against the new
   host, leases a node id there, and its migrations are a no-op. Wait for `/readyz`, switch
   the Caddy upstream and reload, drain, then stop the old color. These are the same steps as
   `upgrade.sh` without a new binary.
5. **Check.** The new color reports `frozen`, because the row was copied. Clone, browse, and
   check `nipad-ee maintenance status`.
6. **Point of no return:** `nipad-ee maintenance end` on the new host.
   - Rollback before this step: revert the DSN, switch Caddy back to the old color, then
     `ALTER SYSTEM RESET default_transaction_read_only` and reload. Neither side has been
     written to.
7. Keep the old database for a few days, then drop it.

`JWT_KEY`, `MFA_ENCRYPTION_KEY` and `CHUNK_URL_SIGNING_KEY` must stay identical. Chunks stay
in S3.

### 7.2 Sqlite (`nipad`)

1. **Before.** Install `nipad` on the new VPS with the same env secrets. `rsync -a` the
   `CHUNK_STORAGE_DIR` as often as you like. It only ever grows, so later passes are deltas.
2. **T0.** `nipad maintenance set frozen` takes effect within one poll. Then:
   - `sqlite3 nipa.db ".backup nipa-move.db"` (online backup API, WAL-safe);
   - a final `rsync` of the chunks;
   - copy the backup to the new host as `nipa.db`.
3. Start `nipad` on the new host. It reports `frozen`. Point Caddy or DNS at it and check.
4. `nipad maintenance end` on the new host. Rollback before this step: point the proxy back
   and run `nipad maintenance end` on the old host.

## 8. Pull requests

The work ships as nine PRs, merged in order. Each one:

- builds and passes `go vet ./...`, `go test -race` on the touched packages and `make lint`;
- has ≥ 80% coverage on new code;
- includes its generated code (`make sqlc`, `make proto`, `make mock`, desktop bindings);
- updates `AGENTS.md` and the docs for what it adds.

Every PR leaves `main` releasable: nothing user-visible changes before PR 3, and `frozen` can
only be set from PR 4 on.

| PR | title | depends on | user-visible change |
|---|---|---|---|
| 1 | Add maintenance core and storage | – | none |
| 2 | Add maintenance admin API and `nipad maintenance` command | 1 | windows can be set and read; nothing enforced |
| 3 | Enforce read-only and closed maintenance | 2 | `read_only`/`full` block requests |
| 4 | Add frozen maintenance for database moves | 3 | `frozen` mode; sessions kept by the CLI and SPA |
| 5 | Email maintenance announcements | 2 | `notify` sends mail |
| 6 | Show maintenance in the CLI, daemon and MCP | 3 | clear errors, no retries, status lines, daemon event |
| 7 | Show maintenance in the web UI | 3 | banner, closed page, admin page, project tab |
| 8 | Show maintenance in the desktop app | 6 | banner, disabled actions, notification |
| 9 | Document maintenance operations and database moves | 4 | runbooks, Caddy fallback page |

PRs 5, 6 and 7 can be developed in parallel after PR 3 (PR 5 only needs PR 2).

Splitting the original phases this way keeps the two session fixes (CLI `Session.Refresh`,
SPA `refresh()`) in PR 4, so `frozen` never ships without them. The `offlineError`
passthrough moves into PR 3, so a refused restore never reports "connect to the server".

### PR 1: maintenance core and storage

Phases 1 and 2 below. The package is pure and the table is unused until PR 2.

### PR 2: admin API and `nipad maintenance` command

Phase 3 below.

### PR 3: enforce read-only and closed maintenance

Phase 4 below, plus the `pristine.go` `offlineError` passthrough with its unit test (a
maintenance error is checked through `status.FromError` details before `codes.Unavailable`).

### PR 4: frozen maintenance for database moves

Phase 5 below, plus:

- `internal/client/usecase/session.go`: `isRefreshRejection` is false for maintenance errors,
  so tokens are kept and there's no `reauth`. This needs `domain.IsMaintenance` and the
  `toDomainError` detail mapping, which move here from phase 7.
- `web/src/api/client.ts`: `refresh()` keeps the session on a maintenance 503.
- Tests for both, and the `tests/suite` frozen-session spec.

### PR 5: email announcements

Phase 6 below.

### PR 6: CLI, daemon and MCP

Phase 7 below, minus the parts already shipped in PRs 3 and 4.

### PR 7: web UI

The SPA half of phase 8.

### PR 8: desktop app

The desktop half of phase 8.

### PR 9: operations and docs

Phase 9 below. The per-feature doc updates already landed with each PR; this one adds
`docs/MAINTENANCE.md`, the `docs/DEPLOY.md` runbooks, the Caddy maintenance page and the
release-checklist rehearsal.

## 9. Phase details

### Phase 1: core package
- `internal/maintenance` complete (mode, window, file, service, poller, info).
- Tests:
  - `Allowed` table (4 classes × 4 modes);
  - `Stricter`;
  - `Active` around `starts_at`;
  - `ParseFile` (empty, valid, invalid JSON → error kept in status, unknown mode);
  - `For` (server only, project only, both → stricter, unknown project);
  - poller (load error keeps the snapshot; file appears or disappears; slug change; a resume
    hook failure keeps the frozen snapshot; a hook success publishes);
  - `Stop` honours ctx.
- Done when the package coverage is ≥ 90% and the package has no repo or usecase imports.

### Phase 2: storage
- Migrations for both dialects, queries, `make sqlc`, both repositories, the sqlite trigger.
- Tests:
  - sqlite in a tempdir and postgres in the testcontainers suite: upsert/get/list/delete;
  - the unique server window;
  - `frozen` refused for a project by the CHECK;
  - cascade or trigger on project delete;
  - recipients (owners, project admins through groups, deleted users and orgs excluded,
    duplicates removed).
- `usecase.Repositories.Maintenance`, wired in both mains and `internal/e2e`.

### Phase 3: usecase, admin API, CLI subcommand
- `usecase.Maintenance` and the `MaintenanceMailer` interface seam (nil mailer for now).
- Gomock tests:
  - permissions (super admin, org owner, project admin denied, member denied);
  - validation;
  - the file-source 409;
  - audit rollback;
  - `Service` applied only after commit.
- Handlers and DTOs (`internal/http/handler/maintenance.go`, `model/maintenance.go`), routes
  (`api/maintenance.go`), the public WebService, `api_test.go` cases, swagger.
- Metrics gauges set from `Service.Apply`.
- Config keys. Both mains build the service and poller; `serverapp.WithMaintenance` registers
  the poller as a dispatcher. Nothing is enforced yet.
- `RunMaintenanceCommand` + `snow.ReservedCLINode`, plus the nodelease range change and the
  free-main guard. Tests over a sqlite tempdir for status, set and end, the audit actor and the
  missing-table error.

### Phase 4: enforcement
- The gRPC interceptor, the classes and the `GetMaintenanceStatus` RPC (`make proto`, handler
  in `internal/grpc/server/maintenance.go`, `isPublicMethod`, `exemptFromRateLimit`).
- The REST filter, `routeClass`, wrapping of `api.WithWebService` for the paddle WebService.
- Tests: the ServiceDesc and route coverage tests (3.1, 3.2) plus the rule tests.
- `internal/e2e` harness tests:
  - schedule → writes still pass;
  - server `read_only` → clone and update pass; push, lock and MR comment are refused (a
    super admin too);
  - `full` → reads refused, while login and the admin GET/DELETE pass;
  - project `read_only` on A → push to A refused, push to B accepted;
  - project `full` on A → `ListMyProjects` still lists A;
  - end → push passes;
  - the rejection counter moves.

### Phase 5: `frozen`
- `Service.Frozen()` seams:
  - `webhook.Dispatcher.WithPause(func() bool)` and `mail.Dispatcher.WithPause`, checked in
    `run` and `sweep` before claiming;
  - `quotaReconcileJob` and `TermsRefreshJob` skip their run;
  - `nodelease.Manager.WithPause(func() bool)`, under which `keepAlive` skips while paused
    and `lastRenewed` is not checked;
  - `Manager.Resume(ctx) error` (renew or reclaim the same id, else an error), registered
    through `Service.OnResume`, which calls `onLost` on failure.
- Auth classes refused in `frozen` (already in the table; test them).
- Tests:
  - unit tests for each pause and resume path (nodelease against the testcontainers
    postgres, including "another holder took the id while paused → resume fails → onLost");
  - `ee/e2e`: a frozen workload (clone, browse, MR view) against a postgres with
    `default_transaction_read_only = on` passes; a push is refused with 503, not a 500;
  - `internal/e2e` (sqlite): the same workload with a write-counting driver wrapper on the
    connection asserts zero writes;
  - move rehearsal: frozen → `.backup` → a second server on the copy reports frozen →
    `RunMaintenanceCommand end` → push passes.

### Phase 6: email announcement
- `MaintenanceMailer` with three templates (scheduled, updated, finished), `ThreadKey`,
  `ProjectID`, and the kicker; wired in `BuildUsecases` when `EmailDispatcher != nil`.
- Usecase tests:
  - recipients per scope;
  - notify false → no mail;
  - updated vs scheduled;
  - end sends finished only when `notify` was set;
  - no mailer → `notified: false`.
- The harness mail receiver suite (`tests/suite`, both editions): one mail per owner, reply
  threading through `In-Reply-To`.

### Phase 7: clients
- Transport: `toDomainError`, `domain.IsMaintenance`, the retry exclusions, `Session`,
  `pristine.go`.
- CLI lines in `version`, `status` and `push`; the MCP hint.
- Daemon: `pollMaintenance`, the `RepoActivity.maintenance` field and event (`make proto`);
  the JSON bridge body.
- Tests:
  - unit: `toDomainError` details; the 503 not retried; `Refresh` keeps tokens and doesn't
    `reauth`; `offlineError` passthrough; the event diffing in `events_test.go`;
  - `tests/suite/maintenance_test.go` (free and ee):
    - toggling through REST with super-admin and org-owner identities;
    - the CLI messages and exit codes for push, lock and mr;
    - `nipa clone` passes in read-only and fails in full;
    - in `frozen` with an expired access token, the stored session is still there after the
      error;
    - the `nipa serve` `WatchEvents` event;
    - the `nipa mcp` write tool error.

### Phase 8: desktop and web UI
- Desktop: the activity binding, `MaintenanceBanner`, `useMaintenance`, disabled actions, the
  notification kind.
- Desktop tests: Go `activity_test.go`; vitest for the banner and the disabled Push/Lock;
  `make desktop-test`.
- SPA:
  - `maintenance.ts`, the `client.ts` changes (the refresh keep-session case is tested
    explicitly);
  - `MaintenanceBanner`, `MaintenancePage`, `AdminMaintenancePage`, the project settings tab;
  - routes and endpoints.
- SPA tests: vitest for each; `npm run lint`, `npm test`, `make web`.

### Phase 9: operations and docs
- `docs/MAINTENANCE.md`: modes, scopes, rule table, the no-bypass rule, API, override file,
  subcommand, email, client behaviour, the presigned URL gap.
- `docs/DEPLOY.md`:
  - "Maintenance windows";
  - "Moving the database" (7.1 and 7.2 with exact commands, the sequence list and rollback);
  - "Hard downtime": Caddy `handle_errors 502 503` + `file_server` with
    `deploy/caddy/maintenance.html`, added to `deploy/caddy/Caddyfile` behind a
    `@maintenance_page` snippet.
- Updates:
  - `AGENTS.md`: a "Flow for maintenance mode" paragraph;
  - `docs/SERVER_GUIDE.md`: config and subcommand;
  - `docs/OBSERVABILITY.md`: metrics;
  - `docs/AUDIT_LOG.md`: actions;
  - `docs/EMAIL.md`: the event;
  - `docs/WEB_UI.md`: routes;
  - `docs/DAEMON.md`: the field and event;
  - `docs/RELEASE_CHECKLIST.md`: a rehearsal item for the move.

## 10. Risks and checks

- **A new RPC or route is not classified.** The coverage tests in phase 4 fail.
- **A write path is missed in `frozen`.** The postgres read-only guard plus the zero-write
  sqlite test catch it. In production the guard turns it into an error instead of lost data.
- **Lease collision when leaving `frozen`.** Caught by `Resume` as a hard gate (2.3).
- **The SPA or CLI logs users out during `frozen`.** Covered by explicit tests in phases 7
  and 8.
- **The admin page is not reachable in `full`.** Avoided by the `control` class (`/me`,
  `/permissions/me`) and the SPA route exceptions. The CLI subcommand is always the fallback.
- **The migration stays forward compatible for one release** (only a new table), as required
  for blue/green.
