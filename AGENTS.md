# AGENTS.md

Project context for opencode. Read this before working in this repo.

## What this project is

**Nipa** is a centralized version control system (Go/gRPC/SQLite) targeting
binary-heavy projects (game dev, 3D assets, media) and large monorepos. It
blends Git (lightweight branches, merge requests), Perforce (binary streaming),
and SVN (path-based access control). Client-server monorepo: `nipa` (client,
CLI at `cmd/nipa`) and `nipad` (server at `cmd/nipad`). `nipad` multiplexes
gRPC (h2c), the REST API and the embedded SPA on one port — see
`cmd/nipad/main.go` (`isAPIPath` routes `/auth`, `/docs`, `/api`; everything
else falls through to the web UI handler). Dual-licensed (Apache 2.0;
everything under `ee/` is enterprise).

## Architecture

- **Server** (`internal/`): `domain` (entities/errors), `usecase` (business
  logic, gomock-tested, depends only on repository interfaces; `browser.go` /
  `browser_history.go` power the web UI tree/commit browsing and
  `merge_request.go` the MR lifecycle), `repository`
  (`sqlite`/`postgres` implementations over sqlc-generated `internal/repository/sqlc/*`),
  `grpc` (proto + `pb` generated + `server` handlers), `http` (REST API).
- **Web UI** (`web/`): Vite/React/TypeScript SPA in `web/`, built with Primer
  React (`@primer/react` + `@primer/primitives` + `@primer/octicons-react`).
  UI code lives in `web/src/` (components use v38 API: `Stack`/`Banner`; the
  old `Box`/`Flex`/`sx` API is gone). Pin `react-is` to the same major as
  React (18) — react-is 19 checks `Symbol.for("react.transitional.element")`
  and silently breaks every Primer visual (`Button` `leadingVisual` etc.) with
  React 18 → black screen. `Banner` REQUIRES a `title` prop (or
  `<Banner.Title>`), children-only usage throws and unmounts the app. Theme is
  dark-only for now: `index.html` sets `data-color-mode="dark"` +
  `data-dark-theme="dark"` and a `html { background-color:#0d1117;
  color-scheme:dark }` inline style (without it the shell is transparent, so the
  bg comes from the browser canvas and text looks dark-on-dark), and
  `main.tsx` passes `colorMode="dark"`; only `dark.css` is imported. Built
  with `make web` into
  `web/server/dist`, embedded into the `nipad` binary via
  `//go:embed all:dist` in `web/server/webui.go` (package `webui`) and served
  for non-API paths in `cmd/nipad/main.go` (`isAPIPath`).
- **Client** (`internal/client/`): `cli` (cobra commands: clone, branch
  (`-a` list, `-c` create+switch, `-d` delete), add, remove, status, push, update, switch,
  merge, revert, log, diff, acl, group, sparse-checkout, mr, lock, unlock), `usecase`
  (clone/login, push, update/switch (incl. sparse), merge, revert, diff, log,
  permission, merge-request orchestration, file locks over small interfaces; `threeway.go`
  holds the shared materialize/stage/delete core), `grpc` (gRPC transport,
  converts pb→server domain types), `merge` (three-way tree decisions + diff3
  text merge), `securestorage` (keyring-backed token store), `config`
  (`~/.config/nipa/config.json`: `diffExternal`, `uploadWorkers`), `difftool`
  (external diff runner), `domain` (client config/url/errors/commit/pending
  state), `localrepo` (`.nipa/` local metadata + SQLite). `localrepo.FindRepoRoot`
  searches up to 32 parent directories for a `.nipa/config` so every in-repo
  command works from subdirectories.
- **Diff engine** (`internal/diff/`): pure package shared by client and server
  candidates — Myers line diff (`Lines`/`LinesWith` with an equality hook for
  whitespace ignores), tree-state comparison
  (`Entry`/`Change`/`Compare`/`FromTree`/`LoadContent`), rename detection
  (`DetectRenames`), content assembly (`AttachContents`), a server-facing
  tree-vs-tree entry point (`TreeDiff`) and unified/stat/name rendering.
  `internal/client/merge` delegates its line diff to `diff.Lines`. Keep this
  package free of client imports: the server merge-request diff reuses it.

Flow for `nipa clone <url> <target>`: `cli/clone.go` parses the URL →
`usecase.Repo.Clone` → ensure empty target → login → resolve default branch (or
`GetBranchByName` when a branch is given) → fetch tree manifest (gRPC
`GetTreeManifest`, recursive, with the URL path) → `localRepo.Init(target)` →
`SaveConfig` (`.nipa/config` json: url + branch + sparse prefixes) →
`syncWorkingCopy` (shared with update/switch: download missing chunks through
the commit-scoped `ChunkScope`, materialize files, record `stat_cache`
fingerprints) → `SaveTree` → `SaveCommit` (branch head commit id). Clone,
update and switch record the branch head commit id locally so `HEAD`/`@` (and
branch creation via `nipa branch -c`, which forks from the pinned commit)
resolve offline.

Flow for `nipa sparse-checkout <list|set|add|remove|disable>` (and `nipa clone
--sparse a,b`): `Update.SetSparse` normalizes the prefixes
(`domain.NormalizePathPrefix`, dedup, empty = full checkout) into
`.nipa/config`'s `sparse` field, then runs a regular update. The manifest
request and the chunk download scope carry the prefixes, so only the sparse
set is materialized and saved; files outside the set are removed from disk and
swept from the local DB but stay tracked on the server. Push from a sparse (or
permission-filtered) clone sends the pinned head commit as
`base_commit_id` (preferred over `base_tree_hash`) so the server applies the
delta to the full base tree. Merge and revert still require a full checkout;
`Push.Run` refuses to run mid multi-target revert sequence.

Flow for binary file locks (`nipa lock <path> [--branch]`, `nipa unlock <path>`,
`nipa lock list`): binary changes are mandatory-lock gated. `FileLock.Acquire`
resolves the scope from the branch — the default branch is a project-global lock
(`file_locks.branch_id NULL`), any other branch a lock of that branch — and
rejects a path already covered (exact or directory prefix, `domain.PrefixCovers`)
by another holder; the same holder is idempotent. `Push.Push` classifies the
touched binaries against the head tree (`headBinaryPaths`): tracked binaries
being modified or removed must be covered by the pusher's lock (409 `binary file
%q requires a lock`), while new binary files only conflict with someone else's
covering lock (409 `%q is locked by <holder>`) — adding a brand-new asset needs
no lock unless a directory or pre-emptive lock guards it. `EnsureLocks` enforces
both lists in the target scope; after `ApplyPush`, `ReleaseLanded` drops the
pusher's own exact-path, non-request locks while directory locks span the
editing pass and stay. Merge requests acquire locks for their changed binary
paths at creation (re-checked and topped up at merge — `branchMerger.
BinaryChangesBetween` is the permission-filter-free three-dot enumeration —
accepting locks held by the request owner so an admin can merge on their
behalf), release them on merge/close, re-acquire on reopen, and clean up
partial acquisitions when creation fails. Plain `FastForward` enforces/releases
via `BinaryLockPlan` (required vs checked paths); `Branch.Delete` releases the
branch's scoped locks (soft delete ⇒ no FK cascade). The `/locks` REST routes,
`LockFile`/`UnlockFile`/`ListFileLocks` RPCs and the web Locks page expose
acquire/release/list.

Flow for `nipa revert <commit>` / `<from>..<to>`: resolve targets via gRPC
`GetCommit` / `WalkCommits` (range walks newest-first, exclusive stop; ranges are
capped at 16 targets and rejected before any three-way work) → for each
target three-way merge `(base=target tree, ours=current head, theirs=mainline
parent tree; root commit ⇒ empty parent; merge commits need --mainline 1|2)` via
`usecase/threeway.go` → stage and commit one revert per target through
`Push.pushStaged` (base = previous head/tree hash) → on conflict persist
localrepo `revert_state` (remaining targets, current tree hash + commit id,
conflicts) and return; `--continue` commits the resolved step and resumes,
`--skip` drops it, `--abort` restores the original tree/pin, `--no-commit`
applies to the working copy without committing (single commit, `-m` sets the
message). Reverts are forward-only; history is never rewritten.

Flow for `nipa diff [<rev1> [<rev2>]] [--] [<path>...]`: `cli/diff.go` splits
revisions from paths (paths only after `--`; `<a>..<b>`/`<a>...<b>` expand to two
revisions and `...` sets merge-base mode) → `usecase.Diff.Run`. With no revisions
it is fully offline: `Snapshot()` (paths, hashes, modes and per-file chunk
hashes) + `ListStaged()` → `walkWorkingFiles` (shared with `WorkingCopy.Status`).
Tracked files whose `stat_cache` fingerprint is fresh and whose file hash equals
the old side are reused without reading; everything else (staged additions,
stale fingerprints, real changes) is hashed with `chunkFile` → `diff.Compare`
over path-keyed `diff.Entry` maps; untracked-unstaged files are excluded, missing
tracked files are deletions, staged new files are additions. `--no-cache` reads
every working file. With revisions, each token
resolves to a commit ID first (branch name via gRPC `GetBranchByName`; `HEAD`/`@`
via localrepo `LoadCommit`, falling back to the configured branch; otherwise
`snow.ParseBase36` → gRPC `GetCommit`), then `GetCommit(id).Tree` is flattened
with `diff.FromTree`; missing chunks are downloaded with `downloadMissing`
through the commit-scoped `ChunkScope` (binary content only with
`--ext-diff`). `<a>...<b>` calls `GetMergeBase` with
the two commit IDs and uses `MergeBaseTree` as the old side. Renames always run
through `diff.DetectRenames` (exact hash, line similarity for text, chunk
overlap otherwise); `-w`/`-b` live in `diff.Options` and `diff.FilterIgnored`
drops files whose differences are fully ignored. `--ext-diff` runs
`internal/client/difftool` with git's 7-argument contract (command from
`NIPA_EXTERNAL_DIFF`/`diffExternal`) and only when requested. `--staged`/`Paths`
narrow the result; rendering (patch, stat, name-only, name-status), colors and
the pager live in `cli/diff.go` + `internal/diff`.

Server merge-request diffs reuse the same engine: `usecase.MergeRequest.Diff`
resolves the source and target heads, `GetMergeBase`, and calls
`Branch.TreeDiffBetween` (`internal/usecase/browser.go`), which loads both
manifests with `commitTreeManifest` (each side pruned by the caller's read
filter) and calls `diff.TreeDiff(baseTree, sourceTree, chunkStore.Get)` — a
three-dot diff like GitHub/GitLab, exposed at
`GET /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}/diff`. The web
UI commit pages diff a commit against its first parent through the same
`commitTreeManifest` + `diff.TreeDiff` path (`?path=` filters to a file/dir).

## Commands

From `Makefile`:

- `make sqlc` — regenerate all three sqlc packages (server sqlite/postgres +
  `internal/repository/sqlc/localrepo` for the client). Uses
  `go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest`.
- `make mock` — `go generate ./...` (regenerates gomock mocks from `//go:generate`).
- `make migrate-up` / `make migrate-down` — `go run ./cmd/migrate`
  (default `DRIVER=sqlite3 DSN=nipa.db`; override for postgres). `nipad` also
  applies pending sqlite migrations at startup.
- `make migrate-create name=<name>` — new up/down migration pair in
  `db/migrations/`. Migrations live per-dialect in `db/migrations/sqlite|postgres`
  (embedded via `go:embed` in `db/migrate.go` and read by `sqlc.yaml`), so move
  the created pair into the dialect dir.
- `make build` — builds `bin/nipad`; `make build-client` — builds `bin/nipa`;
  `make build-all` — web + server + client.
- `make web` / `web-dev` / `web-install` — build the SPA into
  `web/server/dist` / run the Vite dev server (proxies `/api`, `/docs` to
  `NIPA_SERVER_URL`) / `npm install`.
- `make proto` — regenerates pb from `internal/grpc/proto/server.proto`.
- `make lint` — golangci-lint via Docker image (`.golangci.yml` enables
  errcheck/govet/ineffassign/staticcheck/unused/misspell/unconvert; formatters
  gofmt/goimports).

Direct: `go build ./...`, `go vet ./...`, `go test ./...`.

- `make bench-upload mb=512` (or `NIPA_BENCH_MB=512 go test ./internal/e2e
  -run TestEndToEnd_UploadThroughput -v`) measures upload throughput against an
  in-process server: it pushes a repetitive text file, a random binary file and
  a packed-asset `.png`, logs MiB/s, encoding, chunk and HTTP PUT counts, then
  re-clones and verifies the bytes. Skipped unless `NIPA_BENCH_MB` is set;
  `NIPA_BENCH_WORKERS` pins transfer concurrency (otherwise the adaptive tuner
  is exercised).
- The server opens SQLite through `db.OpenSQLite`, which appends
  `_pragma=busy_timeout(10000)` and `_pragma=journal_mode(WAL)` to the DSN.
  Without WAL + a busy timeout, concurrent reads (every RPC resolves the
  project first) collide with confirm/Push writes and surface as
  `database is locked (5) (SQLITE_BUSY)`.

## Conventions

- **Package layout**: consider packages under `internal/` as the border; use the
  existing import ordering (stdlib, third-party, then `github.com/nipalab/nipa/*`).
- **IDs**: server-side records use `github.com/nipalab/nipa/internal/snow`
  (`snow.ID`, `.Base36()` for wire, `.Int64()`/`snow.ParseBase36` for DB/proto).
  Client localrepo tables do NOT use snow IDs.
- **Errors**: `internal/domain.Error` with HTTP-style `Code` + `Message` +
  `InternalMessage` + `Cause`. Constructions: `NewErrorNotFound(msg)`,
  `NewErrorNoPermission()`, `NewErrorUser(msg)`, `NewErrorDatabase(internalMsg)`,
  `NewErrorInternalServer(...)`. Predicates: `domain.IsErrorNotFound(err)`,
  `domain.IsErrorNoPermission(err)`. Client mirrors this with
  `internal/client/domain.Error` (`NewUserError` 400, `NewTokenError` 401).
- **SQL**: schema + queries are sqlc inputs (`db/migrations/sqlite|postgres`,
  `db/queries/sqlite|postgres`, and `internal/client/localrepo/schema|queries`).
  After changing a `.sql` file, run `make sqlc` and check the generated Go
  compiles. Named params (`:name`) are converted to `?N` in `database/sql`.
- **sqlc gotchas**: a multi-statement `:exec` query has its SQL truncated by sqlc
  to one statement — use separate single-statement queries instead. Full-table
  `DELETE` without `WHERE` trips SonarQube (S1035-ish); keep them out.
- **Testing**: testify (`require`/`assert`) + gomock (`go.uber.org/mock`,
  `github.com/golang/mock` legacy mocks exist in `*_mock_test.go`). Server usecase
  tests use `NewMock<h1>...` gomock controllers; client usecases use hand-written
  stubs. SQLite-backed tests use `t.TempDir()` for real DBs.
- **Mocks**: live next to the code with the name suffix `_mock_test.go`; regenerate
  with `make mock`. In `internal/grpc/server`, `*_deps_mock_test.go` /
  `*_usecase_mock_test.go` are the mocks.
- **gofmt/golangci**: new/edited files must be gofmt-clean. One legacy file was
  left unformatted on purpose — do not reformat it unless the whole file is
  already clean: `internal/client/domain/url_test.go`.

## Client localrepo design (`internal/client/localrepo`)

- `.nipa/` inside the clone target: `config` (json `{url, branch, sparse?}`) written
  atomically via `atomicWrite`, `nipa.db` (SQLite, `modernc.org/sqlite`,
  `_pragma=foreign_keys(ON)` in the DSN — cascades silently don't fire otherwise),
  and `objects/` with Git-style loose chunk objects.
- Schema (in `schema/schema.sql`, embedded via `//go:embed`, also the sqlc source):
  - `tree_nodes(path TEXT PRIMARY KEY, parent_path, hash BLOB, mode, snapshot_id)`
  - `files(path TEXT PRIMARY KEY, tree_path FK->tree_nodes ON DELETE CASCADE, hash,
    size_bytes, mode, is_binary, encoding, chunks BLOB, snapshot_id)` — `chunks` is
    the concatenated 32-byte hashes of the file's chunks (metadata only, no
    content); `encoding` is `raw` or `zstd` and refers to the stored chunk bytes.
  - `meta(key PK, value)` — stores `tree_hash`, the pinned `commit_id` /
    `commit_hash`, and the pending `merge_state` / `revert_state` JSON.
  - `staged_files(path PK, staged_at)`.
  - `stat_cache(path PK, size_bytes, mtime_ns, mode, hash, cached_at)` — working
    file fingerprints whose `hash` was validated when the content was last hashed;
    `cached_at` (unix nano) must be newer than `mtime_ns` for the row to be trusted.
- Content lives in `.nipa/objects/<hash[:2]>/<hash[2:]>` (content-addressed loose
  objects, written temp+rename, never erased so `nipa update` can skip unchanged
  content). `StoreChunks`/`StoreChunk` write objects; `OpenChunk`/`LoadChunk` read
  them; `MissingChunks` stats them. Update/switch cache every downloaded chunk,
  merge/revert cache materialized content, and `Push` caches the chunks it uploads
  before sending them. The DB holds metadata only; file→chunk hashes are stored as
  the `files.chunks` blob (no chunk catalog table), which is what lets `nipa diff`
  rebuild the old side offline.
- `SaveTree` semantics (see `store.go`): one transaction. Token = current tree
  hash (`""` for empty). Upsert tree nodes/files by `path`
  (`ON CONFLICT(path) DO UPDATE`, including the encoded `chunks` blob); then
  **mark-and-sweep**:
  `DELETE FROM files/tree_nodes WHERE snapshot_id <> :token` removes only rows that
  were removed/renamed since the last save — never a full-table clear, so saving a
  10k-file repo with 5 changes touches ~5 rows, not the whole table. The same
  transaction drops `stat_cache` rows whose path left `files`. `meta_tree_hash`
  re-set each save. Empty repo: `SaveTree(nil)` sweeps all rows. Cached objects are
  never touched by `SaveTree`.
- `stat_cache` is what keeps `nipa status`/`nipa diff` cheap on big binaries: a
  tracked file whose `(size, mtime)` match a fresh fingerprint (`mtime < cached_at`)
  is trusted without reading, while misses are rehashed (bounded worker pool in
  `Status`) and written back in one `SaveStatEntries` transaction. Fingerprints are
  recorded only when content is known — files materialized by `syncWorkingCopy` /
  `applyThreeWay` and files scanned by `Push`; update's no-op skip path is
  deliberately left alone because it never verified the file, so a dirty file is
  hash-detected once by `status`. Paths are working-copy relative (no leading
  slash) and the sweep strips `files.path`'s leading slash. `status --no-cache` /
  `diff --no-cache` force a full rehash (and refresh the cache). Accepted blind
  spot, same as SVN/Git: an edit that preserves size and mtime is invisible until
  the mtime changes.
- There is no schema migration: the local DB is recreated from scratch (an old
  clone just needs to be deleted and re-cloned).
- The `localRepo` interface (used by `usecase.Repo.Clone`): `Init(target)` /
  `SaveConfig(domain.Config)` / `LoadConfig` / `SaveTree(*serverDomain.TreeNode)`,
  plus the content cache methods `MissingChunks` / `StoreChunks` / `OpenChunk` /
  `LoadChunk`, the working-copy fingerprints `SaveStatEntries` (`LoadStatCache` on the
  status/diff interfaces), staging (`StageAdd` / `StageRemove` / `ListStaged` /
  `ClearStaged`), the head pin (`SaveCommit` / `LoadCommit`) and the
  pending-operation state (`Save/Load/ClearMergeState`,
  `Save/Load/ClearRevertState`). `Push.Run` reads both pending states: merge state
  wins for the base tree/second parent (plus `TargetCommitID`), otherwise
  `RevertState.CurrentTreeHash`/`CurrentCommitID` is the base, and when neither is
  pending the pinned head commit (`LoadCommit`) is sent as `base_commit_id`; both
  states are cleared only after a successful push, and a multi-target revert
  sequence blocks `Push.Run` until `--continue`/`--abort`.
- `clientDomain.Snapshot` files carry the file-level hash (`files.hash`, derived
  from the server manifest by `client/grpc.toServerFile` as `chunker.FileHash` of the
  chunk hashes) plus the `Chunks` hash list decoded from `files.chunks`; content
  itself stays in the loose object store.

## Proto / gRPC

- Source is `internal/grpc/proto/server.proto`; service `NipaService` with
  `LoginWithUsernamePassword`, `LoginWithRefreshToken`; branch RPCs
  `GetListBranch`, `GetBranch`, `GetBranchByName`, `GetDefaultBranch`,
  `CreateBranch`, `RenameBranch`, `DeleteBranch`, `SetDefaultBranch`,
  `SetBranchProtection`; history/tree RPCs `GetTreeManifest`, `GetCommitLog`,
  `GetCommit`, `WalkCommits`, `GetMergeBase`, `MergeFastForward`; merge-request
  RPCs `CreateMergeRequest`, `UpdateMergeRequest`, `ListMergeRequests`,
  `MergeMergeRequest`, `CloseMergeRequest`; transfer RPCs `Push`,
  `GetChunkUploadUrls`, `GetChunkDownloadUrls`, `ConfirmChunkUploads`; file-lock
  RPCs `LockFile`, `UnlockFile`, `ListFileLocks`; PBAC
  RPCs `GetMyPermissions`, `CreatePBACRule`, `ListPBACRules`, `DeletePBACRule`,
  `ListProjectPathPermissions`, `SetProjectPathPermission`,
  `DeleteProjectPathPermission`; group RPCs `CreateGroup`, `ListGroups`,
  `AddGroupMember`, `RemoveGroupMember`.
  `rpc` `GetTreeManifest` takes
  `{context{org,project}, branch, path, tree_hash?, recursive, paths}` (`paths`
  = directory prefixes to include, empty = whole tree) and returns
  `{branch, root_tree}`. `TreeManifest` carries `tree_hash`, `path`, `sub_trees`,
  `files` (`path, mode, size_bytes, is_binary, chunk_hashes`). `GetBranch` takes
  `branch_id` (base36) and `GetBranchByName` takes `name`; both return the branch
  with its head `commit_id`. `CreateBranch` takes `from_branch` and/or
  `from_commit_id`/`from_commit_hash` (exact fork point, base36/hex).
  `GetCommitLog` takes `{context, branch, start_commit_id?, limit}`.
  `GetCommit` takes
  `{context, commit_id}` (base36; **ID only**, no hash alias) and returns the
  commit detail plus its recursive tree manifest; `WalkCommits` takes
  `{context, start_commit_id, stop_commit_id?, limit}` and returns
  `CommitWalkEntry`s newest-first, both-parents, stop-exclusive. `GetMergeBase`
  takes branch names and/or optional `target_commit_id`/`source_commit_id`
  (base36, take precedence) so a merge base can be resolved for commit pairs.
  All are project-scoped in the usecase because `CommitGet`/`CommitGetByHash`
  have no project filter, and commit IDs are server-local (snow) identifiers.
- **Chunk transfer is presigned HTTP, not gRPC streaming.** `GetChunkUploadUrls`
  (project write; refs carry `hash` + exact `size_bytes`) and
  `GetChunkDownloadUrls` (`commit_ids` + optional `paths` prefixes + `hashes`;
  the visible set is the union of the referenced commit trees narrowed by the
  prefixes — `Branch.VisibleChunks(projectID, commitIDs, paths)` — and requested
  hashes are filtered against it first) return one
  page of signed paths built by `internal/chunkurl` (`/api/chunks/{org}/{project}/{hash}
  ?op=put|get&size=&exp=&sig=`, HMAC-SHA256 with `CHUNK_URL_SIGNING_KEY`,
  constant-time verify, TTL `CHUNK_PRESIGN_TTL_SECONDS` default 3600s). The
  client then PUTs/GETs bytes directly over HTTP through an adaptive transfer
  limiter (`internal/client/grpc/transfer.go`): concurrency starts at 16, is
  auto-tuned within [4, 64] from observed throughput, and can be pinned with
  `NIPA_UPLOAD_WORKERS` or `uploadWorkers` in `~/.config/nipa/config.json`
  (pinned disables tuning). `ConfirmChunkUploads` measures the stored content
  size server-side (`ChunkStore.Size`, never a client-supplied value) and writes
  the chunk metadata rows, so `Push` can rely on them; confirmations are
  serialized client-side under a mutex so concurrent upload windows never stack
  SQLite metadata writes. Chunks already stored come back with `already_stored` so
  clients skip the transfer. `Push` sends `base_commit_id` (base36; preferred over
  `base_tree_hash`, so sparse/permission-filtered clones push deltas against the
  full server tree) and `parent_2_commit_hash` for merge commits.
  `Push.pushStaged` scans into 16MB windows handed to
  two uploader goroutines (queue depth 2), so hashing overlaps with the network;
  window chunks are cached locally before upload and the scanner only blocks
  when the bounded queue is full. Chunk sizes are content-aware
  (`internal/chunker/profile.go`): text and in-place-edited formats (`.blend`,
  `.psd`, `.mb`, `.sqlite`, ...) keep the 64KB default, packed assets (textures,
  audio, video, models, archives) use 4MB average chunks, unknown binaries 1MB;
  the server accepts refs up to `chunker.MaxChunkSize()` (16MB). Text is
  zstd-compressed per file (`chunker.Encode`): files up to 8MB raw become one
  compressed chunk (≤4MB), larger text falls back to CDC chunks of the plaintext
  (256KB avg) compressed individually. Every file carries an `encoding` column
  (`internal/chunker.EncodingRaw`/`EncodingZstd`, proto `FileNode.encoding`,
  `files.encoding` on both server and localrepo); hashes always refer to the
  stored bytes, and every client hashing path (push, add/status, diff,
  merge/revert materialization, guarded remove) must go through
  `chunker.Encode`/`chunker.ConfigForFile`, passing the tracked encoding for
  stored files (`storedEncoding`), so file hashes stay consistent; decode at
  assembly points with `chunker.Decode` (client materialization,
  `diff.LoadContent`, merge's `loadFileContent`). The signed routes live in a `/api/chunks` webService (go-restful
  `os.Exit`s on duplicate root paths so it cannot share `/api/v1`) and are NOT
  behind the Bearer filter — the signature is the capability. HTTP PUT pins the
  exact size (Content-Length check + `MaxBytesReader` + BLAKE3 verify) and only
  writes content (`Chunk.StoreUploaded`, no DB writes, avoiding SQLite lock
  contention from concurrent PUTs); DB metadata is written at confirm time.
  `internal/client/grpc/chunks.go`'s `UploadChunks`/`DownloadChunks` take the
  `ChunkScope{Org, Project, CommitIDs, Paths}` that scopes download visibility;
  download callbacks are
  serialized under a mutex because transfers run concurrently. The server only
  returns relative paths: the client connects with the bare host as the gRPC
  target (also the token-storage key) and `Client.httpURL` prefixes `http://`
  when the target has no scheme.
- `Parsec`/`ParseNipaUrl` (`internal/client/domain/url.go`): `/org/project[/path]`.
  `path` is threaded through to the manifest request so a missing subpath returns a
  404, and cloning a repo with an empty (no-commit) branch returns an empty tree,
  not an error (branch root only).

## SonarQube / CI

- CI runs `go build ./...`, `go vet ./...`, `go test -race -coverprofile=...`.
- `sonar-project.properties` excludes generated code (`internal/repository/sqlc/**`),
  swagger, and cmd mains from analysis; coverage expects `coverage.out`.
- Avoid introducing SonarQube issues: no DELETE without WHERE, keep coverage in new
  packages high (localrepo aims ~90%+).

## Web UI

- Routes/API map lives in `docs/WEB_UI.md`. SPA uses `react-router-dom` v6,
  memory-only access token + `HttpOnly` refresh cookie, and a capability mask
  from `/permissions/me` to hide admin controls (server remains authoritative).
  Pages cover repository/org lists, tree browser, blob viewer, commit history
  + diffs, branch management, merge requests (list/create/detail/merge),
  file locks (list/lock/unlock), project/org settings (protection + ACL,
  members/groups), user administration
  and profile; the browse endpoints are served by `internal/usecase/browser.go`
  (+ `browser_history.go` for per-file/per-dir last-commit info) under
  `internal/http/api/`.
- Keep `web/src/api/endpoints.ts` in sync with the REST handlers under
  `internal/http/`. `npm run lint` (tsc), `npm test` (vitest) and `make web`
  must stay green; the built SPA is embedded via `web/server/webui.go`.

## Things to remember

- Big (10k+ files) manifests are expected on day one — never plan an O(entire repo)
  DB write for an update path; default to incremental upsert + sweep.
- Revert is forward-only: one commit per target, history is never rewritten, and
  pending `revert_state` is cleared only on success/abort. It reuses existing
  queries and added no SQL, sqlc, or schema changes.
- Do not commit secrets, config.yaml (gitignored, sample is `config.yaml.sample`).
- Added/edited tests should run under `go test ./...` with `go vet ./...` clean.
- If you change `opencode.json`, agents, or skills, tell the user to restart
  opencode after saving.
- Don't put too much comment on the code. Avoid to add comments as much as possible