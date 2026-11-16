# Running nipad in Docker

The free server (`nipad`: SQLite + local chunk storage + the embedded web UI)
is published as a multi-arch image (`linux/amd64`, `linux/arm64`) on every
release tag:

- `ghcr.io/nipalab/nipad`
- `docker.io/nipalab/nipad`

Tags: the exact version (`0.4.1`, `0.5.0-beta.1`), `major.minor` (`0.4`) and
`latest`. Prereleases only get their exact version tag. A manual run of the
"Docker image" workflow rebuilds a tag's exact version only, unless
`move_tags` is checked. The enterprise
`nipad-ee` is not published as an image.

## Quick start

```sh
docker run -d --name nipad -p 6745:6745 \
  -v nipa-data:/data \
  -e JWT_KEY="$(openssl rand -base64 48)" \
  -e CHUNK_URL_SIGNING_KEY="$(openssl rand -base64 48)" \
  ghcr.io/nipalab/nipad:latest
```

Generate the keys once and keep them: changing them signs everyone out and
invalidates outstanding chunk URLs. The container refuses to start without
them. Then sign in as the bootstrap administrator and continue with
[First-time setup](SERVER_GUIDE.md#first-time-setup).

## Image layout

| Path | Content |
| --- | --- |
| `/usr/local/bin/nipad` | the server binary |
| `/app/config.yaml` | `config.yaml.sample` with `DATABASE_DSN=/data/nipa.db`, `CHUNK_STORAGE_DIR=/data/chunks`, `LOG_LEVEL=info`, `LOG_FORMAT=json` |
| `/app/LICENSE` | the Apache-2.0 license |
| `/data` | volume: the SQLite database and the chunk store |

The image is distroless (no shell) and runs as uid/gid `65532`. A bind mount
for `/data` must be writable by that user (`chown 65532:65532 <dir>`).

Every configuration key can be set as an environment variable (they take
precedence over `/app/config.yaml`), see [SERVER_GUIDE.md](SERVER_GUIDE.md#configure).
To use your own file, mount it read-only at `/app/config.yaml`.

Probes: `GET /healthz` (liveness), `GET /readyz` (readiness, 503 while
draining) and `GET /version`. The image has no `HEALTHCHECK` because there is
no shell or curl inside; configure the probes in your orchestrator.

## Compose with a TLS proxy

`nipad` speaks cleartext HTTP/1.1 + h2c and needs a TLS proxy in front for
anything beyond a trusted network (see [DEPLOY.md](DEPLOY.md)). With Caddy:

```yaml
services:
  nipad:
    image: ghcr.io/nipalab/nipad:latest
    restart: unless-stopped
    environment:
      JWT_KEY: ${JWT_KEY}
      CHUNK_URL_SIGNING_KEY: ${CHUNK_URL_SIGNING_KEY}
      MFA_ENCRYPTION_KEY: ${MFA_ENCRYPTION_KEY}
      PUBLIC_BASE_URL: https://nipa.example.com
      RATE_LIMIT_TRUSTED_PROXIES: 172.16.0.0/12
    volumes:
      - nipa-data:/data
  caddy:
    image: caddy:2
    restart: unless-stopped
    ports: ["80:80", "443:443"]
    command: caddy reverse-proxy --from nipa.example.com --to h2c://nipad:6745
    volumes:
      - caddy-data:/data
volumes:
  nipa-data:
  caddy-data:
```

`RATE_LIMIT_TRUSTED_PROXIES` must cover the proxy's address on the Docker
network, otherwise every client shares the proxy's rate-limit bucket.

## Backups and upgrades

Back up the `/data` volume while the container is stopped (or copy the
database with `sqlite3 nipa.db ".backup ..."` from a sidecar). To upgrade,
pull the new tag and recreate the container; pending SQLite migrations are
applied at startup.

## Building locally

```sh
docker build --build-arg VERSION=$(git describe --tags --always) -t nipad .
```

The `Dockerfile` builds the web UI and the server on the build platform and
cross-compiles for the target, so multi-arch builds need no emulation.
`.github/workflows/docker.yml` builds, smoke-tests and publishes the image.
