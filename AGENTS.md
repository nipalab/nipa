# AGENTS.md

Project context for coding agents. Read this before working in this repo. It is
kept short on purpose: detailed flows live in `docs/internals/` (index below)
and the design docs in `docs/`. Read the relevant file before changing an area.

## What this project is

**Nipa** is a centralized version control system (Go/gRPC) for binary-heavy
projects (game dev, 3D assets, media) and large monorepos, blending Git
(lightweight branches, release tags, merge requests), Perforce (binary
streaming, file locks) and SVN (path-based access control). Client-server
monorepo: `nipa` (client CLI, `cmd/nipa`) plus two servers: the free `nipad`
(`cmd/nipad`; SQLite + local chunk storage) and the enterprise `nipad-ee`
(`ee/cmd/nipad`; PostgreSQL + S3-only chunk storage, `docs/POSTGRES.md`). Both
multiplex gRPC (h2c), the REST API and the embedded SPA on one port through
`internal/serverapp` (`isAPIPath` routes `/auth`, `/docs`, `/api`; everything
else falls through to the web UI handler). The mains only compose repositories
and call `serverapp.BuildUsecases` (`usecase.Repositories` + `UsecaseDeps`),
the single place that wires the usecase graph, also used by the
`internal/e2e` harness. Dual-licensed (Apache 2.0; everything under `ee/` is
enterprise).

## Architecture

- **Server** (`internal/`): `domain` (entities/errors), `usecase` (business
  logic, gomock-tested, depends only on repository interfaces; `browser.go` /
  `browser_history.go` power the web UI tree/commit browsing,
  `merge_request.go` the MR lifecycle, `merge_request_review.go` reviews and
  `tag.go` release tags), `repository/sqlite` (free implementation over
  sqlc-generated `internal/repository/sqlc/sqlite`), `serverapp` (shared
  registry + HTTP/gRPC/SPA lifecycle used by both mains), `grpc` (proto + `pb`
  generated + `server` handlers), `http` (REST API).
- **Enterprise** (`ee/`): `db` (postgres opener/migrator + consolidated
  migrations), `repository/postgres` (the same 13 repositories over
  `ee/repository/postgres/sqlc`, queries in `ee/repository/postgres/queries`),
  `storage/s3`, `cmd/nipad`/`cmd/migrate`, `e2e` (testcontainers smoke). All
  postgres and S3 code lives here; details in `docs/POSTGRES.md`.
- **Web UI** (`web/`): Vite/React/TypeScript SPA on Primer React v38
  (`Stack`/`Banner`; no `Box`/`sx`), built by `make web` into
  `web/server/dist` and embedded via `web/server/webui.go`. See
  `docs/internals/web.md` and `docs/WEB_UI.md`.
- **Client** (`internal/client/`): `cli` (cobra), `usecase`, `grpc`
  (transport), `localrepo` (`.nipa/` metadata + SQLite + object cache),
  `daemon` (`nipa serve`), `mcp` (`nipa mcp`), `ignore`, `config`,
  `securestorage`, `assetscan`, `aiprompt`/`aicontext`/`llm`/`generate`,
  `gitimport`.
- **Pure shared packages** (no client imports, the server reuses them):
  `internal/diff`, `internal/merge`, `internal/preview`, `internal/assetdiff`,
  `internal/assetbudget`, `internal/assetrefs`, `internal/provenance`,
  `internal/unitymeta`, `internal/godotmeta`, `internal/godotres`,
  `internal/pathmatch`, `internal/gitimport`, `internal/maintenance`,
  `internal/totp`, `internal/sealbox`.
- **Desktop app** (`desktop/`, nested module `github.com/nipalab/nipa/desktop`,
  Wails v3 + React/Vite/Tailwind over the `nipa serve` daemon). Root `./...`
  never includes it; use `make desktop`/`desktop-dev`/`desktop-test`. Generated
  TS bindings in `desktop/frontend/bindings` are committed. New UI text must
  land in every `frontend/src/i18n/locales/*.json` catalog.
- **Plugins** (Apache 2.0, all over the daemon JSON bridge):
  Unity `plugins/unity` (`make unity-test`; Core stays free of
  `UnityEngine`/`UnityEditor` and C# 9 compatible), Godot `plugins/godot`
  (C++17 core + GDExtension, `make godot-test`/`godot-build`), VS Code
  `plugins/vscode` (`make vscode-test`; never call `UnwatchRepo`).

## Where the details are

| Area | Internals (agent notes) | Design docs |
|---|---|---|
| Clone, sparse, ignore, revert, restore, diff, shelve, client packages | `docs/internals/client.md` | `docs/CLIENT_GUIDE.md`, `docs/BRANCH_MERGING.md` |
| `.nipa/` local repo, object cache, GC, stat cache | `docs/internals/localrepo.md` | |
| Proto/gRPC surface, push/manifest streams, chunk transfer, storage backends | `docs/internals/protocol.md` | `docs/PUSH_OPTIMIZATION.md`, `docs/POSTGRES.md` |
| `nipa serve` daemon and JSON bridge | `docs/internals/daemon.md` | `docs/DAEMON.md` |
| Desktop app | `docs/internals/desktop.md` | `docs/DESKTOP.md` |
| Unity, Godot, VS Code plugins | `docs/internals/plugins.md` | `docs/UNITY_PLUGIN.md`, `docs/GODOT_PLUGIN.md`, `docs/VSCODE_EXTENSION.md` |
| Asset previews/diffs, budgets, provenance, references | `docs/internals/assets.md` | `docs/AI.md` |
| `nipa mcp`, LLM generation | `docs/internals/ai.md` | `docs/AI.md` |
| `nipa import git` | `docs/internals/git-import.md` | `docs/GIT_IMPORT.md` |
| Tags, service tokens, issue tracker integration, org creation, file locks, merge requests/reviews/strategies | `docs/internals/server.md` | `docs/LOCKING.md`, `docs/BRANCH_MERGING.md`, `docs/WEBHOOKS.md`, `docs/PBAC.md`, `docs/ISSUE_TRACKER.md` |
| Sessions, refresh tokens, SSH login, MFA | `docs/internals/auth.md` | `docs/SSH.md`, `docs/MFA.md` |
| Email, SaaS onboarding, storage quota, BYO storage, billing | `docs/internals/saas.md` | `docs/EMAIL.md`, `docs/SAAS_ONBOARDING.md`, `docs/CLOUD_STORAGE_QUOTA.md`, `docs/BYO_STORAGE.md`, `docs/BILLING.md` |
| Rate limit, audit log, maintenance mode, tracing, observability, benchmarks, Docker, deployment | `docs/internals/operations.md` | `docs/RATE_LIMIT.md`, `docs/AUDIT_LOG.md`, `docs/MAINTENANCE.md`, `docs/TRACING.md`, `docs/OBSERVABILITY.md`, `docs/PERFORMANCE.md`, `docs/DOCKER.md`, `docs/DEPLOY.md` |
| Web UI (routes, billing, maintenance pages) | `docs/internals/web.md` | `docs/WEB_UI.md` |

When you add or change a feature, update the matching `docs/internals/` file
(or the design doc), not this file. Add here only a rule every task must know.

## Commands

From `Makefile`:

- `make godot-test` — CMake + doctest for the Godot plugin `core/` with the gcovr
  80% line gate; `make godot-build` builds the GDExtension (fetches godot-cpp 4.4).

- `make sqlc` — regenerate all sqlc packages: sqlite +
  `internal/repository/sqlc/localrepo` for the free client/server, and postgres
  in `ee/repository/postgres/sqlc` (inputs: `ee/db/migrations/postgres`,
  `ee/repository/postgres/queries`). Uses
  `go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest`.
- `make mock` — `go generate ./...` (regenerates gomock mocks from `//go:generate`).
- `make migrate-up` / `make migrate-down` — sqlite only, `go run ./cmd/migrate`
  (default `DSN=nipa.db`). `nipad` also applies pending sqlite migrations at
  startup. Postgres equivalents: `make migrate-up-postgres DSN=postgres://...`
  / `make migrate-down-postgres` via `go run ./ee/cmd/migrate`; `nipad-ee`
  applies pending postgres migrations at startup.
- `make migrate-create name=<name>` — new up/down migration pair in both
  dialect dirs: `db/migrations/sqlite` and `ee/db/migrations/postgres` (both
  embedded via `go:embed` and read by `sqlc.yaml`).
- `make build` — web + `bin/nipad` + `bin/nipa`; `make build-server-ee` —
  `bin/nipad-ee`; `make build-all` — web + both servers + client. Builds inject
  `internal/version` via `-ldflags` from
  `VERSION ?= git describe --tags --always --dirty` / `REVISION`; plain
  `go build` falls back to `debug.ReadBuildInfo` (`dev-<sha>` or the VCS
  pseudo-version). `make release version=vX.Y.Z` tags a clean HEAD (annotated;
  push manually; full release: binaries, desktop, production deploy) and
  `make release-server version=vX.Y.Z` tags `server-vX.Y.Z` (production
  `nipad-ee` deploy only, no GitHub release; the deploy build reports the
  version without the prefix). Both refuse a version taken by either prefix;
  `VERSION` and the desktop `GIT_VERSION` describe `v*` tags only. `nipa version [--server] [url]` reports the client build and
  probes the server through the public `GetServerVersion` RPC; both servers
  also answer unauthenticated `GET /version` (JSON) and `-version`, and log the
  version at startup. `daemon.Version` defaults to the build version (Ping /
  `daemon.json` / `nipa mcp`).
- `make test` — `go test ./...` (requires Docker for the testcontainers-backed
  postgres/minio suites); `make test-ee` — `go test ./ee/...`.
- `make test-client` / `test-client-ee` / `test-client-both` — the Ginkgo
  black-box client e2e suite (`tests/run.sh [free|ee|both]`): builds `bin/nipa`,
  starts a real `nipad`/`nipad-ee` (ee starts postgres + minio from
  `tests/docker-compose.ee.yml`) and drives the compiled CLI against it, so both
  editions are verified to behave the same client-side. Covers every command and
  flag (clone/push/update, branch, switch, merge, revert, log, diff,
  sparse-checkout, tag, lock, mr, acl, group) plus `nipa serve` (daemon gRPC),
  `nipa mcp` (stdio JSON-RPC) and webhook deliveries (events, filters,
  signatures, ping/redelivery), with multi-user identities for permission
  (incl. org owners' implicit project access vs. plain members' deny-by-default)
  and lock scenarios. `NIPA_TOKEN_FILE` makes `securestorage` file-backed so the
  CLI runs without an OS keyring/TTY; the suite skips unless `NIPA_TEST_HOST` is
  set. Details in `tests/README.md`.
- `make web` / `web-dev` / `web-install` — build the SPA into
  `web/server/dist` / run the Vite dev server (proxies `/api`, `/docs` to
  `NIPA_SERVER_URL`) / `npm install`. `make marketing-dist
  [MARKETING_DIR=../nipa-web]` builds the Astro marketing site with
  `MARKETING_BASE=/web/` and syncs it into `ee/web/marketing/dist` for the
  embedded fallback; production marketing is Netlify (base `/`) behind the
  Caddy proxy and needs no rebuild.
- `make proto` — regenerates pb from `internal/grpc/proto/server.proto` into
  `internal/grpc/pb` and from `internal/client/grpc/proto/daemon.proto` into
  `internal/client/grpc/daemonpb` (the daemon proto imports the server one;
  the target `M`-maps that import onto `internal/grpc/pb`).
- `make lint` — golangci-lint via Docker image (`.golangci.yml` enables
  errcheck/govet/ineffassign/staticcheck/unused/misspell/unconvert; formatters
  gofmt/goimports).

Direct: `go build ./...`, `go vet ./...`, `go test ./...`.

- The server opens SQLite through `db.OpenSQLite`, which appends
  `_pragma=busy_timeout(10000)`, `_pragma=journal_mode(WAL)` and
  `_txlock=immediate` to the DSN. Without WAL + a busy timeout, concurrent
  reads (every RPC resolves the project first) collide with confirm/Push
  writes and surface as `database is locked (5) (SQLITE_BUSY)`; without
  immediate transactions, a deferred transaction that reads before writing
  (e.g. `ApplyPushAll` resolving chunk rows) fails upgrading with
  `SQLITE_BUSY_SNAPSHOT` when another connection commits in between, and the
  busy handler is not invoked for that case.

## Conventions

- **Package layout**: consider packages under `internal/` as the border; use the
  existing import ordering (stdlib, third-party, then `github.com/nipalab/nipa/*`).
- **IDs**: server-side records use `github.com/nipalab/nipa/internal/snow`
  (`snow.ID`, `.Base36()` for wire, `.Int64()`/`snow.ParseBase36` for DB/proto).
  The 8-bit node id comes from `SNOWFLAKE_NODE_ID`; when unset `nipad-ee` leases
  a unique one per instance from `snowflake_node_leases` (`ee/nodelease`,
  renewed until shutdown, exit on lost lease) so multi-pod never collides,
  while the free `nipad` stays single-instance with the static default 0.
  Client localrepo tables do NOT use snow IDs.
- **Server config**: `internal/config.LoadConfig` reads `config.yaml`, merges
  `.env`, and binds every `Config` `mapstructure` key to the environment
  (`envKeys`), so a new field is env-settable without a manual `BindEnv`
  (precedence env > `.env` > `config.yaml` > `SetDefault`).
- **Errors**: `internal/domain.Error` with HTTP-style `Code` + `Message` +
  `InternalMessage` + `Cause`. Constructions: `NewErrorNotFound(msg)`,
  `NewErrorNoPermission()`, `NewErrorUser(msg)`, `NewErrorDatabase(internalMsg)`,
  `NewErrorInternalServer(...)`. Predicates: `domain.IsErrorNotFound(err)`,
  `domain.IsErrorNoPermission(err)`. Client mirrors this with
  `internal/client/domain.Error` (`NewUserError` 400, `NewTokenError` 401).
- **SQL**: schema + queries are sqlc inputs (`db/migrations/sqlite` +
  `db/queries/sqlite` for the free server, `ee/db/migrations/postgres` +
  `ee/repository/postgres/queries` for enterprise, and
  `internal/client/localrepo/schema|queries` for the client). After changing a
  `.sql` file, run `make sqlc` and check the generated Go compiles. Named params
  (`:name`) are converted to `?N` in `database/sql`.
- **sqlc gotchas**: a multi-statement `:exec` query has its SQL truncated by sqlc
  to one statement — use separate single-statement queries instead. Full-table
  `DELETE` without `WHERE` trips SonarQube (S1035-ish); keep them out. On
  sqlite, mixing unnamed `?` placeholders with `sqlc.narg(...)` makes sqlc emit
  a broken `?N` mixture that misbinds args at runtime — name every parameter
  with `sqlc.arg(...)` once a query uses `narg`. On
  postgres, quote keyword arg names (`sqlc.arg('limit')`), cast nullable filters
  and `LIMIT`/`OFFSET` params (`sqlc.narg('x')::boolean`,
  `sqlc.arg('limit')::bigint`), and keep integer columns `BIGINT` so generated
  types stay `int64` like sqlite.
- **Testing**: testify (`require`/`assert`) + gomock (`go.uber.org/mock`,
  `github.com/golang/mock` legacy mocks exist in `*_mock_test.go`). Server usecase
  tests use `NewMock<h1>...` gomock controllers; client usecases use hand-written
  stubs. SQLite-backed tests use `t.TempDir()` for real DBs. Enterprise postgres
  tests are testify suites over testcontainers (postgres + minio, one schema per
  test); override with `NIPA_TEST_POSTGRES_DSN`/`NIPA_TEST_S3_ENDPOINT`/
  `NIPA_TEST_S3_IMAGE` when using service containers.
- **Mocks**: live next to the code with the name suffix `_mock_test.go`; regenerate
  with `make mock`. In `internal/grpc/server`, `*_deps_mock_test.go` /
  `*_usecase_mock_test.go` are the mocks.
- **gofmt/golangci**: new/edited files must be gofmt-clean. One legacy file was
  left unformatted on purpose — do not reformat it unless the whole file is
  already clean: `internal/client/domain/url_test.go`.

## Traps that break things silently

- Web UI: pin `react-is` to React's major (18); react-is 19 breaks every
  Primer visual (black screen). `Banner` requires a `title` prop, children-only
  usage unmounts the app. Theme is dark-only (`index.html`
  `data-color-mode="dark"` + inline `html` background, `main.tsx`
  `colorMode="dark"`, only `dark.css` imported).
- Keep `web/src/api/endpoints.ts` in sync with the REST handlers under
  `internal/http/`; `npm run lint`, `npm test` and `make web` must stay green.
- Client localrepo opens SQLite with `_pragma=foreign_keys(ON)` (cascades
  silently don't fire otherwise) and has no schema migration: old clones are
  re-cloned. Its tables do not use snow IDs.
- Every client hashing path goes through `chunker.Encode`/`ConfigForFile`
  with the tracked encoding (`storedEncoding`), or file hashes diverge.
- Chunk transfer is presigned HTTP, not gRPC streaming; the `/api/chunks`
  routes are not behind the bearer filter (the signature is the capability).
- Every new REST route needs a maintenance class
  (`testdata/maintenance_routes.golden`, `NIPA_UPDATE_GOLDEN=1`) and every new
  gRPC method an entry in `maintenance_classes.go`.
- Repositories touched by audited writes must be `dbtx`-aware (no private
  `BeginTx`).

## SonarQube / CI

- CI runs `go build ./...`, `go vet ./...`, `go test -race -coverprofile=...
  -coverpkg=./...` (cross-package coverage, so the REST/e2e integration tests
  count towards the handler/usecase packages they exercise).
- `sonar-project.properties` excludes generated code
  (`internal/repository/sqlc/**`, `ee/repository/postgres/sqlc/**`), swagger,
  and cmd mains from analysis; coverage expects `coverage.out`.
- Avoid introducing SonarQube issues: no DELETE without WHERE, keep coverage in new
  packages high (localrepo aims ~90%+).

## Things to remember

- Big (10k+ files) manifests are expected on day one — never plan an O(entire repo)
  DB write for an update path; default to diffing against the stored rows and
  writing only the difference.
- Keep the free/enterprise boundary: `internal/` never imports `ee/`, the free
  `cmd/nipad` links no ee package (sqlite + local chunks only), and postgres/S3
  are wired exclusively from `ee/cmd/*`; see `docs/POSTGRES.md`.
- Revert is forward-only: one commit per target, history is never rewritten, and
  pending `revert_state` is cleared only on success/abort. It reuses existing
  queries and added no SQL, sqlc, or schema changes.
- Do not commit secrets, config.yaml (gitignored, sample is `config.yaml.sample`).
- Added/edited tests should run under `go test ./...` with `go vet ./...` clean.
- If you change `opencode.json`, agents, or skills, tell the user to restart
  opencode after saving.
