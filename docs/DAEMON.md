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
tests) and atomically writes:

```
~/.config/nipa/daemon.json   // {pid, port, token, version}   perms 0600
```

Client discovery: read file → check `pid` alive → dial → `Ping`. A stale file
(pid gone or dial refused) means "no daemon"; the GUI auto-spawns
`nipa serve` and waits for `Ping` readiness before showing any workspace.

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
| `Status` | unary | full `WorkingCopy.Status` result (staged/deleted/modified/untracked/missing/conflicts); `no_cache` passthrough |
| `Stage` | unary | add/remove path lists → returns fresh status |
| `Update`, `Switch` | **server-streaming** | progress events → terminal result |
| `Push` | **server-streaming** | message → progress → result (commit id/tree hash) |
| `Merge`, `Revert` | **server-streaming** | incl. continue/skip/abort variants; conflicts returned as terminal event |
| `Diff` | **server-streaming** | unified/stat/name output paged as bytes (reuse `usecase.Diff` renderers) |
| `Login` | unary | (host, user, password) → `securestorage`; **no prompts** in daemon |
| `Shutdown` | unary | drain + exit |

### Proxy ops (unary pass-through to the server)

Phase A scope: branches (list/create/delete/protect info), `GetTreeManifest`,
`GetCommitLog`/`GetCommit`/`WalkCommits`/`GetMergeBase`, MR
list/create/merge/close + detail incl. review summary and threads, file locks
(lock/unlock/list). Phase B: PBAC/path permissions, groups, admin.

All proxy calls take the repo `root`, resolve `.nipa/config` → host/org/project,
and run on the daemon's warm, auto-refreshing client.

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
- **`Diff` gained a `context` field** (hunk context, 0 = default) and accepts
  `<a>..<b>` / `<a>...<b>` ranges daemon-side, matching the CLI.
- **Proxy requests are forwarded as-is** (e.g. `limit` is the caller's), with
  only the clone's org/project context filled in and the caller's context
  overwritten.
- **`WorkingCopy.RefreshStatEntries`** (`internal/client/usecase`) is the one
  addition outside `internal/client/daemon`; `localrepo.Init` now closes a
  previous handle and the DSN sets `busy_timeout`, since status and the
  reconciler share a handle.

Not yet implemented: Phase B proxies (PBAC/path permissions, groups, review
submission/thread writes, branch protection/default changes), idle-exit,
`StatusStream`, CLI passthrough via the daemon, and socket/named-pipe transport.

## Later

- Idle-exit timer for auto-spawned daemons.
- `StatusStream` live notifications.
- CLI passthrough (`nipa status --daemon`) reusing a running daemon — nice for
  big repos, not required for the GUI.
- Socket/named-pipe transport if the TCP+token model proves too exposed in
  hardened environments.
