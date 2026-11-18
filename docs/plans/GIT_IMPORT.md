<!--
Handoff notes for the implementing session:
- Design: docs/GIT_IMPORT.md. Read it first.
- Global rules: never commit to git; new code needs >=80% test coverage; keep comments minimal.
- Released project: every schema change is a NEW migration pair (sqlite 000025, postgres 000012
  at the time of writing; re-check the latest numbers), never edit existing migrations.
- Defaults assumed below (change the affected PRs if the user decides otherwise):
  origin-aware commit hash (no message trailer), project admin may import,
  automatic author match limited to verified org members, web import job deferred.
-->

# Git import: plan by PR

Every PR must leave `go build ./...`, `go vet ./...`, `make lint` and
`go test ./...` green, with ≥80% coverage of the new code. PRs touching
`web/` or `desktop/` also keep `npm run lint`/`npm test` (and
`make desktop-test`) green.

```
PR1 schema ─► PR2 hash+ApplyImport ─► PR3 usecase ─► PR4 RPCs ─┬─► PR5 read surfaces (server/CLI/MCP)
                                                               │      └─► PR6 web + desktop
                                                               └─► PR7 core planner ─► PR8 converter
                                                                          └─► PR9 client sink+state ─► PR10 CLI + suite
                                                                                   └─► PR11 LFS ─► PR12 remote URLs
PR13 docs (after PR10; updated again after PR12)
```

PR5/PR6 can run in parallel with PR7–PR9.

---

## PR1: commit origin and author columns

**Goal:** the storage for imported commits, with no behavior change.

- Migrations `000025_commit_origin` (sqlite) / `000012_commit_origin` (postgres):
  nullable `author_name`, `author_email`, `origin` on
  `commits`, plus the partial unique index `commits_project_origin
  (project_id, origin) WHERE origin IS NOT NULL`. The down migrations drop
  them. No `authored_at` column: imported commits store the author date in
  `created_at`.
- `domain.Commit`: `AuthorName`, `AuthorEmail` and `Origin string`. Map them in the sqlite and postgres branch repositories
  wherever commits are read (`GetCommit`, `GetCommitByHash`, the log/walk
  queries).
- New queries in both dialects: `CommitListByOrigins(project_id, origins[])`.
  Then `make sqlc`.

**Tests:**
- migration up/down on both dialects;
- the repository round-trip of the new fields (NULL for native commits);
- `CommitListByOrigins` scoped to the project.

**Done when:** existing tests pass unchanged and the new columns are readable.

## PR2: origin-aware commit hash and `ApplyImport`

**Goal:** write an imported commit without moving any branch.

- `treehash.CommitHashWithOrigin(tree, parents, message, origin)`: identical
  to `CommitHash` when the origin is empty. Golden tests pin the existing
  hashes.
- `usecase.ApplyPushRequest` gains `Origin`, `AuthorName`, `AuthorEmail` and
  `CreatedAt *time.Time` (nil = now; imports pass the author date).
  `BranchID` becomes optional (zero = do not move a head).
- `PushRepository.ApplyImport(ctx, req) (existing *domain.Commit, err)` in
  `internal/repository/sqlite/push_repository.go` and
  `ee/repository/postgres/push_repository.go`:
  - the same `pushbatch` path as `ApplyPush`, with no branch update;
  - an origin conflict returns the stored commit instead of an error;
  - a missing chunk link keeps the `missing chunk data` error.
- Regenerate the mocks.

**Tests:**
- repository tests in both dialects: a root commit, a child commit with a
  delta, a merge with parent 2, a duplicate origin (idempotent), and a
  missing chunk;
- confirm that branch heads stay untouched.

## PR3: `usecase.Import`

**Goal:** the business rules, gomock-tested.

- Extract the shared parts of `Push.Push` into helpers:
  - validation (`validatePushFiles`, `validateRemovedFiles`, the file-hash
    check, `ensurePathWrites`);
  - `buildNewTree` from a given parent commit;
  - `buildApplyRequest`.

  Push behavior stays identical (covered by the existing push tests).
- `internal/usecase/import.go`:
  - `Lookup(projectID, origins)`: read permission and project-scoped.
  - `Commit(projectID, ImportCommit)`:
    - requires project admin;
    - `Chunk.RequireWritable` gate;
    - at most two parents, each resolved by origin in this project (unknown →
      400);
    - origin validation per docs/GIT_IMPORT.md "Origin format": a small
      `domain.ParseOrigin` with a system registry (`git`, `hg` hex ids;
      `svn`, `p4` decimal ids with a required qualifier), a 512-byte cap,
      and table tests for every system;
    - author fields capped at 255 runes;
    - author mapping: the explicit `UserID` from a map entry must be an org
      member, otherwise a verified-email match among org members, else the
      caller;
    - no locks, hooks or reviews.
  - `SetRefs(projectID, SetRefsInput{Branches, Tags, Default, Update})`:
    - the whole call runs under `LockProject` in one transaction;
    - creates missing branches and tags at the mapped commits;
    - in update mode, fast-forwards existing branches only when the head is an
      ancestor (bounded walk like `Branch.Contains`), otherwise reports
      `diverged`;
    - protected branches are reported `protected`;
    - sets the default branch;
    - returns a per-ref result;
    - emits one `push` webhook per moved branch (no file list) and
      `tag.created` per new tag;
    - records audit `project.git_imported` in `domain/audit_actions.go`.
- Add `Import` to `serverapp.BuildUsecases` and `usecase.Repositories` (the
  push repository is already there).

**Tests (gomock):**
- permissions (writer refused, admin ok);
- parent errors and the octopus refusal (>2);
- idempotent commit;
- each author mapping branch;
- SetRefs create/ff/diverged/protected/default;
- webhook and audit calls;
- read-only store refusal.

## PR4: gRPC RPCs and client transport

**Goal:** the wire protocol, end to end.

- `server.proto`: `ImportLookup`, `ImportStream` (bidi) and `ImportSetRefs`
  with the messages from the design (`PushStreamFiles`/`Chunks`/`Removed`
  reused). Then `make proto`.
- `internal/grpc/server/import.go`:
  - reuses the `push_stream.go` receive helpers and caps;
  - acks one `ImportStreamResponse` per `ImportCommitEnd`;
  - a usecase error terminates the stream with the mapped status.
- `maintenance_classes.go`: `ImportLookup` = read, the rest = write. Add the
  rate-limit entries. The class tests walk the ServiceDesc automatically.
- `internal/client/grpc/import.go`:
  - `ImportLookup`;
  - `OpenImport(ctx, org, project) (*ImportSession, error)` with
    `Send(commit)`/`Recv()`;
  - `ImportSetRefs`;
  - auth/refresh/rate-limit retry through `callAuthed` like `PushStream`; an
    older server answers `Unimplemented`, mapped to an upgrade hint.
- `internal/e2e/import_test.go`, against the in-process server:
  - upload chunks, stream 3 commits (root, child, merge), set 2 branches + 1
    tag;
  - clone each branch and compare the bytes;
  - re-stream the same commits (idempotent);
  - run on both sqlite (default) and postgres (ee e2e smoke).

## PR5: show imported authors (server, CLI, MCP)

**Goal:** imported metadata is visible on every non-UI surface.

- Proto:
  - `CommitLogEntry.origin`;
  - `CommitWalkEntry.author_name/email/origin`;
  - `CommitDetail.author_name/email/origin`.

  Server mapping prefers the author fields (`author_name` falls back to the
  user join as today).
- REST commit DTOs (`internal/http/model`) get the same fields. Update
  `web/src/api/endpoints.ts` types (fields only).
- CLI:
  - `nipa log` adds `(imported <sha7>)` (the date already comes from
    `created_at`);
  - `--json` gets `origin`;
  - `nipa blame` uses the author name.
- MCP `nipa_log` gets the same fields.
- Daemon proxy: no change (verbatim forwarding).

**Tests:**
- server mapping tests;
- CLI golden output (`log_test.go`, `blame_test.go`);
- an MCP log test;
- the REST handler test.

## PR6: show imported authors (web + desktop)

- Web history list, commit page and blame/file history: the imported author
  (dates need no change, they come from `created_at`), plus an "Imported from Git" label with a copyable SHA.
  vitest.
- Desktop: History rows and commit detail, the blame gutter
  (`lib/blame.ts`), and the activity feed. New strings go into every
  catalog in `frontend/src/i18n/locales`. Regenerate the bindings.

**Tests:** vitest for both frontends and the desktop service tests.

## PR7: `internal/gitimport` source + planner

**Goal:** a pure ordering of what to import.

- Add `github.com/go-git/go-git/v5`.
- `Source` over `git.PlainOpen` (bare or non-bare), with ref listing and
  peeled annotated tags.
- `Planner.Plan(Options{Refs, Depth, Since, Snapshot})` returns:
  - the ordered commits (parents first, Kahn's algorithm with committer-time
    ties);
  - parents trimmed to the window;
  - octopus warnings;
  - the ref → tip map and the default branch from `HEAD`.
- `Options.Known` (origins the server already has) prunes them and their
  ancestors for resume/update.

**Tests (go-git fixtures in `t.TempDir()`):**
- linear history, branches and a merge, an octopus, orphan roots;
- depth/since cut, snapshot;
- tag peeling and ref patterns;
- pruning by `Known`.

## PR8: `internal/gitimport` converter

**Goal:** turn a Git commit into a Nipa commit event.

- `Converter.Convert(commit)` returns an `Event{Origin, Parents, Author,
  Message, Files, Removed}`:
  - computes `DiffTree` against parent 1 (an empty tree for roots);
  - maps modes (`100644`/`100755`);
  - applies the symlink policy (`skip|file`) and skips submodules, both with
    warnings;
  - validates paths with the same rules as the server's `validateRepoPath`.
    Move that function to a shared pure package if needed, so client and
    server agree.
- `BlobCache` interface (memo by Git blob SHA → file hash, chunks, encoding,
  size, binary flag). An in-memory implementation for tests.
- Chunking: `chunker.ProbeBinary` + `ConfigForFile` + `Encode`, streamed
  from the blob reader, with a `ChunkSink` callback for the bytes (the
  uploader).
- `DryRun(plan)` returns a `Report` (counts, bytes, invalid paths, symlinks,
  submodules, octopus, empty commits).

**Tests:**
- deltas and removals;
- the exec bit;
- symlink skip/file;
- the submodule warning and invalid path errors;
- the blob memo hit (no second read);
- the encoding parity check: converter hashes equal `nipa push` of the same
  file;
- the dry-run report.

## PR9: client sink and state

**Goal:** connect the core to the server, resumably.

- `internal/client/gitimport/state.go`:
  - SQLite at `<git-dir>/nipa-import/state.db` (tables `blobs`, `lfs`,
    `meta`), implementing `BlobCache`;
  - an option mismatch with the stored `meta` target → error unless
    `--reset-state`.
- `internal/client/gitimport/sink.go`:
  - `Lookup` → `ImportLookup` (batched by 1000);
  - chunk upload through the existing `UploadChunks` windows (16 MiB, two
    uploaders);
  - `Commit` → `ImportSession` with depth-2 pipelining: upload for commit
    k+1 overlaps the apply of k;
  - `SetRefs` → `ImportSetRefs`.
- `internal/client/gitimport/run.go`: `Importer.Run(ctx, Options, Progress)`
  runs the plan, converts, uploads, commits, sets refs and returns a
  `Summary`. Cancellation is safe.
- `authormap.go`: mailmap-style parser (`Name <email> = <user-id>`).

**Tests:**
- a fake `Sink` and transport;
- resume after a failure injected at commit k (no re-chunk, no re-upload);
- `--update` with a new commit;
- diverged and protected ref results;
- the state option mismatch;
- the author map parse errors.

## PR10: `nipa import git` CLI + client suite

- `internal/client/cli/import.go`, registered in `root.go`:
  - `nipa import git <git-path> <nipa-url>` with `--refs`,
    `--default-branch`, `--depth`, `--since`, `--snapshot`, `--symlinks`,
    `--author-map`, `--source-key`, `--update`, `--reset-state`, `--dry-run`, `--json`,
    `--yes`;
  - `MakeSureLoggedIn`;
  - a confirmation prompt (non-TTY requires `--yes`);
  - progress via `cli/progress.go`;
  - a diverged or protected ref is a warning, not a failure: it is printed,
    listed in `--json`, and the command still exits 0 when everything else
    succeeded.
- `tests/suite/import_git_test.go` (both editions):
  - build a repo with `git` if available, else go-git;
  - dry-run, import, clone every branch and compare with `git archive`;
  - check log authors and dates;
  - add a Git commit and run `--update` (fast-forward);
  - push in Nipa then `--update` (diverged);
  - a non-admin is refused.

## PR11: Git LFS

- `internal/gitimport/lfs`:
  - pointer parser (spec v1, size cap on pointer files);
  - local store resolver;
  - batch API client (`download`, basic/bearer auth from `git credential
    fill` when `git` exists, `.lfsconfig`/`lfs.url`/remote-derived endpoint,
    retries, streaming SHA-256 verify).
- Converter: a pointer blob is resolved to the object stream before
  chunking. The memo is keyed by OID, so the same object under two pointer
  blobs is converted once.
- `--lfs auto|local|off`, plus the dry-run count of missing OIDs.

**Tests:**
- an `httptest` LFS server: ok, 401 → credential retry, a corrupt object, a
  404 OID, and a transfer error retry;
- local store hit;
- `off` keeps the pointer.

## PR12: remote Git URLs

- A `<git-path>` that is a URL (`https://`, `ssh://`, `git@host:path`,
  `file://`) is mirrored with go-git (`PlainClone` `Mirror: true`, then
  `Fetch` on later runs) into `<user cache>/nipa/git-import/<sha256(url)>`,
  where the state DB also lives.
- Auth: credentials in the URL, the credential helper, or the SSH agent and
  default keys.
- `--keep-cache=false` removes the mirror after a successful run.

**Tests:**
- a `file://` remote fixture;
- fetch on `--update`;
- an auth error is surfaced with a hint;
- a suite case over `file://`.

## PR13: docs

- `docs/GIT_IMPORT.md`: change the status line to implemented and note any
  deviations.
- `AGENTS.md`: add a "Flow for `nipa import git`" paragraph and the new
  RPCs/columns.
- `docs/CLIENT_GUIDE.md`: a migration how-to:
  1. create the project;
  2. `git lfs fetch --all`;
  3. dry-run;
  4. import;
  5. mirror with `--update` until cutover;
  6. protect the branches.
- `docs/WEBHOOKS.md`: imported pushes have no file list.
- `docs/AUDIT_LOG.md`: the `project.git_imported` action.

---

## Later (separate design): import from the web UI

A server job reusing `internal/gitimport` with an in-process sink: an
`import_jobs` table, progress, cancellation, encrypted remote credentials, a
scratch disk limit and per-org concurrency, plus an "Import from Git" step in
new-project creation. It needs its own design for SaaS disk and abuse limits.
