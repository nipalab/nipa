# Release readiness checklist — v0.1.0 (beta)

Status: **audit (2026-10-15)**. Scope: the first public beta of both editions
(free `nipa`/`nipad` + enterprise `nipad-ee`, source-available under `ee/`).
Every item cites the file or target that proves it; check items off on the
release commit. The manual e2e plan (`docs/manual-test-plan.md`) is a
pre-release step, not a replacement for the automated gates below.

## 1. Release infrastructure

- [ ] **Release workflow.** `.github/workflows/` has `ci.yml` and `web.yml`
  only; `make release` just creates a local tag. Add `.github/workflows/
  release.yml` triggered on `v*` tags that builds `bin/nipa`, `bin/nipad` and
  `bin/nipad-ee` (linux/darwin/windows × amd64/arm64 for the client), writes
  `SHA256SUMS`, and attaches everything to the GitHub Release with
  `CHANGELOG`-based notes.
- [ ] **First tag.** No git tags exist yet. Tag with
  `make release version=v0.1.0` (validates clean tree + `vX.Y.Z` format) and
  push the tag; the release workflow consumes it.
- [ ] **Container images.** No Dockerfile exists for either server (only
  `tests/docker-compose.ee.yml` for tests). Provide Dockerfiles for `nipad`
  (SQLite + local chunks) and `nipad-ee` (postgres + S3), publish them, and
  document pull/run.
- [ ] **Pin `golangci-lint`.** `Makefile` uses
  `GOLANGCI_LINT_IMAGE ?= docker.io/golangci/golangci-lint:latest`; pin a
  fixed version so lint results are reproducible.
- [ ] **Versioning policy.** Record the 0.x semver contract (breaking changes
  allowed between minors during beta) in the README/CONTRIBUTING.
- [ ] **CHANGELOG.** Add `CHANGELOG.md` (or a documented release-notes
  convention) and write the v0.1.0 entry from the git history.
- [ ] **Branch protection & CODEOWNERS.** Protect `main` (require CI + review),
  add `.github/CODEOWNERS`; consider Dependabot/Renovate for Go and npm
  dependencies.

## 2. Community & legal

- [ ] **CONTRIBUTING.md.** Build/test workflow (`make build`, `make sqlc`,
  `make mock`, `make test`, the `tests/run.sh` harness with `NIPA_TEST_HOST`),
  DCO/CLA decision, commit style, and the ee/ boundary rule (`internal/` never
  imports `ee/`).
- [ ] **SECURITY.md.** Vulnerability report address, supported versions, and
  the responsible-disclosure window.
- [ ] **CODE_OF_CONDUCT.md.** Adopt a standard text (e.g. Contributor
  Covenant) and name a contact.
- [ ] **Issue & PR templates.** `.github/ISSUE_TEMPLATE/` plus
  `PULL_REQUEST_TEMPLATE.md`; PR template should ask for the relevant tests
  and `make lint` status.
- [ ] **Third-party license audit.** Run `go-licenses` over `internal/`, `cmd/`
  and `ee/` and a license check over `web/` (npm); add a `NOTICE` if any
  bundled dependency requires attribution. The dual-license statement
  (`LICENSE` Apache 2.0 + `ee/LICENSE` commercial, README §License) must stay
  accurate for the published tree.
- [ ] **Legal review.** Recommend counsel review of `ee/LICENSE` and the
  dual-license positioning before the first public tag.
- [ ] **GitHub metadata.** Repository description, topics, Discussions
  on/off, and a final org/name check — the Go module path
  `github.com/nipalab/nipa` (`go.mod`) must match the public remote.

## 3. Security

- [ ] **Placeholder-secret guard.** `config.yaml.sample` ships
  `JWT_KEY: secret` and `CHUNK_URL_SIGNING_KEY: change-me`. Both binaries
  should refuse to start (or log a prominent warning) on known placeholder
  values, the same way `nipad-ee` rejects unsupported backends.
- [ ] **Admin bootstrap story.** Document how a fresh server gets its first
  admin (config seeding; the manual test plan references the seeded
  super-admin) and whether the password must be rotated on first login.
- [ ] **Login rate limiting.** Confirm brute-force protection on the password
  login and the SSH challenge flow (`BeginLoginSSHKey` / `CompleteSSHLogin`),
  or add a limiter; every failure already returns the generic 400.
- [ ] **`govulncheck` in CI.** Add `govulncheck ./...` (and keep deps
  updated) to `ci.yml`.
- [ ] **Secret history scan.** `config.yaml`, `.env*` and `*.db` are
  gitignored; scan the full history (`git log -p`, gitleaks) once before the
  repository is made public.
- [ ] **Re-verify hardening defaults.** pprof off by default
  (`PPROF_ENABLED: false`), `CHUNK_PRESIGN_TTL_SECONDS: 3600`,
  `WEBHOOK_EGRESS_ALLOWLIST` empty (public targets only), daemon
  `daemon.json` 0600 with constant-time token compare — all implemented, but
  re-check on the release commit and state them in the deployment doc.

## 4. Product completeness

- [ ] **Web UI status.** The README still labels the SPA "work in progress".
  Either finish it for beta or relabel it explicitly (e.g. "beta: the tags
  page is not implemented yet"); release tags have REST routes and API
  clients (`web/src/api/endpoints.ts` `listTags`/`createTag`/`deleteTag`) but
  no SPA page.
- [ ] **Reconcile `docs/MERGE_REQUEST_GAPS.md`.** The file is the audit trail;
  several notes are stale (email notifications are implemented per
  `docs/EMAIL.md`; the review-request `user id` lookup nicety). Re-verify each
  `[partial]` item and mark beta-blocking vs post-release.
- [ ] **Defer observability phase 3 explicitly.** `docs/OBSERVABILITY.md`
  phases 1–2 (logging, request IDs, `/healthz`, `/readyz`, `/metrics`, pprof)
  are implemented; mark OTel tracing as post-release in the doc and README.
- [ ] **Execute the manual test plan.** Run `docs/manual-test-plan.md` end to
  end against the built binaries and record the result (add a "last executed"
  line or a results file).
- [ ] **Upgrade & backup contract.** Test `make migrate-down` on both
  dialects, document SQLite backup/VACUUM and postgres backup guidance, and
  state the local-clone policy (no local schema migration — re-clone) in the
  deployment docs.
- [ ] **Production deployment doc.** TLS reverse-proxy guidance for the single
  h2c port (`internal/serverapp`), `config.yaml.sample` must-change list
  (`JWT_KEY`, `CHUNK_URL_SIGNING_KEY`), env vars, metrics/health endpoints,
  systemd unit sample.
- [ ] **Version surface smoke.** `nipa version`, `nipa version --server <url>`
  and unauthenticated `GET /version` report the release build for both
  editions.

## 5. Quality gates on the release commit

- [ ] `go build ./...` and `go vet ./...` clean.
- [ ] `go test -race ./...` with coverage plus the SonarQube quality gate
  green (`ci.yml`).
- [ ] `make test-client-both` green (free first, then ee with postgres +
  minio).
- [ ] Web: `npm run lint`, `npm test`, `make web` green (`web.yml`), and the
  built SPA embedded (`web/server/dist` via `web/server/webui.go`).
- [ ] `make lint` clean with the pinned golangci-lint version.
- [ ] Clean-machine quickstart from the release binaries: clone → add →
  status → push → branch → merge request → merge → tag → lock/unlock.
- [ ] Cross-platform CLI smoke (Windows/macOS): path handling, line endings
  in text merges, and the `NIPA_TOKEN_FILE` file-backed credential fallback
  when no OS keyring exists.
- [ ] Throughput sanity: `make bench-upload mb=512` and
  `make bench-download mb=512` results recorded in the release notes.

## 6. Performance & capacity (SaaS beta)

See `docs/PERFORMANCE.md` for scenarios, metric names and the capacity notes;
`tests/load/README.md` for the harness usage.

- [ ] **Baselines recorded.** Run `make bench-upload mb=512`,
  `make bench-download mb=512`, `make bench-scale gb=2` and `make bench-unit`
  on the release machine; fill the baseline table in `docs/PERFORMANCE.md`
  (same machine notes, same `NIPA_BENCH_WORKERS`).
- [ ] **Memory ceiling holds.** `make bench-scale gb=2` passes: peak heap
  growth stays below the file size (streaming verified for the multi-GB path).
- [ ] **Load scenarios executed on ee.** `make load-test args='-scenario
  push|clone|mixed -clients 16 -duration 60s'` against a staging-like
  deployment (real postgres + S3, not minio); findings recorded (max clean
  clients, error rate, pool waits, wire rates).
- [ ] **Free-edition envelope measured.** `tests/load -scenario push` finds the
  max clean concurrency on SQLite; the recommended team/repo-size ceiling is
  documented in the README (`docs/PERFORMANCE.md` §free-edition envelope).
- [ ] **Postgres pool sized.** Open item: the pool is untuned (no
  `SetMaxOpenConns` above tests). Add `DB_POOL_*` knobs, size per pod so
  `pods × max_open + headroom < max_connections`, and verify with
  `nipa_db_pool_wait_*` deltas; otherwise treat pod count as unmeasured.
- [ ] **SLOs ratified.** The proposal table in `docs/PERFORMANCE.md` is
  reviewed and either accepted or re-targeted; accepted numbers are quoted in
  the release notes.
- [ ] **CI stays fast.** The benches and load runner remain env-gated
  (skipped in `make test`/CI); schedule the load run as a manual/nightly job,
  never a per-PR gate.
- [ ] **Quota under load (ee).** A near-quota project in the load run gets 402s
  before bytes are accepted (`docs/CLOUD_STORAGE_QUOTA.md` semantics), not
  after.

## 7. Repository hygiene before the public snapshot

- [ ] No stray local artifacts committed: `nipa.db*`, `chunks/`, `temp/`,
  `coverage.out`, `config.yaml`, `.nipa/` scratch dirs — all gitignored today;
  confirm the release commit contains none.
- [ ] `web/server/dist` holds only the built assets intended for embedding
  (`.gitignore` keeps `index.html` only).
- [ ] Free/enterprise boundary re-check: `internal/` imports no `ee/` package
  and `bin/nipad` links no ee code (verify with `go list -deps ./cmd/nipad`).
- [ ] README quickstart matches the Makefile targets in this tree
  (`make build-server build-client`); every README claim links to something
  shipped in v0.1.0.
- [ ] `docs/` is internally consistent: no doc describes a feature as
  "planned" that shipped, or vice versa.
