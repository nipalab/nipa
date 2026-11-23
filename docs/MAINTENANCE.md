# Maintenance mode

Operator guide for putting a Nipa server (free `nipad` or `nipad-ee`), an
organization or a project into maintenance without stopping the process, and
for the planned operations that rely on it: moving the database, upgrades and
hard downtime behind Caddy.

## Concepts

| mode | meant for | reads | writes | sign-in, refresh, logout | server's own DB writes |
|---|---|---|---|---|---|
| `read_only` | storage work, backups, repairs | yes | refused | yes | yes (sessions, audit, outboxes, deliveries) |
| `full` (shown as "closed") | closing a scope to users | refused | refused | yes | yes |
| `frozen` (server only) | copying or moving the database | yes | refused | refused | none |

- **Scopes.** One window per scope at most: the server, each organization,
  each project. A project request is covered by the server window, its org's
  window and its own; an org request (members, groups, rules, creating a
  project) by the server and org windows; a request without a target
  (`ListMyProjects`, `GET /orgs`, the profile) by the server window only.
  Listings still show orgs and projects that are in maintenance.
- **Strictest wins.** `frozen` > `full` > `read_only`. On a tie the broadest
  scope is reported (server > org > project), the one that ends last.
- **Scheduled vs active.** A window with a future start is an announcement
  only; it becomes active at `starts_at` on every instance within one poll.
- **Manual end only.** `ends_at` is an estimate shown to users and sent as
  `Retry-After`; nothing ends a window except `end` (or removing the override
  file).
- **No bypass.** Super admins, org owners and service tokens get the same
  limits as everyone else.
- **Propagation.** Every instance reloads the windows (one query) and the
  override file every `MAINTENANCE_POLL_SECONDS` (default 5, min 1) and keeps
  them in memory, so requests never query for maintenance. A failed reload
  keeps the last state (`nipa_maintenance_poll_errors_total`). The instance
  that took the change applies it at once; others follow within one poll.

### What each request class does

| class | examples | `read_only` | `full` | `frozen` |
|---|---|---|---|---|
| public | `GET /api/maintenance`, `GetMaintenanceStatus`, `GetServerVersion`, gRPC health, `GET /api/v1/auth/capabilities` | open | open | open |
| auth | password, SSH and MFA logins, `POST /api/v1/auth/login\|refresh\|logout\|mfa/verify`, OAuth `start`/`callback`/`complete` | open | open | refused |
| control | the maintenance routes, `GET /api/v1/me`, `GET .../projects/{project}/permissions/me` | open | open | `GET`/`HEAD` only |
| read | clone, update, log, diff, chunk downloads, every other `GET` | open | refused | open |
| write | push, locks, merge requests, branches, tags, settings, chunk uploads, registration and verification, Paddle webhooks | refused | refused | refused |

Untouched in every mode: `/healthz`, `/readyz` (stays 200), `/version`,
`/metrics` and the web UI files.

Refusals are gRPC `UNAVAILABLE` with an `ErrorInfo` (reason `MAINTENANCE`,
domain `nipa`, metadata `scope`, `mode`, `message`, `starts_at`, `ends_at`,
`org`, `project`) plus a `RetryInfo` while the end is ahead, and HTTP `503`
with `X-Nipa-Maintenance: <scope>;<mode>`, `Retry-After` (when an end is set)
and the body:

```json
{"error": "project acme/game is in maintenance (read-only): Storage upgrade",
 "maintenance": {"scope": "project", "mode": "read_only", "message": "Storage upgrade",
                 "starts_at": "...", "ends_at": "...", "org": "acme", "project": "acme/game"}}
```

Paddle retries refused webhooks on its own schedule.

## Setting and ending windows

### From the host: `nipad[-ee] maintenance`

The command opens the database directly (never migrates it), so run it where
the server's `config.yaml` is the working directory and with a user that can
read it. On the SaaS host layout (`docs/DEPLOY.md`):

```sh
cd /etc/nipa && sudo -u nipa /opt/nipa/$(cat /var/lib/nipa/active-color)/nipad-ee maintenance status
```

```
maintenance status
maintenance set <read_only|full|frozen> [--org <slug> | --project <org/project>] [-m <message>] [--start <RFC3339>] [--end <RFC3339>] [--notify]
maintenance end [--org <slug> | --project <org/project>]
```

- `--start` defaults to now for a new window and keeps the stored start of an
  existing one; `--end` is the estimate. Times are RFC3339
  (`2026-11-20T22:00:00Z`). The start may be at most 90 days ahead, the end
  must follow the start, the message is at most 500 characters, `frozen` is
  server only.
- `set` on an existing window updates it (same id, same thread of emails).
- Changes are audited as actor `system` with `via: cli`.
- `schema too old, start the server once`: the database predates the
  `maintenance_windows` table; start the new server version first.
- Use the command whenever the API cannot help: a `full` window you lock
  yourself out of, and always to end `frozen`.

### Over HTTP and in the web UI

| route | who | class |
|---|---|---|
| `GET /api/maintenance` | anyone (server window only, `{server, source}`) | public |
| `GET\|PUT\|DELETE /api/v1/admin/maintenance` | super admin (GET adds the org and project windows) | control |
| `GET\|PUT\|DELETE /api/v1/orgs/{org}/maintenance` | GET: org members (owners also see the project windows); PUT/DELETE: org owner or super admin | control |
| `GET\|PUT\|DELETE /api/v1/orgs/{org}/projects/{project}/maintenance` | GET: project readers (`{server, org, project}`); PUT/DELETE: org owner or super admin | control |

`PUT` body: `{"mode", "message", "starts_at", "ends_at", "notify"}`; the answer
is the stored window with `notified`. Ending a missing window is a 404, a
refused read a 403. Project admins cannot set windows.

The SPA has `/admin/maintenance` (super admins, user menu "Maintenance") and a
Maintenance tab in the org and project settings (org owners and super
admins). See `docs/WEB_UI.md`.

### Override file: `MAINTENANCE_FILE`

When `MAINTENANCE_FILE` names a file that exists, it replaces the stored
server window on that instance (org and project windows still apply). An
empty file means `full`; otherwise it is JSON:

```json
{"mode": "read_only", "message": "Disk replacement", "ends_at": "2026-11-20T23:00:00Z"}
```

`mode` defaults to `full`; unknown fields, unknown modes or an unreadable file
fail closed (`full`, with the error kept for the status). Removing the file
ends it within one poll. While it exists, setting or ending the server window
through the API, the SPA or the command answers `409 maintenance is
controlled by MAINTENANCE_FILE`, and `GET /api/maintenance` reports
`"source": "file"`. A file window has no `starts_at`.

It needs no database and no token, so it is the switch to use when the
database is slow or unreachable. Put `MAINTENANCE_FILE` in `config.yaml`
(not only in a color's env file) so both colors and the `maintenance` command
see the same file, and keep it readable by the server user (for example
`/etc/nipa/maintenance.json`). Being per host, it does not travel with a
database copy.

## Observability and audit

Metrics (`docs/OBSERVABILITY.md`):

| metric | meaning |
|---|---|
| `nipa_maintenance_server_mode{mode}` | 1 for the effective server mode (`off` included) |
| `nipa_maintenance_windows{scope,mode}` | active org and project windows |
| `nipa_maintenance_rejected_total{transport,class,scope}` | refused requests |
| `nipa_maintenance_poll_errors_total` | failed reloads (including a failed `frozen` resume) |

Audit actions (`docs/AUDIT_LOG.md`): `maintenance.scheduled`,
`maintenance.updated`, `maintenance.ended`, target the window, metadata
`scope`, `org_id`, `project_id`, `mode`, `starts_at`, `ends_at`, `notify`.

To see what one instance enforces, ask it directly:
`curl -s http://127.0.0.1:<port>/api/maintenance`.

## Email announcements

`notify` (`--notify`, the "Email ..." checkbox) emails the people a window
affects (`docs/EMAIL.md`): "scheduled" when a window is created or announced
for the first time ("started" when already active), "updated" when an
announced window changes, "finished" when a window last stored with `notify`
ends. One thread per window (`maintenance:<window id>`).

Recipients: server window, the verified owners of every live org; org window,
its owners plus root admins (direct or group, org-wide or on any live
project); project window, the org's owners plus the project's admins. The
answer's `notified` is true only when something was queued (`false` with
`EMAIL_SENDER=off`, without `notify` or without valid recipients).

Defaults pending confirmation:

- the announcement ignores the users' `notify_email` opt-out (service notice);
- org-wide admins get org emails even when the org has no live project;
- `maintenance set|end` on the host queues the rows itself (when the loaded
  config has an email sender); the running server sends them on its next
  email poll (`EMAIL_POLL_SECONDS`).

Email delivery pauses while `frozen`, so announce a database move by
scheduling the window ahead (`--start`): the "scheduled" email goes out before
the freeze.

## Client behaviour

- **CLI.** A refusal prints `<scope> is in maintenance (<mode>): <message>`,
  then `estimated end: <UTC> (maintenance ends manually)` and a hint
  (read-only: "read-only commands still work"; closed: "try again after
  ~<local time>"; frozen: "sign-in is paused; commands that read work while
  your session lasts"). Exit code 1, nothing is retried (chunk transfers
  included) and the stored session is kept: a refused token refresh or
  sign-in never prompts for a password or drops the tokens.
  `nipa status` and `nipa version --server` print one
  `maintenance: <scope> <mode> [from <start> (scheduled)] [until ~<end>][: <message>]`
  line per window (`nipa version --server` prints `maintenance: off` when
  none); `nipa push` refuses before scanning when an active window covers the
  clone and prints `maintenance starts in <duration>: ...` for one starting
  within 24 h. `--json` outputs carry `maintenance`.
- **MCP.** Failures add `next: retry after <ends_at> (an estimate;
  maintenance ends manually)` or `retry later`.
- **Daemon (`nipa serve`).** `WatchEvents` reports the current windows in
  `RepoActivity.maintenance` and a `maintenance` event (`scheduled`,
  `started`, `updated`, `ended`); a maintenance refusal never marks the clone
  offline. Failed operations carry `OpFailure.maintenance`, and the JSON
  bridge answers 503 with `maintenance` (`docs/DAEMON.md`).
- **Desktop.** Builds on the daemon fields above (banner, disabled actions,
  notification; `docs/DESKTOP.md`).
- **Web UI.** Banner per covering window, a maintenance page for `full`
  (and for `frozen` once the session cannot be refreshed), "New merge
  request" and merge disabled during a window, and the session kept through
  `frozen` (`docs/WEB_UI.md`).

## `frozen` in detail

- Paused while frozen: webhook and email dispatchers (sweeps, claims, trims;
  a job already claimed waits instead of being dropped), the quota reconcile
  job (waits for the end), the terms refresh, service token `last_used_at`
  touches and the `nipad-ee` node lease renewal and release.
- Leaving `frozen`: each `nipad-ee` instance first renews its node lease (or
  reclaims the same id) and only then publishes the unfrozen state. If
  another instance took the id meanwhile it exits (systemd restarts it); if
  the database is unreachable it stays frozen and retries on the next poll.
- Writes that remain possible: startup migrations and the lease claim of an
  instance started on a frozen database, deliveries already past the pause
  check when the window started (finished within one poll plus
  `SHUTDOWN_TIMEOUT_SECONDS`), and the `maintenance` command itself.
- Ending: the maintenance `PUT`/`DELETE` routes are refused while frozen, so
  end it from the host (`maintenance end`, or remove the override file).
- Signed-in users keep reading until their access token expires; the CLI and
  the SPA keep the session and refresh it once the window ends.

## Known gaps

- Presigned S3 download URLs (`nipad-ee`) issued before a `full` window keep
  working until `CHUNK_PRESIGN_TTL_SECONDS` (default 3600) expires. Uploads
  with old URLs land in the bucket but cannot be confirmed.
- `GetMaintenanceStatus` sends the stored access token but never refreshes it:
  with an expired token the CLI, daemon and MCP only see the server window,
  not the org and project windows (a refused request still reports them).
- `nipa status` waits up to 2 s for the maintenance probe when the server is
  unreachable.
- `maintenance --notify`/`end` read `EMAIL_*` from the shell running the
  command plus `config.yaml`/`.env`, not from a color's systemd env file, so
  keep the email settings in `config.yaml` (or export them) for the command.

## Runbooks

### Planned `read_only` or `full` windows

Binary upgrades need no window: the blue/green flip (`docs/DEPLOY.md`) keeps
the service up. Use a window for work that must not see writes (bucket
migration, a data repair, a long reindex) or reads (`full`).

1. Announce ahead (users and the CLI see it as scheduled, emails go out):

   ```sh
   nipad-ee maintenance set read_only --start 2026-11-21T22:00:00Z --end 2026-11-21T23:00:00Z \
     -m "Storage migration" --notify
   ```

   Or narrow it: `--org acme`, `--project acme/rocket`. Org owners can do the
   same from the org or project settings Maintenance tab.
2. At the start, wait one poll plus `SHUTDOWN_TIMEOUT_SECONDS` so requests
   admitted before it finish, then work. Check
   `curl -s http://127.0.0.1:8001/api/maintenance` on each color.
3. Change the estimate if needed (`set` again with a new `--end`; announced
   windows send "updated").
4. `nipad-ee maintenance end` (sends "finished" when announced).

### Moving the postgres database (`nipad-ee`)

The app host stays; the database moves to another VPS. Writes stop for the
few minutes between T0 and the color switch; reads keep working throughout.

**Before (days ahead).**

1. Build the new server with the same postgres major version, TLS and
   `pg_hba.conf` access for the app host and a replication user, then seed a
   streaming replica from the old primary:

   ```sh
   pg_basebackup -h <old-db> -U <replicator> -D <new-data-dir> -R -X stream -C -S nipa_move -P
   ```

   Start it and check on the old primary:
   `SELECT client_addr, state, replay_lsn FROM pg_stat_replication;`
   (`state = streaming`). Managed services that do not allow replication
   connections need their own migration tooling or logical replication.
2. Logical replication instead (major version change or managed services):
   create the schema on the new server (`pg_dump --schema-only`),
   `ALTER TABLE ssh_login_challenges REPLICA IDENTITY FULL;` on the old
   primary (the only table without a primary key; without it its deletes fail
   once published), `CREATE PUBLICATION nipa_move FOR ALL TABLES;` and
   subscribe from the new server. Sequences are not replicated: at step 6,
   once `pg_stat_subscription` shows the subscription caught up, run on the
   new server

   ```sql
   SELECT format('SELECT setval(pg_get_serial_sequence(%L, %L), COALESCE(max(%I), 0) + 1, false) FROM %I.%I;',
                 table_schema || '.' || table_name, column_name, column_name, table_schema, table_name)
   FROM information_schema.columns
   WHERE is_identity = 'YES' AND table_schema = current_schema() \gexec
   ```

   (today `pbac_rules`, `project_paths_permissions`, `refresh_tokens`,
   `tree_nodes`, `chunks`, `files`), then drop the subscription instead of
   promoting.
3. Schedule the window so the announcement goes out before the freeze:

   ```sh
   cd /etc/nipa && sudo -u nipa /opt/nipa/$(cat /var/lib/nipa/active-color)/nipad-ee \
     maintenance set frozen --start 2026-11-22T06:00:00Z --end 2026-11-22T06:30:00Z \
     -m "Moving the database" --notify
   ```

4. Prepare the new DSN and a copy of `/etc/nipa/config.yaml`; keep every key
   the same (see "What must not change").

**T0.**

5. The window activates on every color within one poll. Check
   `curl -s http://127.0.0.1:<port>/api/maintenance` (`"mode":"frozen","active":true`)
   and wait one poll plus `SHUTDOWN_TIMEOUT_SECONDS`. Then guard the old
   primary (superuser):

   ```sql
   ALTER SYSTEM SET default_transaction_read_only = on;
   SELECT pg_reload_conf();
   ```

6. Wait until the replica has replayed everything: `SELECT
   pg_current_wal_lsn();` on the old primary equals `SELECT
   pg_last_wal_replay_lsn();` on the replica (or `pg_wal_lsn_diff(
   pg_current_wal_lsn(), replay_lsn) = 0` in `pg_stat_replication`). Promote
   the new server: `SELECT pg_promote();`, then `SELECT pg_is_in_recovery();`
   is `false`.
7. Point `DATABASE_DSN` in `/etc/nipa/config.yaml` at the new server and flip
   colors with the current binary (same steps as an upgrade, no new release):

   ```sh
   sudo /opt/nipa/upgrade.sh /opt/nipa/$(cat /var/lib/nipa/active-color)/nipad-ee
   ```

   The idle color starts against the new database (migrations are a no-op,
   it leases a node id there; a color started during the window always
   writes this way, which is why it must point at the writable new
   database), passes `/readyz`, takes the traffic, and the old color drains
   and stops. The idle color's previous binary is replaced by the current
   one.

**Check.**

8. The new color reports `frozen` (the window row was copied). Clone and
   browse through the public URL, run `nipad-ee maintenance status` from
   `/etc/nipa`, and check the logs (`journalctl -u nipad-ee@<color>`).

**Point of no return.**

9. `nipad-ee maintenance end` (from `/etc/nipa`, now against the new
   database). Writes resume, the "finished" email is queued. Keep the old
   database (read-only) for a few days, then drop the replication slot
   (`SELECT pg_drop_replication_slot('nipa_move');`) and the old server.

**Rollback (any time before step 9).** Neither database has taken a user
write since T0.

1. On the old primary: `ALTER SYSTEM RESET default_transaction_read_only;
   SELECT pg_reload_conf();` (first: a starting color claims a node lease).
2. Restore the old `DATABASE_DSN` in `/etc/nipa/config.yaml`.
3. `sudo /opt/nipa/upgrade.sh --rollback` (restarts the previous color against
   the old database and flips Caddy back).
4. `nipad-ee maintenance end`, then retire the promoted server.

### Moving the sqlite server (`nipad`)

The whole server moves to a new host: `config.yaml`, `nipa.db` and the chunk
directory (`CHUNK_STORAGE_DIR`). Paths below assume the server runs from
`/srv/nipa`.

**Before.**

1. Install the same `nipad` version on the new host with the same
   `config.yaml` (see "What must not change"). Lower the DNS TTL of the
   public name (for example to 300 s) a day ahead.
2. Copy the chunks as often as you like; they are content-addressed and only
   added, so later passes only send new files:

   ```sh
   rsync -a old:/srv/nipa/chunks/ /srv/nipa/chunks/
   ```

3. Schedule the window: `nipad maintenance set frozen --start <T0> --end
   <T0+30m> -m "Moving the server" --notify` (from `/srv/nipa` on the old
   host).

**T0.**

4. Wait until `curl -s http://127.0.0.1:6745/api/maintenance` reports the
   active frozen window, plus one poll and `SHUTDOWN_TIMEOUT_SECONDS`. On the
   old host take an online copy (backup API, safe with WAL) and check it:

   ```sh
   sqlite3 nipa.db ".backup nipa-move.db"
   sqlite3 nipa-move.db "PRAGMA integrity_check;"
   ```

   (`sqlite3 nipa.db "VACUUM INTO 'nipa-move.db'"` works too.)
5. Final chunk pass (`rsync -a` as above), then copy `nipa-move.db` to the new
   host as `/srv/nipa/nipa.db` (no `-wal`/`-shm` files).
6. Start `nipad` on the new host. It reports `frozen` (`./nipad maintenance
   status`). Point DNS (or the proxy) at it and check reads: clone, browse.

**Point of no return.**

7. `nipad maintenance end` on the new host. Never end the window on the old
   host: clients that still resolve the old address keep getting frozen
   refusals instead of writing to the old copy. Stop the old server once the
   old DNS TTL has passed.

**Rollback (before step 7).** Point DNS or the proxy back, run `nipad
maintenance end` on the old host and stop the new one.

### What must not change

The new host (or the new DSN) must use the same values for:

| key | why |
|---|---|
| `JWT_KEY` | access tokens, refresh session tags and the OAuth state key; changing it signs everyone out |
| `MFA_ENCRYPTION_KEY` | stored TOTP secrets; changing it disables every enrolled second factor |
| `CHUNK_URL_SIGNING_KEY` | chunk URLs and proxied-upload receipts in flight |
| `PUBLIC_BASE_URL`, `EMAIL_BASE_URL` | SSH key login is bound to the public origin; email links |
| `CHUNK_STORAGE*`, `CHUNK_S3_*` | where the chunks live (`nipad-ee` keeps the same bucket) |
| `OAUTH_*`, `PADDLE_*`, `BILLING_*`, `SAAS_*`, `EMAIL_*` | social login callbacks, billing webhooks and prices, onboarding, mail |
| `MAINTENANCE_FILE` | must not exist on the new host unless you mean it |

Moving the app host also moves TLS: the new Caddy obtains its certificate
once the name resolves to it (HTTP-01), so either copy Caddy's data directory
(`/var/lib/caddy/.local/share/caddy`) beforehand or use a DNS challenge. Keep
`PUBLIC_BASE_URL` unchanged, or SSH key logins fail until clients use the new
origin.

## Hard downtime: the Caddy maintenance page

When the process is down (both colors stopped, a failed start, a host move
without a frozen window), there is no server to answer. The sample
`deploy/caddy/Caddyfile` answers for it with `handle_errors 502 503 504`
(Caddy 2.8 or newer):

| caller | answer |
|---|---|
| gRPC (`Content-Type: application/grpc*`) | `grpc-status 14` with the same `ErrorInfo` a `full` server window sends, so the CLI, daemon and MCP print `server is in maintenance (closed): the service is offline; try again in a few minutes`, keep the session and do not retry |
| `/api/*`, `/auth/*` | `503` JSON `{error, maintenance}` like a server `full` window; the SPA shows its maintenance page and keeps the session, and clears it on the next successful poll |
| anything else | `503` with `deploy/caddy/maintenance/index.html` (dark, no external assets) |

Every answer carries `Retry-After: 60`, `Cache-Control: no-store`,
`X-Nipa-Maintenance: server;full` and the usual security headers. A running
server's own 503s (maintenance windows, draining `/readyz`) are regular
upstream responses and pass through unchanged. Netlify marketing failures
also get the page. `internal/maintenance/caddy_test.go` keeps the gRPC
details, message and JSON in sync with the server's refusal.

Install:

```sh
sudo install -d -m 0755 /etc/caddy/maintenance
sudo install -m 0644 deploy/caddy/maintenance/index.html /etc/caddy/maintenance/index.html
sudo install -m 0644 deploy/caddy/Caddyfile /etc/caddy/Caddyfile   # after merging local edits
sudo caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile --envfile /etc/caddy/caddy.env
sudo systemctl reload caddy
```

`NIPA_MAINTENANCE_ROOT` in `/etc/caddy/caddy.env` moves the page folder. A
free `nipad` behind its own Caddy can reuse the `handle_errors` block and the
`security_headers` snippet as they are.
