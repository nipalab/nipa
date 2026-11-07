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
| `/etc/caddy/caddy.env` | Caddy env, `MARKETING_UPSTREAM` (`deploy/caddy/caddy.env.sample`) |
| `/etc/systemd/system/caddy.service.d/nipa.conf` | drop-in loading `caddy.env` (`deploy/caddy/caddy.service.d/nipa.conf`) |
| `/var/lib/nipa/active-color` | which color receives traffic (written by `deploy/upgrade.sh`) |
| `journalctl -u nipad-ee@blue` / `@green` | server logs |

Both colors share `config.yaml`, so `JWT_KEY`, `CHUNK_URL_SIGNING_KEY` and the
database/S3 settings are identical across flips. Sessions and presigned chunk
URLs stay valid across restarts; do not rotate those keys during an upgrade.

## Prerequisites

- A VPS with systemd (Debian/Ubuntu assumed below) and root/sudo.
- DNS: an `A`/`AAAA` record for the public host (e.g. `nipalab.com`) pointing
  at the VPS; the Netlify marketing site stays reachable at its own
  `*.netlify.app` hostname (see below).
- Firewall: allow `22`, `80`, `443` only. The color ports stay on loopback.
- Managed Postgres (15+), reachable with TLS, and a managed S3-compatible
  bucket. Enable bucket versioning and (recommended) object lock/retention for
  chunk data.
- Caddy installed from the official repo (provides the `caddy` binary and the
  `caddy.service` unit).

## First-time install

1. **Build the release.** From a checkout on the build machine:

   ```
   make web build-server-ee      # static (CGO_ENABLED=0) bin/nipad-ee with the embedded SPA
   sha256sum bin/nipad-ee        # ship this checksum with the binary
   ```

### Marketing site (Netlify)

The marketing site is a separate Astro deployment (the `nipa-web` repo on
Netlify). Caddy serves both the app and the marketing site on `nipalab.com`:
the `@marketing` matcher proxies the landing page, `/pricing`, `/download`,
`/support`, `/contact`, `/privacy`, `/terms`, the guides at `/guides`,
`/_astro/*`, `/screenshots/*` and the root assets (`favicon.svg`,
`apple-touch-icon.png`, `logo.svg`, `og.png`, `robots.txt`, `site.webmanifest`,
`sitemap-*.xml`); every other path goes to `nipad-ee`. The product's REST API
docs stay at `/docs`, which is why the marketing guides live at `/guides`.
Because Caddy owns the apex domain, the upstream must be the Netlify site's
own origin (its `*.netlify.app` hostname), never `https://nipalab.com` — that
would proxy Caddy back into itself. Caddy sends the upstream's own host
(`{upstream_hostport}`), so the Netlify site does not need `nipalab.com` as a
domain alias. Marketing links are root-relative, so navigation stays on the
domain; `Sign in` / `Open dashboard` / `/register` link to the app on the same
origin. A marketing change only needs a Netlify deploy — no `nipad-ee`
rebuild or blue/green flip:

- Keep the `@marketing` `handle` block in `deploy/caddy/Caddyfile` ahead of the
  app's `reverse_proxy`. `upgrade.sh` only rewrites the `reverse_proxy
  127.0.0.1:<port>` line, so the marketing upstream is untouched by flips.
- Add every new marketing page route or root asset to the `@marketing` matcher
  and to the reserved org slugs (`internal/usecase/reserved.go` and
  `web/src/slug.ts`), or the app's SPA routes will shadow it. Run
  `npm run build && npm run routes` in `nipa-web` to print the matcher for the
  current build and fail when the Caddyfile misses a route;
  `TestMarketingRoutesAreReservedOrgSlugs` fails when a matcher path is not a
  reserved org slug.
- Before deploying a Caddyfile that adds a path, make sure no organization
  already uses that slug (it would become unreachable), for example
  `SELECT slug FROM organizations WHERE slug IN ('contact','download','guides','privacy','support','terms');`
  and rename any hit.
- `nipa-web` builds flat files (`pricing.html`) with `trailingSlash: 'never'`,
  so Netlify serves pages without redirects. After the first deploy check
  `curl -sI https://nipalab.com/pricing` (200, no `netlify.app` in `Location`),
  `/guides/cli` (marketing) and `/docs` (REST API docs).
- Point it at the Netlify site with `MARKETING_UPSTREAM` (required: the
  Netlify-generated origin, never `https://nipalab.com`) in
  `/etc/caddy/caddy.env` (step 6).
- Build `nipa-web` with the default base `/` (`netlify.toml` carries the build
  command and cache headers); `site` in `astro.config.mjs` sets the canonical
  origin. The `*.netlify.app` hostname stays reachable, but canonical tags point
  to `nipalab.com`.

`make marketing-dist` remains an optional fallback that builds the site with
base `/web/` and embeds a copy into `nipad-ee` (for a SaaS host without the
Caddy proxy); it is not part of the Netlify deployment.

2. **Create the service user and directories.**

   ```
   sudo useradd --system --home /nonexistent --shell /usr/sbin/nologin nipa
   sudo install -d -o root -g root -m 0755 /opt/nipa/blue /opt/nipa/green
   sudo install -d -o root -g nipa -m 0750 /etc/nipa
   sudo install -d -o root -g root -m 0755 /var/lib/nipa
   sudo install -m 0755 bin/nipad-ee /opt/nipa/blue/nipad-ee
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
   | `SAAS_MODE` | `1` (enables registration, social login and the storage ledger; the marketing site is served by Caddy from Netlify) |
   | `SAAS_FREE_QUOTA_BYTES` | free-tier quota per new account |
   | `EMAIL_BASE_URL` | `https://nipalab.com` (required in SaaS mode) |
   | `EMAIL_SENDER` + provider keys | e.g. `sendgrid`/`scaleway`/`smtp`; `off` logs a warning and registration emails are not delivered |
   | `OAUTH_BASE_URL`, `OAUTH_*` | optional, enable Google/GitHub sign-in |
   | `LOG_FORMAT` | `json` |

   `nipad-ee` refuses to start when `JWT_KEY` or `CHUNK_URL_SIGNING_KEY` is
   empty, still the sample value, shorter than 32 characters, or when both
   are the same.

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
   Only the active color is enabled at boot: `upgrade.sh` enables the new color
   and disables the old one on every flip and rollback, so a reboot starts the
   color Caddy points at.

6. **Caddy.**

   ```
   sudo install -m 0644 deploy/caddy/Caddyfile /etc/caddy/Caddyfile
   sudo install -m 0640 -g caddy deploy/caddy/caddy.env.sample /etc/caddy/caddy.env
   sudoedit /etc/caddy/caddy.env           # MARKETING_UPSTREAM=https://<site>.netlify.app
   sudo install -D -m 0644 deploy/caddy/caddy.service.d/nipa.conf \
     /etc/systemd/system/caddy.service.d/nipa.conf
   sudo systemctl daemon-reload
   sudo systemctl enable --now caddy
   sudo systemctl restart caddy            # pick up the drop-in if caddy was running
   echo blue | sudo tee /var/lib/nipa/active-color
   ```

   On a host other than `nipalab.com`, change the site address and the ACME
   `email` in `/etc/caddy/Caddyfile` first.

   The Caddyfile already points at `127.0.0.1:8001` (blue), sets HSTS and the
   basic security headers, logs requests as JSON to the journal, and answers
   404 for `/metrics`, `/readyz` and `/debug/pprof` (probe them on the
   loopback port; `/healthz` and `/version` stay public). Keep the
   `reverse_proxy 127.0.0.1:<port> {` line shape: `upgrade.sh` rewrites it.

   `upgrade.sh` refuses to run while `MARKETING_UPSTREAM` is empty and passes
   `/etc/caddy/caddy.env` to `caddy validate --envfile`, so the check sees
   the same values as the running Caddy (`NIPA_CADDY_ENV` overrides the path).

7. **First admin.** Register the first account through the web UI (SaaS mode),
   confirm the email, then promote it directly in postgres:

   ```sql
   UPDATE users SET is_admin = true, is_super_admin = true
   WHERE email = 'you@example.com';
   ```

8. **Smoke test** with a real client over TLS (requires a client build that
   supports `https://` URLs):

   ```
   nipa login --host https://nipalab.com
   nipa version --server https://nipalab.com/org/project
   curl -fsS https://nipalab.com/healthz
   curl -sI https://nipalab.com/ | grep -i strict-transport-security
   ```

## Pre-launch checklist

- [ ] DNS `A`/`AAAA` for the host point at the VPS; firewall allows only 22/80/443.
- [ ] Postgres reachable with `sslmode=require` (or stricter); connection limit
      sized for `2 × pool + headroom`; automated backups/PITR on.
- [ ] S3 bucket versioning on; credentials scoped to the bucket/prefix.
- [ ] `JWT_KEY` and `CHUNK_URL_SIGNING_KEY` generated with `openssl rand -base64 48`
      (the server refuses weak values); `config.yaml` is `0640 root:nipa`.
- [ ] `SAAS_MODE=1`, `EMAIL_BASE_URL`, `EMAIL_SENDER` + provider keys set, and a
      registration email actually arrives.
- [ ] `/etc/caddy/caddy.env` has the Netlify origin; `curl -sI https://<host>/pricing`
      is 200 and `/docs` is the API docs.
- [ ] First admin registered and promoted.
- [ ] One full `upgrade.sh` run and one `--rollback` done on the host.
- [ ] A reboot after a flip brings back the active color only
      (`systemctl is-enabled nipad-ee@blue nipad-ee@green`).

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
4. Rewrites the `reverse_proxy` upstream, validates the Caddyfile (with
   `/etc/caddy/caddy.env`) and reloads Caddy. Reloads never drop established
   connections; new traffic goes to the new color, which is now enabled at boot.
5. Waits for the old color's connections to drain (`ss`, bounded by
   `NIPA_DRAIN_TIMEOUT`), then `systemctl stop` → SIGTERM → graceful shutdown:
   `/readyz` answers 503 while draining, in-flight requests get the
   `SHUTDOWN_TIMEOUT_SECONDS` budget (shared with the dispatcher and exporter
   drain), then the process exits, and the old color is disabled at boot.
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
