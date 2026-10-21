# Running a Nipa server

This guide shows how to build, configure and run `nipad`, create
the first organization and project, and hand the repository URL to your team.

The server is a single binary with SQLite metadata and local chunk storage.

## What the server provides

`nipad` listens on **one port** (default `6745`) and multiplexes:

| Path | Purpose |
| --- | --- |
| `/` | Web UI (clone browser, merge requests, locks, project/org settings, user admin) |
| `/api/v1/...` | REST API, used by the web UI and automation |
| `/docs/` | Swagger UI for the REST API (`/docs/api.json` is the OpenAPI document) |
| gRPC (h2c) | The protocol the `nipa` CLI and the `nipa serve` daemon use |
| `/healthz` | Liveness probe, always `200` until the process exits |
| `/readyz` | Readiness probe; `503` while the server is shutting down |
| `/version` | Build version as JSON |
| `/metrics` | Prometheus metrics (enabled by default) |

Metadata lives in SQLite; file content is stored as BLAKE3-addressed chunks in
a local directory. There is no external dependency to install.

## Requirements

- Go (the module declares Go 1.27)
- `make`
- Node.js + npm only if you want to build the web UI (`make web`)

## Build

Install the UI dependencies once, then build:

```sh
make web-install      # first time only (npm install in web/)
make build
```

This builds the web UI into `web/server/dist`, then both binaries:

- `bin/nipad` — the server
- `bin/nipa` — the CLI

If you only need the server, run `make web build-server`. The repository tracks
only `web/server/dist/index.html`; the real single-page app assets are produced
by `make web`. Without it, the embedded UI is an empty shell whose asset
references do not resolve. Re-run `make web` whenever the UI changes.

## Configure

`nipad` reads `config.yaml` from its **working directory** and refuses to start
without it. Start from the sample:

```sh
cp config.yaml.sample config.yaml
```

A `.env` file next to `config.yaml` is merged on top. Keys present in
`config.yaml` (and keys with built-in defaults) can be overridden by environment
variables of the same name; the core settings `SERVER_ADDRESS`, `SERVER_PORT`,
`SHUTDOWN_TIMEOUT_SECONDS` and `SNOWFLAKE_NODE_ID` are read from the
environment even when absent from the file. Relative paths (`nipa.db`,
`./chunks`) are resolved against the working directory, so run the server from
the directory that holds its data.

### Minimal configuration

```yaml
SERVER_ADDRESS: 0.0.0.0
SERVER_PORT: 6745
DATABASE_DSN: nipa.db
JWT_KEY: change-me-to-a-long-random-secret
CHUNK_STORAGE: local
CHUNK_STORAGE_DIR: ./chunks
CHUNK_URL_SIGNING_KEY: change-me-to-another-random-secret
```

| Setting | Meaning |
| --- | --- |
| `SERVER_ADDRESS`, `SERVER_PORT` | Listen address. `0.0.0.0` exposes the port on every interface. |
| `DATABASE_DSN` | SQLite database file. It is created on first start. |
| `JWT_KEY` | Signs access tokens. Use a long random value and keep it secret. |
| `CHUNK_URL_SIGNING_KEY` | Signs chunk upload/download URLs. **The server panics at startup when this is empty.** |
| `CHUNK_STORAGE` | Must be `local`; this server stores chunks on disk. |
| `CHUNK_STORAGE_DIR` | Directory for content-addressed chunks. |
| `CHUNK_PRESIGN_TTL_SECONDS` | Lifetime of a signed chunk URL (default `3600`). |

Generate secrets, for example, with `openssl rand -hex 32`.

### Operational settings

| Setting | Default | Meaning |
| --- | --- | --- |
| `SHUTDOWN_TIMEOUT_SECONDS` | `15` | Graceful drain window after `SIGTERM`; `/readyz` answers `503` immediately. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `LOG_FORMAT` | `console` | `console` or `json`. |
| `PPROF_ENABLED` | `false` | Serve `/debug/pprof/` on the main port; keep off outside trusted networks. |
| `METRICS_ENABLED` | `true` | Serve Prometheus metrics on `/metrics`. |
| `HASHER_WORKERS` | `4` in the sample (`0` → `1`) | Password hashing concurrency. |
| `SNOWFLAKE_NODE_ID` | `0` | Record id node. The server is single-instance; leave `0`. |

### Optional services

| Topic | Settings | Details |
| --- | --- | --- |
| Email notifications | `EMAIL_SENDER` (`off` by default), `EMAIL_FROM`, `EMAIL_BASE_URL`, ... | [EMAIL.md](EMAIL.md) |
| Webhooks | `WEBHOOK_EGRESS_ALLOWLIST`, `WEBHOOK_TIMEOUT_SECONDS`; endpoints are configured per project in the UI | Restrict egress to private/loopback receivers with the allowlist. |
| Tracing | `TRACING_ENABLED`, `TRACING_OTLP_*` | [TRACING.md](TRACING.md) |
| Logs/metrics export over OTLP | `LOGS_OTLP_ENABLED`, `METRICS_OTLP_ENABLED`, ... | [OBSERVABILITY.md](OBSERVABILITY.md) |
| SSH key login | No server setting; manage keys from the profile page | [SSH.md](SSH.md) |

## Start the server

```sh
./bin/nipad
```

The first start applies the embedded SQLite migrations, logs the progress, and
begins listening. Verify from another terminal:

```sh
curl -s http://localhost:6745/version
curl -s -o /dev/null -w '%{http_code}\n' http://localhost:6745/readyz   # 200
```

Then open `http://localhost:6745/` for the web UI.

Stop with `Ctrl+C` or `SIGTERM`; in-flight requests get
`SHUTDOWN_TIMEOUT_SECONDS` to finish.

## First-time setup

### 1. Sign in as the built-in administrator

A fresh database seeds one super admin account:

| Field | Value |
| --- | --- |
| Email | `nipa` |
| Password | `nipa` |

Open the web UI and sign in with those values, or use the REST API:

```sh
# requires jq; or copy access_token from the JSON response
TOKEN=$(curl -s http://localhost:6745/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"nipa","password":"nipa"}' | jq -r .access_token)
```

> Change this password immediately. It is a fixed bootstrap credential.

The web UI profile page changes it. With `curl`:

```sh
curl -s -X POST http://localhost:6745/api/v1/me/password \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"old_password":"nipa","new_password":"a-long-new-password"}'
```

New passwords must be at least 8 characters.

### 2. Create user accounts

User creation is a global-admin operation; there is no self-service
registration. Use the web UI under the admin user list, or:

```sh
curl -s -X POST http://localhost:6745/api/v1/users \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Alice","email":"alice@example.com","password":"alice-password"}'
```

The email must look like an address (`name@example.com`). Admin flags are set
separately with `PATCH /api/v1/users/{id}/admin`.

### 3. Create an organization and a project

Any authenticated user can create an organization and becomes its owner.
Project creation requires organization ownership, so the creator can create
projects right away.

```sh
# organization: {name, slug}
curl -s -X POST http://localhost:6745/api/v1/orgs \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Acme Games","slug":"acme"}'

# project inside the organization
curl -s -X POST http://localhost:6745/api/v1/orgs/acme/projects \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Rocket","slug":"rocket"}'
```

The web UI exposes the same flow: **New organization** on the home page, then
**New project** on the organization page. Organization owners can add members
and manage groups/permissions from the organization page.

### 4. Share the repository URL

The clone URL is:

```
http://<host>:6745/<org-slug>/<project-slug>
```

For example `http://localhost:6745/acme/rocket`. Give your team the server URL
and those slugs (or a user account); the client guide continues from there:

```sh
nipa clone http://localhost:6745/acme/rocket ./rocket
```

## Data, backups and upgrades

All state is under the server's working directory:

```
.
├── config.yaml          # settings and secrets
├── nipa.db              # SQLite metadata
├── nipa.db-wal          # while running
├── nipa.db-shm          # while running
└── chunks/              # content-addressed file chunks
```

To back up, stop the server (or use SQLite's online backup), then copy
`nipa.db` and `chunks/` together. Restoring only one of them leaves the
metadata pointing at missing content.

To upgrade, replace `bin/nipad` and restart. Pending SQLite migrations are
applied automatically at startup; there is no separate migration step. Keep a
backup before upgrading.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `panic: Config File "config" Not Found` | No `config.yaml` in the working directory. Run `cp config.yaml.sample config.yaml`. |
| `panic: CHUNK_URL_SIGNING_KEY must be set` | Set a non-empty signing key in `config.yaml`. |
| `bind: address already in use` | Another process (often a second server) uses the port. Stop it or change `SERVER_PORT`. |
| Web UI loads a blank page or an old build | `web/server/dist` holds only the tracked placeholder. Run `make web` and rebuild/restart `nipad`. |
| `database is locked (5) (SQLITE_BUSY)` | Only one `nipad` may write the same SQLite file. Stop the duplicate instance and check file permissions/disk. |
| `/readyz` returns `503` | The process received `SIGTERM` and is draining, or the database ping fails. Check the logs. |
| Login as the seeded admin fails | The email is the literal string `nipa`, not an email address. |

## Where to go next

- Client commands and workflows: [CLIENT_GUIDE.md](CLIENT_GUIDE.md)
- Access control (path rules, groups): [PBAC.md](PBAC.md)
- Mandatory binary locks: [LOCKING.md](LOCKING.md)
- Web UI routes and REST surface: [WEB_UI.md](WEB_UI.md)
- SSH key login: [SSH.md](SSH.md)
- Email notifications: [EMAIL.md](EMAIL.md)
- Metrics, logs and traces: [OBSERVABILITY.md](OBSERVABILITY.md), [TRACING.md](TRACING.md)
