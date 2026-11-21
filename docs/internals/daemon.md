# Client daemon internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

Flow for `nipa serve` (client daemon, design in `docs/DAEMON.md`):
`cmd/nipa/serve.go` builds `daemon.NewServer` (proto
`internal/client/grpc/proto/daemon.proto`, generated
`internal/client/grpc/daemonpb`) and publishes `~/.config/nipa/daemon.json`
(`{pid, port, token, version}`, 0600, atomic write); every RPC must carry the
`x-nipa-daemon-token` metadata (constant-time compare), and `Shutdown`/SIGTERM
drain in-flight operations before removing the file. `WatchRepo(root)` resolves
any path inside a clone to its root (up to 32 parents), validates
`.nipa/config` and pins a per-root entry: cached `localrepo` handle +
`usecase.WorkingCopy`, watcher/reconciler, coordinator and a `RepoOps` graph
(`serveRepoOps` builds one usecase set and one gRPC client per root, since the
client transport binds one host at a time). `Status`/`Stage` are read-through
`WorkingCopy` calls under the shared coordinator slot (`Status` reports the
configured branch and the detached `head` marker); `Update`/`Switch`/
`Push`/`Merge`/`Revert` take the exclusive slot (FIFO, queued exclusive blocks
later shared waits) and stream `OpEvent`s (queued/started/progress/result/
failure; cancelling the stream always terminates it with a failure event).
`Switch` detaches at a tag when its request carries `tag`
(`UpdateRunner.SwitchTag`), otherwise switches branch.
`Diff` streams rendered patch/stat/name output (64 KiB chunks, file-by-file for
patches) under the shared slot (`show` + one revision diffs a commit against
its first parent, `DiffOptions.Show`); `DiffFiles` runs the same request but streams
one structured `DiffFile` per path (status, `internal/preview` viewer kind,
side metadata, counts, `greet.DiffHunkDetail` hunks capped at 1 MiB per file)
for the desktop GUI. `ReadBlob` streams one file version (working copy,
offline `snapshot`, or a `revision` whose chunks are downloaded first) through
`usecase.Diff.ReadBlob`, and `Preview` decodes old/new images with
`internal/preview` into downscaled PNG chunks plus pixel-diff stats (415 for
non-image or unsupported formats). `WatchChanges` streams coalesced change
notices per root (watcher batch paths after the stat refresh; empty = refresh
all after ops and stage/restore) for the GUI, and `Status` reports `added`
(staged paths the snapshot does not track). An fsnotify watcher debounces events into
dirty batches and a background reconciler refreshes `stat_cache` through
`WorkingCopy.RefreshStatEntries`; watcher failure or event loss is harmless
(read-through status still stat-detects). `Proxy*` RPCs forward `greet.*`
messages verbatim over `Client.ServiceClient()`, filling the clone's
org/project context from `.nipa/config`; server-scoped `AccountStatus`/`Login`/`LoginSSH`/`ListProjects`/
`RemoteBranches`/`RemoteTree` run on a fresh connection per call
(`Options.Accounts`, wired in `cmd/nipa/serve.go`; the daemon never prompts),
`ProxyTagList`/`ProxyTagCreate`/`ProxyTagDelete` forward the tag RPCs,
`ProxyResolveIssues`/`ProxySearchIssues` forward the issue tracker reads
(`proxy_issue.go`; commit, branch and merge request payloads already carry
`issue_keys`),
`ProxyMergeRequestUpdate`/`Commits`/`Checks`/`Timeline`/`AddComment`/`Reply`/
`Resolve` complete the merge-request surface, `WatchEvents` polls the server per root (branch log vs the pinned commit,
lock list, open merge requests' review requests) and streams `RepoActivity`
with teammate `pushed`/`lock_released`/`review_requested` events,
`Conflicts`/`ResolveConflict`
(`usecase.Conflicts` over the merge/revert state's `conflict_kinds`,
`conflict_binary` and `resolved` fields, filled by `applyThreeWay`) list and
settle conflicted files for the GUI, `Status`
reports a waiting merge/revert as `pending` (+ `pending_remaining`),
`Clone` streams `OpEvent`s outside any repo slot and `SetSparse` stores the
prefixes then updates under the exclusive slot; `LockStatus` joins the lock list,
the branch scope (default branch = mainline locks), admin rights and the
caller id (`Client.UserID`, the access token's JWT `sub`) with the status into
per-file `mine`/`theirs`/`needed`/`free` states for changed binaries; `UnwatchRepo` waits for in-flight refs
before closing handles.

JSON bridge (design in `docs/DAEMON.md`): `Server` also binds a second loopback
listener (`--json-port` / `NIPA_DAEMON_JSON_PORT`, published as `json_port` in
`daemon.json`) served by `jsonbridge.go`, a reflection-driven HTTP/1.1 adapter
for engine plugins that cannot speak gRPC (Unity first): `POST /v1/<Method>` protojson-decodes the body
and invokes the daemon's own gRPC listener with the forwarded
`x-nipa-daemon-token` (unary = JSON, server streams = NDJSON, errors mapped to
HTTP statuses, non-loopback `Host`/`Origin` refused). `MetaWarnings`
(`daemon/metawarnings.go` over `internal/unitymeta` / `internal/godotmeta`,
`engine` = `unity|godot`, empty detects `project.godot` then
`ProjectSettings/ProjectVersion.txt`) is the sidecar lint RPC the desktop
service and engine plugins call. `PushPlan` (`daemon/pushplan.go`, shared
slot) returns the push plan's changes with `assetscan.PushReport`'s
`asset_violations` and `asset_impact` (broken, newly_unused, truncated);
budget/impact failures become `warnings`, never an RPC error. It runs over
`RepoOps.PushCheck` (`NewPushChecker`: its own `Push` + `Diff` usecases,
serialized). `Generate` (`daemon/generate.go`, `kind` = `commit|mr`,
optional `source`/`target`) gathers the `aicontext` input under the shared
slot over `RepoOps.Generate` (`SerialGenerator` around `serveGenerator`: a
`generate.Generator` with its own `Diff`/`Repo` usecases and the root's
client as `Connector`), then asks the model outside the slot and answers
`{title, text, model, source, target}` without the trailer; the model is
resolved per call (no restart after a settings change) and a missing
endpoint/model is `FailedPrecondition`. `DiskUsage` (`daemon/storage.go`,
shared slot) answers `localrepo.DiskUsage`: snapshot file count and bytes
from one `SnapshotUsage` aggregate query (no working-tree walk), one pass over
`.nipa/objects` for cache bytes and object count, the lock pins and the
sparse prefixes. `GC` runs `usecase.PruneCache` on the root's cached handle
under the exclusive slot (queued like an operation) and returns `{objects,
bytes, skipped, skip_reason}`; a completed run settles the `cachePruner`'s
pending request. `ListFiles` (`daemon/files.go`,
shared slot) lists one directory: `localrepo.ListDir` rows merged with a
status cached per root (`repo.cachedStatus`: reused while the change hub's
generation is unchanged and younger than 5 s; the `Status` RPC refreshes it)
plus untracked/added files and on-disk-only folders; no lock data and no
server call. `RestoreRevision` (exclusive slot, `OpEvent` stream, result
`restore`) runs `RepoOps.Restore` (`usecase.RevisionRestore`: commit-scoped
download of one file of a commit, `materializeFile` over the working file,
nothing staged; refuses pending merge/revert, paths outside the sparse set,
folders, unknown commits or paths). `AssetRefs`, `Provenance` and
`SetProvenance` (`daemon/assetrefs.go`, `daemon/provenance.go`, shared slot)
serve the desktop where-used and origin panels; see the asset references and
provenance flows. `Blame` (`daemon/blame.go`, shared slot, `SerialBlame`
over `RepoOps.Blame` = `usecase.Blame`) streams `progress` per version read
and then the `result` (lines with `commit_id`/`uncommitted`, the commits
they point at, `truncated`). `LoginSSH` takes an optional `key_path` +
`passphrase` (exactly that key, no fallback, never logged). Shelves
(`daemon/shelve.go` over `usecase.Shelve`, flow below): `ShelveCreate`/
`ShelveApply` (exclusive slot, `OpEvent`, results `shelve`/`shelve_apply`,
`abort` on apply), `ShelveList`/`ShelveDrop`/`ShelveFiles` (shared slot);
`SyncPlan` (shared slot) answers `usecase.OverwrittenPaths` over
`Update.TargetTree` (`UpdateRunner.TargetTree`) and a fresh status.

