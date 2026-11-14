# Nipa Client Daemon (`nipa serve`)

Design for the local `nipa` daemon: a long-lived process that every GUI surface
(desktop client, Windows Explorer, game-engine plugins) talks to instead of
spawning the CLI per event. It reuses the exact usecase graph the CLI already
builds — there is no second implementation of VCS logic, only a host for it.

**Scope decisions locked in** (see `docs/INTEGRATIONS.md`):
- Loopback **TCP + capability token file** (no Unix sockets/named pipes in v1).
- **Synchronous server-streaming RPCs** for long operations (no operation registry).
- **One daemon process, N clone roots** (not one process per clone).

---

## Non-goals

- No virtual/VFS checkout layer — the working copy stays materialized on disk.
- No serving of other machines: loopback only, single-user.
- No new VCS semantics: status, staging, push, update, switch, merge, revert and
  diff are the *existing* usecase code, just hosted.
- The CLI keeps its own process model; `nipa serve` is additive.

---

## Architecture overview

```
nipa-gui / Explorer / engine plugin ──(loopback gRPC + daemon token)──► nipa serve
                                                                        │  per registered root:
                                                                        │   ├ localrepo handle (cached)
                                                                        │   ├ fsnotify watcher (dirty set)
                                                                        │   └ op coordinator (mutex)
                                                                        │  shared:
                                                                        │   ├ grpc.Transport + Session (per host)
                                                                        │   ├ securestorage keyring (exclusive owner)
                                                                        │   └ usecase registry (Repo/Push/Update/Merge/…)
nipa serve ──(server gRPC + presigned HTTP)──► nipad
```

- `cmd/nipa` gains a `serve` subcommand. Its `main()` wiring is a copy of the
  CLI's construction (`usecase.NewAuth/NewPush/NewUpdate/...` over
  `grpc.NewClient` + `usecase.NewSession`), kept alive across calls instead of
  exiting after one command.
- The daemon **proxies remote operations**: branches, MRs, reviews, locks,
  commit history and tree manifests go through the daemon's warm transport, so
  GUI clients never speak the server protocol.
- Each registered root gets a lazy `localrepo.NewLocalRepo()` (SQLite handle),
  a watcher, and an op coordinator. Handles are closed on unwatch/shutdown.

---

## Lifecycle & discovery

### Endpoint + token (`daemon.json`)

On start the daemon binds `127.0.0.1:<random>` (`NIPA_DAEMON_PORT` pins it for
tests; `NIPA_DAEMON_EVENT_INTERVAL` shortens the `WatchEvents` poll) and
atomically writes:

```
~/.config/nipa/daemon.json   // {pid, port, json_port, token, version}   perms 0600
```

Client discovery: read file → check `pid` alive → dial → `Ping`. A stale file
(pid gone or dial refused) means "no daemon"; the GUI auto-spawns
`nipa serve` and waits for `Ping` readiness before showing any workspace.

### JSON bridge (`json_port`)

Clients that cannot speak gRPC over h2c (Unity's Mono `HttpClient`, Godot,
Unreal) use a second loopback listener, `127.0.0.1:<json_port>`
(`nipa serve --json-port`, `NIPA_DAEMON_JSON_PORT`; 0 picks a free port).
Older readers ignore the new `json_port` field.

The bridge (`internal/client/daemon/jsonbridge.go`) is generic: routes come
from the `NipaDaemon` service descriptor, so every RPC, including future
ones, is exposed without per-method code. `POST /v1/<Method>` takes the
request message as `protojson` (empty body = empty message) and invokes the
daemon's own gRPC listener, forwarding `x-nipa-daemon-token` so the gRPC
interceptors stay the single auth point.

- Unary RPCs answer `200 application/json`; server streams answer
  `200 application/x-ndjson`, one message per line flushed per event. A client
  disconnect cancels the RPC (and so a running `Update`/`Push`). A stream
  failing after the first message ends with a `{"error":{"code","message"}}`
  line. Client-streaming RPCs are not exposed.
- Messages use proto field names (`UseProtoNames`) and emit unpopulated fields.
- Errors map the gRPC code to an HTTP status (`InvalidArgument` 400,
  `Unauthenticated` 401, `PermissionDenied` 403, `NotFound` 404,
  `FailedPrecondition`/`Aborted`/`AlreadyExists` 409, `ResourceExhausted` 429,
  `Unimplemented` 501, `Unavailable` 503) with body `{"code","message"}`.
- A non-loopback `Host` or `Origin` is rejected with 403 (DNS-rebinding
  guard); non-POST is 405, an unknown method 404.

### Auth

- Every RPC must carry `x-nipa-daemon-token` metadata, verified
  **constant-time** against the token in `daemon.json`. This is a *local
  capability*, deliberately a separate header from the server's
  `authorization: Bearer` so the two token classes cannot be confused.
- The user's *server* session stays keyring-backed via `securestorage`, which
  the daemon owns exclusively once running. `Session.reauth`'s interactive
  prompt path is never used here: a 401/refresh-rejection from the server is
  surfaced as an RPC error, the GUI renders a login dialog, and
  `daemon.Login(host, user, password)` stores the result via the normal
  `securestorage.SaveToken` path.

### Shutdown

- `daemon.Shutdown` RPC or `SIGTERM`.
- Grace period (default 10s): in-flight exclusive ops are allowed to finish;
  no new ops are admitted; all `localrepo` handles close; `daemon.json` is
  removed last so discovery never half-succeeds.
- No idle-exit in MVP. An idle-exit timer (auto-spawned daemons dying after
  N idle hours) is listed as later work — it must not fire while a watcher is
  registered.

---

## Repo registry & op coordination

`WatchRepo(root)` validates `.nipa/config` exists and registers the clone:

| Concern | Mechanism |
|---------|-----------|
| Root → handle | Lazy `localrepo` cache; ref-counted; `UnwatchRepo`/shutdown closes |
| Serialization | Per-root coordinator: update/push/switch/merge/revert take an **exclusive** slot (queued FIFO, not rejected); status/stage/diff/lock-list go **shared** |
| Cancellation | Op context derives from the streaming RPC's context — cancelling the stream cancels the op |
| Two clones | Fully independent coordinators; only keyring/transport are shared |

Queued exclusive ops stream a `queued` event so the UI can show "waiting for
the running update…". Watch out: not every hash loop checks ctx yet
(`WorkingCopy.rehash` does not); cancellation is best-effort in v1 and a
follow-up to tighten.

---

## RPC surface (`internal/client/grpc/proto/daemon.proto`)

`daemon.proto` imports `server.proto` (include path = module root) and reuses
`TreeManifest`/`FileNode`/`Branch`/`CommitLogEntry` types verbatim; generated
pb goes to `internal/client/grpc/daemonpb`. `make proto` gains a second
`protoc` invocation.

### Local working-copy ops

| RPC | Shape | Notes |
|-----|-------|-------|
| `Ping` | unary | readiness + version; discovery handshake |
| `WatchRepo` / `UnwatchRepo` / `ListRepos` | unary | validate + register; returns config (url, branch, sparse) |
| `Status` | unary | full `WorkingCopy.Status` result (configured branch + detached `head` marker, staged/deleted/modified/untracked/missing/conflicts, and `added`: the staged paths the snapshot does not track yet); `pending` is `merge` or `revert` while one waits for continue/abort, `pending_remaining` the revert targets left (current one included); `no_cache` passthrough |
| `Stage` | unary | add/remove path lists → returns fresh status |
| `Restore` | unary | discard unpushed changes for files/dirs → returns fresh status; staged paths refused |
| `Update`, `Switch` | **server-streaming** | progress events → terminal result; `Switch` takes an optional `tag` to detach HEAD at a release tag instead of switching branches |
| `Push` | **server-streaming** | message → progress → result (commit id/tree hash) |
| `Merge`, `Revert` | **server-streaming** | incl. continue/skip/abort variants; conflicts returned as terminal event |
| `Conflicts` | unary | the waiting merge or revert: `op`, the base/ours/theirs revisions with labels (merge: merge base, target branch, source branch; revert: the reverted commit, the branch head, its parent) and each conflicted file's kind (`text`, `binary`, `add_add`, `modify_delete`, `delete_modify`), binary flag, viewer kind and `resolved` flag; empty `op` when nothing waits |
| `ResolveConflict` | unary | settles one conflicted file (`ours`/`theirs` = that side's committed version, a side without the file deletes it; `content` = given bytes; `working` = keep the disk file; `delete`), stages it, records it in the pending state's `resolved` list and returns the status (which carries `resolved`). Text written or kept must have no `<<<<<<< ours`/`>>>>>>> theirs` lines |
| `MergeGodotResource` | unary | read-only Godot-aware three-way merge of one conflicted `.tscn`/`.tres` of the waiting merge or revert: loads the base/ours/theirs versions named by `Conflicts` and runs `internal/godotres.Merge3`; returns `merged` bytes, `clean` and `conflicts` (`section`, `key`, `message`). A clean result is applied with `ResolveConflict{choice: content}`; with conflicts the bytes are only ours-wins and must not be written. Errors: not a `.tscn`/`.tres`, nothing waiting, no conflict on that path, a binary conflict, a parse error. A side that deleted the file yields one conflict |
| `Diff` | **server-streaming** | unified/stat/name output paged as bytes (reuse `usecase.Diff` renderers) |
| `DiffFiles` | **server-streaming** | same request as `Diff` (format ignored; `show` with one revision compares it with its first parent, or an empty tree for a root commit — the GUI's commit view); one structured `DiffFile` per path: status, viewer `kind` (`internal/preview.Classify`), old/new side metadata (mode, size, binary, hex hash), add/remove counts and `greet.DiffHunkDetail` hunks — omitted with `hunks_omitted` above 1 MiB of hunk text per file |
| `ReadBlob` | **server-streaming** | one file version (`BlobRef`: `working` default, `snapshot` = last synced side of a working diff, offline, or `revision` + branch/tag/commit/HEAD); `BlobInfo` (size, binary, hex hash, kind) then decoded content in 64 KiB chunks; revision chunks are downloaded into `.nipa/objects` first. Paths are repo-relative, `.nipa/` and symlinks leaving the clone are refused |
| `Preview` | **server-streaming** | `old`/`new` `BlobRef`s + optional texture `level`/`face` → per side `ImagePreview` (dims, format, encoding, depth, alpha, levels/faces/cube and the decoded level/face, PSD `layers`, `hdr`) + downscaled PNG chunks (`max_size`, default 2048; HDR sides are `preview.PackHDR` RGBE PNGs twice as wide), then `ImageDiffStats` from `preview.CompareImages` when both sides are given; non-image or unsupported formats fail with 415 and the decoder's reason |
| `WatchEvents` | **server-streaming** | team activity for one watched root, polled from the server every `interval_seconds` (default 60, clamped to 15–3600; `NIPA_DAEMON_EVENT_INTERVAL` overrides it for tests): `incoming` (commits on the clone's branch it does not have, up to 100), `review_requests` (open merge requests with a pending review request for the caller) and `events` since the previous poll (`pushed` with author/message/count, `lock_released` for someone else's lock, `lock_requested` when a teammate asked the caller to release a lock they hold (first request or one that notified the holder again), `review_requested`). The first poll is a baseline without events; later ones are sent only when something changed. A failed poll sends `error` once and keeps the counts. Ends on unwatch or shutdown |
| `WatchChanges` | **server-streaming** | change notices for one watched root: the watcher batch's paths after the stat-cache refresh, or no paths (= refresh everything) after an operation, a stage/restore, or more than 512 coalesced paths. Headers are sent once subscribed; never pins the repo, ends on unwatch or shutdown |
| `ListFiles` | unary | one directory level of the clone (snapshot rows of that level + working-copy status, untracked files and folders included); see below |
| `SearchFiles` | unary | tracked files whose path holds the query's characters in order, ranked; see below |
| `RestoreRevision` | **server-streaming** | writes a file as it was in a commit over the working file under the exclusive slot; see below |
| `AssetRefs` | unary | what an engine asset uses and which files use it, from a reference graph cached per root; see below |
| `Provenance` | unary | origin record, status and `required` flag per path; see below |
| `SetProvenance` | unary | records a file's origin by hand like `nipa provenance set` and stages `.nipaprovenance.yml`; see below |
| `Blame` | **server-streaming** | which commit last changed each line of a text file (`nipa blame`); progress, then the result; see below |
| `ShelveCreate` | **server-streaming** | stores the given changes (all when `paths` is empty) in a new local shelf and puts those files back, under the exclusive slot; see below |
| `ShelveApply` | **server-streaming** | merges a shelf into the working copy (`keep` keeps it), or with `abort` stops an apply waiting on conflicts; see below |
| `ShelveList` / `ShelveDrop` / `ShelveFiles` | unary | the clone's shelves (and the one being applied), delete one, a shelf's changes as `DiffFile`s |
| `SyncPlan` | unary | local changes an update, branch switch or tag checkout would replace; see below |
| `Login` | unary | (host, user, password, otp?, mfa_challenge?) → `securestorage`, or `mfa_required` + challenge; **no prompts** in daemon |
| `Shutdown` | unary | drain + exit |

### Proxy ops (unary pass-through to the server)

Phase A scope: branches (list/create/delete/protect info), release tags
(`ProxyTagList`/`ProxyTagCreate`/`ProxyTagDelete`), `GetTreeManifest`,
`GetCommitLog` (with `path`: only the commits that changed a file or folder)/
`GetCommit`/`WalkCommits`/`GetMergeBase`, MR
list/create/update/merge/close/reopen + detail incl. review summary, reviews,
threads, commits, checks and timeline, comments (`ProxyMergeRequestAddComment`/
`Reply`/`Resolve`) and review submission, file locks
(lock/unlock/list). Phase B: PBAC/path permissions, groups, admin.

All proxy calls take the repo `root`, resolve `.nipa/config` → host/org/project,
and run on the daemon's warm, auto-refreshing client.

Server-scoped calls work before any clone exists. They take a `server`
(host, host:port or http/https URL, normalized by `ParseEndpoint`) and run on
a fresh connection per call (`Options.Accounts.Connect`): `AccountStatus`
(is a session stored for the host; email and account id from the token),
`Login` (password; the connection to the server is opened first, the session
is stored under the host), `LoginSSH` (registered key from ssh-agent or
`~/.ssh`, no passphrase prompt; an optional `key_path` pins exactly that key,
unlocked with the optional `passphrase`, with no fallback to other keys — the
passphrase lives only in the request and is never logged), `ListProjects` (server `ListMyProjects`),
`RemoteBranches` and `RemoteTree` (server `ListDirectory`: one directory's
subfolders plus its file count and bytes). `Clone(url, branch, sparse,
target)` streams `OpEvent`s like the long operations but outside any repo
slot (target must be absolute and empty) and ends with a sync result read
from the new clone; `SetSparse(root, paths)` takes the root's exclusive slot,
stores the prefixes and runs an update so the working copy matches. The
daemon never prompts: a missing session fails with "sign in to this server
first".

`LockStatus(root)` combines proxy calls with the local status: it lists the
project's locks (`ListFileLocks`), resolves whether the configured branch is
the default (`GetBranchByName`: mainline locks apply, otherwise that branch's
locks), checks admin rights (`GetMyPermissions`) and reads the caller's
account id from the session's access token (JWT `sub`, unverified, used only
to tell "mine" from "theirs"). Each changed path that is a tracked binary or
has a lockable asset extension (`chunker.IsLockablePath`) gets a state:
`mine`, `theirs` (covered by someone else's file or directory lock),
`needed` (tracked, uncovered: the server's push gate would refuse it) or
`free`.

Lock pins: after the server grants `ProxyLockFile`, the daemon pins the
lock's path in the clone (`RepoOps.Pins`, `usecase.LockPins`, under the
root's exclusive slot): the path is recorded in `pinned_paths` and the cached
old version of the raw files it covers is recaptured from the working file or
downloaded, so desktop, Unity and Godot get offline compare and restore for
the files they lock. A pin failure never fails the lock; it comes back in
`ProxyLockFileResponse.warning`. `ProxyUnlockFile` unpins and prunes after the
server released the lock (a local failure is only logged).
`ProxyLockRequestRelease` forwards `RequestFileLockRelease` (ask the holder to
release a lock); the lock details in `LockStatus` and `ProxyListFileLocks`
carry the `requests` made on each lock.

Cache prune: `Diff`, `DiffFiles`, `ReadBlob`, `Preview` and `Restore` can
download snapshot content into `.nipa/objects` (the pristine fallback), but
the GUI reads the same file several times in a row, so they do not prune
inline. Each call instead arms the root's `cachePruner` (`prune.go`); every
coordinator admission restarts its quiet period (`Options.PruneQuiet`,
default 30 s), and when it elapses the pruner takes the exclusive slot only if
the root is idle (`tryAcquireExclusive`, never queued) and runs
`usecase.PruneCache`. A busy root, or a `repository busy` skip from another
process holding `.nipa/lock`, re-arms it; unwatch cancels it and waits for a
running prune.

---

## Progress streaming (`OpEvent`)

```
message OpEvent {
  oneof event {
    Queued   queued;      // exclusive slot held by another op
    Started  started;     // phase began
    Progress progress;    // objects done/total, bytes done/total, phase
    Result   result;      // terminal success payload (per-op message)
    Failure  failure;     // terminal domain.Error
  }
}
```

The daemon adapts the existing `usecase.DownloadProgress` /
`usecase.UploadProgress` callbacks onto a channel feeding the stream — the same
pattern the CLI's `progressRenderer` plugs into, so no usecase changes. A
stream disconnect cancels the op context; progress lost mid-flight is
acceptable (the usecases are idempotent up to their existing transactional
guarantees).

---

## Status pipeline

`Status` stays **read-through** `WorkingCopy.Status` — snapshot + staged set +
`stat_cache`, one bounded walk, rehashing only fingerprint misses. Crash/restart
safe, and identical semantics to the CLI (no forked status definition).

The watcher is an **accelerator, not the source of truth**:

1. Recursive watch per registered root (skip `.nipa`; pruned on dir removal).
2. Events are debounced (coalesce ~250–500 ms) into a per-root **dirty set**.
3. A background reconciler rehashes dirty paths (bounded workers) and writes
   `stat_cache` fingerprints via `SaveStatEntries`.
4. A `Status` call therefore usually pays only the stat walk; fingerprint
   misses still rehash on demand, so **event loss is tolerated** — the next
   status stat-detects anything the watcher missed.

On Linux/macOS, fsnotify needs manual per-directory watch management
(recursive add/remove helpers); on Windows, `ReadDirectoryChangesW`
(`fsnotify`'s recursive backend) covers it. `StatusStream` (server-streamed
status-change notifications for live GUI views) is deferred to Phase B; the
polling RPC is correct and fast without it.

---

## Errors, config, package layout

- Errors cross the wire already shaped like the server's: the CLI's
  `grpc.toDomainError` mapping (400/401/403/404/409) is reused in reverse —
  the daemon maps `domain.Error` back to gRPC codes, and clients re-map on
  receipt. Message strings pass through untouched.
- `~/.config/nipa/config.json` (`diffExternal`, `uploadWorkers`) is read once
  at daemon startup; per-op overrides are not supported in v1.
- New packages: `internal/client/daemon` (service, registry, coordinator,
  watcher, token/`daemon.json` plumbing) + `internal/client/grpc/daemonpb`
  (generated). `cmd/nipa` gains `serve`.

---

## Testing strategy

- **Unit** (`t.TempDir`): token file read/verify, registry registration/eviction,
  coordinator queueing + cancellation, status cache behavior (the existing
  `status_cache_test.go` patterns carry over).
- **Watcher**: behind an interface; tests inject a fake event source so no
  fsnotify flakes. A "watcher disabled" run asserts read-through status still
  detects everything (event-loss tolerance is a feature, not a fallback).
- **Integration**: daemon on a local listener driven end-to-end against an
  in-process `nipad` (mirroring `internal/e2e`): clone → watch → status →
  stage → push → update, plus exclusive-op queueing and stream-cancel behavior.
  Keyring is a fake `securestorage` backend injected in tests.
- Long-hash stress tests are skipped unless an env var is set, like
  `NIPA_BENCH_MB` benches.

---

## Implementation order

1. `daemon.proto` + codegen (`make proto` extension).
2. Token + `daemon.json` plumbing; `nipa serve` skeleton with `Ping`/`Shutdown`.
3. Repo registry + coordinator; `WatchRepo`/`Status`/`Stage`.
4. Progress adapter; `Update`/`Switch`/`Push`/`Merge`/`Revert` streaming.
5. Watcher + background reconciler.
6. Proxy RPCs (branches, tree, log/commits, MRs, locks).
7. e2e integration tests.

## Implementation status

Implemented (phases 1–7): `nipa serve` with token discovery, lifecycle and
drain; the repo registry, per-root coordinator and `Status`/`Stage`; streaming
`Update`/`Switch`/`Push`/`Merge`/`Revert`; streaming `Diff`; the fsnotify
watcher + background reconciler; and the Phase A proxy surface (branches, tree,
commits, merge base, MR reads/lifecycle, file locks). The client daemon lives
in `internal/client/daemon`, the proto in
`internal/client/grpc/proto/daemon.proto`, generated code in
`internal/client/grpc/daemonpb`.

Notes where the implementation settled details the design left open:

- **Per-repo client, not one warm transport.** Every watched root gets its own
  `RepoOps` graph and gRPC client (`serveRepoOps`): the client transport binds
  one host at a time, so sharing it across roots could cross-wire concurrent
  calls. The session/keyring is still shared.
- **Shutdown of a queued/running operation** cancels it and ends its stream
  with a terminal failure event (code 499); `GracefulStop` then waits for the
  handler to return.
- **The reconciler skips paths whose fingerprint `Status` would already
  trust**, so the accelerator follows Status's trust model exactly instead of
  being stricter (an update/push does not trigger redundant rehashing).
- **`ReadBlob`/`Preview` run through `DiffRunner.ReadBlob`** (`usecase.Diff.ReadBlob`,
  callback-style so `SerialDiff` holds its lock for the whole read) under the
  shared slot; content streams chunk by chunk (`blobReader`) and previews cap
  encoded input at 512 MiB on top of `preview.MaxPixels`.
- **`DiffFiles` is the GUI's diff feed** (`docs/DESKTOP.md`): it shares
  `runDiff` (validation, shared slot, `FilterIgnored`) with `Diff`, and reuses
  the server's hunk messages so desktop and web MR diffs share one shape.
- **`WatchChanges` is the GUI's live feed**: each repo owns a `changeHub`;
  publishing never blocks (each subscriber coalesces until it reads), the
  reconciler publishes its batch, `runOp` and `Stage`/`Restore` publish a full
  refresh. `Serve` now closes the stop channel before `GracefulStop`, so open
  streams end instead of blocking shutdown.
- **`Diff` gained a `context` field** (hunk context, 0 = default) and accepts
  `<a>..<b>` / `<a>...<b>` ranges daemon-side, matching the CLI.
- **Proxy requests are forwarded as-is** (e.g. `limit` is the caller's), with
  only the clone's org/project context filled in and the caller's context
  overwritten.
- **`WorkingCopy.RefreshStatEntries`** (`internal/client/usecase`) is the one
  addition outside `internal/client/daemon`; `localrepo.Init` now closes a
  previous handle and the DSN sets `busy_timeout`, since status and the
  reconciler share a handle.
- **Tag checkout** rides the existing `Switch` stream: `SwitchRequest.tag`
  routes to `UpdateRunner.SwitchTag`, `Status` carries the detached marker
  (`branch` + `head{kind,name}`), and `Update` while detached re-syncs the
  pinned commit. The manifest fetch uses `GetTreeManifest` with `commit_id`
  (project-scoped), so a sparse clone only receives the tagged set.

`MetaWarnings` lints the pending changes of Unity or Godot projects in the
clone from the daemon's own status plus snapshot blob reads (at most 256
sidecar reads per call), for the desktop app and the engine plugins.
`engine` picks the linter: `unity` (`internal/unitymeta`: `missing-meta`,
`orphan-meta`, `meta-not-staged`, `guid-changed`) or `godot`
(`internal/godotmeta`: `missing-sidecar`, `orphan-sidecar`,
`sidecar-not-staged`, `uid-changed` for `.import`/`.uid` files). Empty
detects `project.godot`, then `ProjectSettings/ProjectVersion.txt`, at the
clone root; with neither, both run and each only reports inside its own
projects. Paths are clone-relative; an unknown engine is `InvalidArgument`.

`PushPlan(root)` is the offline `nipa push --dry-run` report under the shared
slot: the staged changes from `usecase.Push.Plan` (`changes`, `up_to_date`,
`upload_objects`, `upload_bytes`) plus `assetscan.PushReport`'s
`asset_violations` (path, rule, limit, actual, max, message; `.nipaassets.yml`
from the working copy) and `asset_impact` (`broken` from/raw/target/reason,
`newly_unused`, `scanned`, `truncated`; absent when nothing broke). A budget
or impact failure is a `warnings` string (`budget check skipped: ...`,
`impact check skipped: ...`), never an RPC error; plan errors (nothing
staged, detached at a tag, mid revert sequence) are. It runs over its own
`Push` and `Diff` usecases (`RepoOps.PushCheck`, `NewPushChecker`, serialized
per root).

`Generate(root, kind, source, target)` drafts text with the configured
OpenAI-compatible endpoint (`internal/client/generate`, see
[AI.md](AI.md#generation---generate)): `kind` `commit` describes the staged
changes (offline), `mr` the merge base vs `source` diff plus the source
commits since the merge base (`source` empty = the clone's branch, `target`
empty = the project default branch; branches and the log come from the
server). It answers `{title, text, model, source, target}`: `title` is only
set for `mr`, `text` never carries the `Nipa-Generated` trailer (the caller
adds it where the text is used), and nothing is pushed or created. The input
is gathered under the shared slot through its own `Diff`/`Repo` usecases
(`RepoOps.Generate`, `SerialGenerator`); the model is asked after the slot is
released, so a slow endpoint never holds up a push. The endpoint, model and
key are resolved on every call (config file, `NIPA_LLM_*`, then the
`llm-api-key` keyring entry), so settings saved from the desktop app apply
without a restart. No endpoint or model is `FailedPrecondition` (`AI
generation is not configured: ...`); an unknown `kind` is `InvalidArgument`;
endpoint failures carry the redacted `llm` message.

`DiskUsage(root)` reports what a clone occupies under the shared slot:
`working_files`/`working_bytes` (one aggregate query over the snapshot's
`files` rows; the working tree is never walked, so edits not yet pushed are
not counted), `cache_objects`/`cache_bytes` (one pass over the
`.nipa/objects` fan-out directories; bytes include interrupted `.tmp-*`
writes, the count only hash-named objects), `pinned_paths` (lock pins) and
the clone's `sparse` prefixes.

`GC(root)` prunes the object cache like `nipa gc` (`usecase.PruneCache` on
the root's cached handle) and answers `{objects, bytes, skipped,
skip_reason}`. It queues for the root's exclusive slot like an operation
(cancelling the call leaves the queue), so it never runs beside a status,
diff or long operation of this daemon, nor beside the idle `cachePruner`,
which only fires when it can take the exclusive slot without waiting. A run
that was not skipped settles the pruner's pending request. The usecase rules
still apply inside the slot: `skipped` with `repository busy` when another
process holds `.nipa/lock`, and with `a merge or revert is in progress`
while either is pending.

`ListFiles(root, dir, name?)` lists one directory of the clone under the
shared slot for the desktop Files tab and the engine plugins: the snapshot
rows of that level (`localrepo.ListDir`: files by `tree_path`, sub-directories
by `tree_nodes.parent_path`, both indexed, so a 10k-file clone never loads its
whole snapshot) merged with the working-copy status. Each entry is `{name,
path, is_dir, size_bytes, is_binary, kind (preview.Classify), status, staged,
children_changed, tracked, hash, mtime_ns}`; `status` is empty, `modified`,
`added`, `deleted`, `missing`, `untracked` or `conflict`, `children_changed`
marks folders with anything changed below, and untracked or newly added files
(and folders that only exist on disk, `tracked` false) come from the status
too. Entries are folders first, then files, by name; `name` narrows the answer
to one entry (`NotFound` otherwise), and a `dir` with `..` or inside `.nipa`
is `InvalidArgument`. The status is the one the `Status` RPC last computed
for the root while no change notice was published since (the `WatchChanges`
hub's generation) and it is younger than 5 s; otherwise it is computed once
and cached the same way, so a listing costs the directory query plus a pass
over the changed paths. Locks are not part of the answer: callers join their
own lock list (the desktop uses `LockStatus`), so listing never calls the
server.

`SearchFiles(root, query, limit)` (shared slot, limit default 50, at most
200) finds tracked files for the desktop command palette: the query's
non-space characters must appear in the path in order, case-insensitively.
`localrepo.SearchPaths` prefilters the snapshot with one `LIKE '%c%h%a%r%'`
query (`%`, `_` and `\` escaped; non-ASCII characters become `_` because
SQLite folds case for ASCII only), capped at 20000 rows; the daemon then
ranks in Go: basename prefix, basename substring, basename subsequence, path
substring, path subsequence, then the tighter match window, the shorter path
and the path. `truncated` reports that the prefilter hit its cap. Hits carry
`{path, name, size_bytes, is_binary, kind}`; untracked files are not
searched and no server call is made.

`RestoreRevision(root, path, commit_id)` ("get this version") writes the file
as it was in a commit over the working file under the exclusive slot and
streams `OpEvent`s (phase `restore`, download progress, result `restore {path,
commit_id}`). `usecase.RevisionRestore` resolves the commit through
`GetCommit`, downloads the missing chunks commit-scoped to that path into the
object cache and writes the file through `materializeFile` (zstd chunks are
decoded; a read-only working file stays read-only, like update), then prunes
the cache. Nothing is staged: the file shows as modified (or untracked when
the path is not tracked any more). It refuses a pending merge or revert, a
path outside the sparse checkout, a folder, a commit that does not exist
(`revision "<id>" not found`) and a path the commit does not have (`<path>
does not exist in <id>`, 404).

`ShelveCreate(root, name, paths, include_untracked)` runs `usecase.Shelve`
(`.nipa/shelves/<id>/`: `manifest.json` plus the chunks in the shelf's own
`objects/`, written in a `.tmp-<id>` folder, fsynced and renamed, so the
cache GC never sees them and dropping a shelf removes a folder). The working
copy is touched only after the shelf is on disk: staged entries are
unstaged, tracked files restored from the snapshot (their content is made
available first, so an offline failure happens before anything is written)
and new files removed. Untracked files are taken when named exactly or with
`include_untracked`. Entries record the base version, whether the base text
was copied into the shelf, the staged/untracked flags and whether a lock pin
covered the path (display only). The result is `OpResult.shelve`.
`ShelveApply(root, id, keep)` refuses paths with local changes or outside the
sparse set, then runs the three-way core (`applyThreeWay`: base = the
shelved base, ours = the synced snapshot, theirs = the shelf, chunks read
from the shelf through an overlay), restores the staged flags and, on
conflicts, saves `shelf_state` (status `pending: shelf`; `Conflicts`
answers `op: shelf` with `theirs.id` = the shelf id; `ResolveConflict` reads
ours from the snapshot and theirs from the shelf, does not stage, and the
last resolution clears the state and drops the shelf unless `keep`).
`abort` puts every path the apply wrote back to the snapshot and keeps the
shelf. Merge, revert, push, shelve and apply refuse while `shelf_state` is
pending and the cache GC skips. `ReadBlob` takes `source` `shelf` (the
shelved file) and `shelf_base` (its base, from the shelf for text, else from
the base commit) with the shelf id as `revision`.

`SyncPlan(root, branch, tag)` fetches the manifest the operation would
materialize (`Update.TargetTree`: the tag's commit, the branch, or with both
empty the clone's branch or pinned tag commit) and returns
`usecase.OverwrittenPaths`: edited, deleted, new or staged files whose path
the target writes with a version other than the synced one; for a switch or
tag checkout every staged path too, since those refuse to run with them.

`Blame(root, path, revision)` (shared slot, serialized per root) streams
`BlameEvent`s: `progress {done, total}` per file version read, then one
`result` or `failure`. `usecase.Blame` blames the working file when
`revision` is empty (against the pinned commit, so lines the commit lacks
are `uncommitted`), otherwise the file as of a branch, tag, commit id or
`HEAD`. It lists the commits that changed the path through `GetCommitLog`
with `path` (the server scans at most 500 commits, first-parent), reads each
version newest first through a dir-scoped `GetTreeManifest` of that commit
(the root's own files, or the parent folder's subtree) and the
commit-scoped chunk download, and attributes lines with `diff.Lines`: a
line a version adds compared with the next older one belongs to that
version's commit. After the oldest listed commit it reads that commit's
first parent; when the file still exists there, the remaining lines are
older than the scan and stay without a commit (`truncated`). The walk stops
as soon as every line is attributed. Text files up to 2 MiB only: a binary
or larger blamed version is a 400, an older one ends the history like the
scan cap. The result is `{path, revision, lines[] {text, commit_id,
uncommitted}, commits[] {id, hash, author_name, author_email, message,
created_at}, truncated}` with the commits lines point at, newest first.
Chunks it downloads are pruned once the root is idle.

`AssetRefs(root, path)` answers `{path, asset, uses[], used_by[],
unknown[], scanned, truncated, partial}` under the shared slot. `uses` are
the references the file makes (`path`, `raw` as written, `resolved`; an
unresolved one names a target the clone does not have), `used_by` the
sources that point at it, `unknown` the references a sparse clone cannot
resolve, `truncated` that the scan hit its cap (5000 sources, 8 MiB per file)
and `partial` that the clone is sparse. A path that cannot take part in a
reference (`assetrefs.Relevant` is false: not a Unity/Godot source, sidecar,
Godot-importable file or file inside a Unity project) answers `asset: false`
without a scan. The graph (`assetscan.Graph` over `RepoOps.Refs`, its own
`Diff` usecase) is built on the first lookup and kept per root: concurrent
lookups wait for one build, and it is rebuilt only after a change notice
with no paths (operations, stage/restore, coalesced batches), a notice
naming a source, sidecar or other relevant path or `.nipaignore`, or after 2
minutes. The first lookup on a large project reads every engine source in
the working copy; later ones are a map lookup.

`Provenance(root, paths[])` (at most 1000 paths) answers `{enabled,
entries[]}` under the shared slot: `enabled` is whether the clone has a
`.nipaprovenance.yml`, and each entry carries `status` (`recorded`,
`modified`, `edited`, `missing`, from `provenance.Status` against the working
bytes), `required` (a `require_provenance` rule in the working copy's
`.nipaassets.yml` covers the path), `exists`, `has_record` and the record's
fields (`tool`, `model`, optional `seed`, `source`, `recorded_at` RFC 3339,
`license`, `human_edited`, `prompt_sha256`, `digital_source_type`, `note`).
Paths with neither a record nor a rule are left out, so a clone without both
files answers an empty list. The working file is hashed only for records
that carry a content hash.

`SetProvenance(root, path, tool?, model?, seed?, license?, human_edited?,
note?, prompt_sha256?)` runs `WorkingCopy.SetProvenance`, the core of `nipa
provenance set`: unset fields keep the record's value, the source becomes
`manual`, the content hash and time are refreshed from the working file,
`.nipaprovenance.yml` is created when missing (which opts the repository in)
and staged. It takes the shared slot like `Stage` and publishes a change
notice for `.nipaprovenance.yml`. The answer is the new entry plus
`created`. Only a prompt hash is accepted; the desktop hashes the prompt
before calling.

Not yet implemented: Phase B proxies (PBAC/path permissions, groups, review
submission/thread writes, branch protection/default changes), idle-exit, CLI passthrough via the daemon, and socket/named-pipe transport.

## Later

- Idle-exit timer for auto-spawned daemons.
- `StatusStream` live notifications.
- CLI passthrough (`nipa status --daemon`) reusing a running daemon — nice for
  big repos, not required for the GUI.
- Socket/named-pipe transport if the TCP+token model proves too exposed in
  hardened environments.
