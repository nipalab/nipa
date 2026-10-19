# Deploying the SaaS on a VPS (no Docker)

Runbook for a single-VPS production deployment of the enterprise server
(`nipad-ee`) with **zero-downtime restarts and upgrades**: two color instances
(blue/green) behind Caddy, which terminates TLS for the single h2c server port
and proxies to the active color. Postgres and the S3 chunk store are managed
services; the VPS only runs Caddy and `nipad-ee`.

```
clients ──TLS──▶ Caddy :80/:443 ──h2c──▶ nipad-ee@<active> 127.0.0.1:8001|8002
                    (Let's Encrypt)              │
                                                 ├──▶ managed postgres
                                                 └──▶ managed S3 bucket
```

An upgrade starts the idle color, waits for `/readyz`, flips the Caddy upstream
and drains the previous color. New connections never see a refused port, and
in-flight operations keep running until the server's drain window ends.

## Host layout

| Path | Purpose |
|---|---|
| `/opt/nipa/blue/nipad-ee`, `/opt/nipa/green/nipad-ee` | per-color binaries (keep the previous one for rollback) |
| `/etc/nipa/config.yaml` | shared settings (database, S3, secrets, SaaS flags) |
| `/etc/nipa/.env` | optional shared env overrides (same keys as `config.yaml`) |
| `/etc/nipa/blue.env`, `/etc/nipa/green.env` | per-color `SERVER_ADDRESS`/`SERVER_PORT`/`SHUTDOWN_TIMEOUT_SECONDS` (templates in `deploy/nipad-ee.env.sample`) |
| `/etc/systemd/system/nipad-ee@.service` | template unit (`deploy/nipad-ee@.service`) |
| `/etc/caddy/Caddyfile` | TLS edge (`deploy/caddy/Caddyfile`) |
| `/var/lib/nipa/active-color` | which color receives traffic (written by `deploy/upgrade.sh`) |
| `journalctl -u nipad-ee@blue` / `@green` | server logs |

Both colors share `config.yaml`, so `JWT_KEY`, `CHUNK_URL_SIGNING_KEY` and the
database/S3 settings are identical across flips. Sessions and presigned chunk
URLs stay valid across restarts; do not rotate those keys during an upgrade.

## Prerequisites

- A VPS with systemd (Debian/Ubuntu assumed below) and root/sudo.
- DNS: an `A`/`AAAA` record for the public host (e.g. `nipa.example.com`)
  pointing at the VPS.
- Firewall: allow `22`, `80`, `443` only. The color ports stay on loopback.
- Managed Postgres (15+), reachable with TLS, and a managed S3-compatible
  bucket. Enable bucket versioning and (recommended) object lock/retention for
  chunk data.
- Caddy installed from the official repo (provides the `caddy` binary and the
  `caddy.service` unit).

## First-time install

1. **Build the release.** From a checkout on the build machine:

   ```
   make web build-server-ee      # produces bin/nipad-ee with embedded SPA
   sha256sum bin/nipad-ee        # ship this checksum with the binary
   ```

2. **Create the service user and directories.**

   ```
   sudo useradd --system --home /nonexistent --shell /usr/sbin/nologin nipa
   sudo install -d -o root -g root -m 0755 /opt/nipa/blue /opt/nipa/green
   sudo install -d -o root -g nipa -m 0750 /etc/nipa
   sudo install -d -o root -g root -m 0755 /var/lib/nipa
   sudo install -m 0555 bin/nipad-ee /opt/nipa/blue/nipad-ee
   ```

3. **Write `/etc/nipa/config.yaml`** (start from `config.yaml.sample`). The
   must-change list for a public SaaS:

   | Key | Value |
   |---|---|
   | `DATABASE_DSN` | `postgres://user:pass@host:5432/nipa?sslmode=require` |
   | `JWT_KEY` | long random secret (`openssl rand -base64 48`) |
   | `CHUNK_URL_SIGNING_KEY` | different long random secret |
   | `CHUNK_STORAGE` | `s3` (or omit; enterprise defaults to S3) |
   | `CHUNK_S3_ENDPOINT`, `CHUNK_S3_REGION`, `CHUNK_S3_BUCKET`, `CHUNK_S3_PREFIX` | managed bucket |
   | `CHUNK_S3_ACCESS_KEY_ID` / `CHUNK_S3_SECRET_ACCESS_KEY` | leave empty to use the AWS env/instance providers |
   | `SAAS_MODE` | `1` (enables registration, social login and the storage ledger) |
   | `SAAS_FREE_QUOTA_BYTES` | free-tier quota per new account |
   | `EMAIL_BASE_URL` | `https://nipa.example.com` (required in SaaS mode) |
   | `EMAIL_SENDER` + provider keys | e.g. `sendgrid`/`scaleway`/`smtp`; `off` logs a warning and registration emails are not delivered |
   | `OAUTH_BASE_URL`, `OAUTH_*` | optional, enable Google/GitHub sign-in |
   | `LOG_FORMAT` | `json` |

   Keep the file `0640 root:nipa`. Do **not** set `SNOWFLAKE_NODE_ID`: the
   colors run concurrently during a flip and lease distinct node ids from
   postgres (a fixed id would let two instances mint colliding ids).

4. **Per-color env files.**

   ```
   sudo cp deploy/nipad-ee.env.sample /etc/nipa/blue.env
   sudo cp deploy/nipad-ee.env.sample /etc/nipa/green.env
   sudo sed -i 's/^SERVER_PORT=.*/SERVER_PORT=8002/' /etc/nipa/green.env
   ```

   `SHUTDOWN_TIMEOUT_SECONDS` bounds the whole graceful shutdown (in-flight
   pushes/merges plus the dispatcher and exporter drain); set it above your
   longest expected operation (60 is a good start) and keep the unit's
   `TimeoutStopSec=180` above it.

5. **Install the unit and start the blue color.**

   ```
   sudo install -m 0644 deploy/nipad-ee@.service /etc/systemd/system/nipad-ee@.service
   sudo systemctl daemon-reload
   sudo systemctl enable nipad-ee@blue
   sudo systemctl start nipad-ee@blue     # migrates the schema, then serves
   curl -fsS http://127.0.0.1:8001/readyz
   ```

   `nipad-ee` applies pending postgres migrations at startup before listening;
   the unit's `ExecStartPost` fails the start if `/readyz` never turns green.

6. **Caddy.**

   ```
   sudo install -m 0644 deploy/caddy/Caddyfile /etc/caddy/Caddyfile
   sudo sed -i \
     -e 's/admin@nipalab\.com/you@example.com/' \
     -e 's/^nipalab\.com {/your.host.example {/' \
     /etc/caddy/Caddyfile
   sudo systemctl enable --now caddy
   echo blue | sudo tee /var/lib/nipa/active-color
   ```

   The Caddyfile already points at `127.0.0.1:8001` (blue) and answers 404 for
   `/metrics` and `/debug/pprof`; remove that matcher (or restrict it to a
   private scraper) if you intentionally expose them. Keep the
   `reverse_proxy 127.0.0.1:<port> {` line shape: `upgrade.sh` rewrites it.

7. **First admin.** Register the first account through the web UI (SaaS mode),
   confirm the email, then promote it directly in postgres:

   ```sql
   UPDATE users SET is_admin = true, is_super_admin = true
   WHERE email = 'you@example.com';
   ```

8. **Smoke test** with a real client over TLS (requires a client build that
   supports `https://` URLs):

   ```
   nipa login --host https://nipa.example.com
   nipa version --server https://nipa.example.com/org/project
   curl -fsS https://nipa.example.com/healthz
   ```

## Upgrades (zero downtime)

```
deploy/upgrade.sh /path/to/nipad-ee <sha256>   # normal upgrade
deploy/upgrade.sh --rollback                   # flip back to the previous color
```

What the script does:

1. Reads the active color (state file, falling back to the Caddyfile upstream).
2. Verifies the checksum, installs the binary into the **idle** color
   (`install` + atomic rename).
3. Starts the idle color and waits for `/readyz` (postgres ping) plus a
   `/version` smoke check.
4. Rewrites the `reverse_proxy` upstream, validates the Caddyfile and reloads
   Caddy. Reloads never drop established connections; new traffic goes to the
   new color.
5. Waits for the old color's connections to drain (`ss`, bounded by
   `NIPA_DRAIN_TIMEOUT`), then `systemctl stop` → SIGTERM → graceful shutdown:
   `/readyz` answers 503 while draining, in-flight requests get the
   `SHUTDOWN_TIMEOUT_SECONDS` budget (shared with the dispatcher and exporter
   drain), then the process exits.
6. Writes the new active color.

Failure behavior: every failure before the Caddy reload stops the idle color,
restores the Caddyfile and leaves production untouched. The previous binary
stays on disk, so `--rollback` re-starts the old color and flips back. Roll
back **before** the next migration if the release changed the schema (see
below).

### In-flight behavior

- gRPC/REST calls on established connections finish during the drain window;
  long pushes are safe if `SHUTDOWN_TIMEOUT_SECONDS` covers them. Operations
  still running when the window expires are cut off (the server logs
  `server shutdown incomplete`), so keep the budget above your slowest
  operation.
- Chunk transfers in the enterprise server go directly to S3 via presigned
  URLs, so they do not touch the server or Caddy and survive a flip. Keep
  `CHUNK_URL_SIGNING_KEY` stable across restarts; presigned URLs issued before
  a flip remain valid until `CHUNK_PRESIGN_TTL_SECONDS` expires.
- The SPA keeps working: it holds no server-side session state (JWT + HttpOnly
  refresh cookie only).
- `nipa serve` daemons and MCP sessions reconnect to Caddy on the next call;
  an operation that errors mid-flip can simply be retried (pushes are
  idempotent).
- During the overlap both colors run webhook dispatchers; a webhook event can
  be delivered twice in the worst case. Consumers should treat deliveries as
  idempotent (the delivery id/event payload repeats). Email notifications are
  claimed atomically in postgres and are not affected.

### Schema migrations

Both colors apply pending migrations at startup, and the old color keeps
serving while the idle one migrates. Keep migrations **forward compatible** for
one release (add columns with defaults, backfill, then drop in a later
release). A rollback across a migration that dropped or renamed something may
be incompatible; when in doubt, upgrade forward and roll the code back only to
releases that share the current schema.

## Day-2 operations

- **Logs:** `journalctl -u nipad-ee@blue -f` (JSON with `LOG_FORMAT=json`).
- **Health/metrics:** `/healthz` (liveness), `/readyz` (postgres readiness;
  503 while draining), `/version` (unauthenticated build info), `/metrics`
  (Prometheus, `METRICS_ENABLED=true` by default). Keep these on the loopback
  ports for probes; the sample Caddyfile answers 404 for `/metrics` and
  `/debug/pprof` on the public host. To scrape metrics externally, add a
  matcher restricted to the scraper (or a basic-auth protected route) instead
  of removing that block outright.
- **Backups:** managed-postgres automated backups/PITR; S3 bucket versioning
  plus lifecycle rules. The VPS itself holds no state worth backing up
  (config + binaries only).
- **Capacity:** two color instances can be live briefly during a flip, so size
  the postgres connection limit for `2 × pool + headroom` (the pool is
  currently untuned; see `docs/PERFORMANCE.md`).
- **Key rotation:** rotate `JWT_KEY` to invalidate all sessions, and
  `CHUNK_URL_SIGNING_KEY` to invalidate outstanding chunk URLs. Do this as a
  normal two-color rolling change (both colors read the same config file), not
  mid-flip, and expect existing presigned transfers to fail once.

## Growing beyond one VPS

The same unit and script scale out later: put a load balancer (or Caddy on a
separate host) in front of several VPSes, each running one or two colors, and
run `upgrade.sh` per VPS. `nipad-ee` already leases its snowflake node id from
postgres, so any number of instances can run concurrently.
