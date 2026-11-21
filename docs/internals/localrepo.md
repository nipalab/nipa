# Client localrepo internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

## Client localrepo design (`internal/client/localrepo`)

- `.nipa/` inside the clone target: `config` (json `{url, branch, sparse?,
  head?}`; `head` is the detached marker `{"kind":"tag","name":"v1.0.0"}` and
  is absent while on a branch) written
  atomically via `atomicWrite`, `nipa.db` (SQLite, `modernc.org/sqlite`,
  `_pragma=foreign_keys(ON)` in the DSN — cascades silently don't fire otherwise),
  `objects/` with Git-style loose chunk objects, and `shelves/` (one folder
  per local shelf, outside the object cache).
- Schema (in `schema/schema.sql`, embedded via `//go:embed`, also the sqlc source):
  - `tree_nodes(path TEXT PRIMARY KEY, parent_path, hash BLOB, mode)`
  - `files(path TEXT PRIMARY KEY, tree_path FK->tree_nodes ON DELETE CASCADE, hash,
    size_bytes, mode, is_binary, encoding, chunks BLOB)` — `chunks` is
    the concatenated 32-byte hashes of the file's chunks (metadata only, no
    content); `encoding` is `raw` or `zstd` and refers to the stored chunk bytes.
  - `meta(key PK, value)` — stores `tree_hash`, the pinned `commit_id` /
    `commit_hash`, and the pending `merge_state` / `revert_state` /
    `shelf_state` JSON.
  - `staged_files(path PK, staged_at)`.
  - `pinned_paths(path PK, pinned_at)` — lock pins (`PinPaths`/`UnpinPaths`/
    `ListPins`), stored normalized like snapshot paths
    (`NormalizePathPrefix`: slash separated, no leading slash, root not
    pinnable); a pin covers its exact path or a directory prefix
    (`domain.PrefixCovers`, so `a/b` never covers `a/bc`).
  - `stat_cache(path PK, size_bytes, mtime_ns, mode, hash, cached_at)` — working
    file fingerprints whose `hash` was validated when the content was last hashed;
    `cached_at` (unix nano) must be newer than `mtime_ns` for the row to be trusted.
- Content lives in `.nipa/objects/<hash[:2]>/<hash[2:]>` (content-addressed loose
  objects, written temp+rename (`.tmp-*` in the object's directory) so `nipa
  update` can skip unchanged content). `StoreChunks`/`StoreChunk` write objects; `OpenChunk`/`LoadChunk` read
  them; `MissingChunks` stats them, after one listing of the 256 fan-out
  directories when it is asked about 16 hashes or more (`objectFanout`): a
  hash whose `<hash[:2]>` directory does not exist is missing without a
  probe, so a fresh clone (empty store) stats nothing. Update/switch download only the chunks of
  files they rewrite and store only those of cached files (zstd text, pinned
  raw content, raw files without known chunk sizes): raw unpinned chunks go
  straight into the working file and never touch `.nipa/objects`, so there is
  no temporary 2× disk peak. Merge/revert cache materialized content, and `Push`
  caches the chunks it uploads before sending them except raw chunks of
  unpinned paths (the upload windows carry the bytes in memory). The DB holds metadata only; file→chunk hashes are stored as
  the `files.chunks` blob (no chunk catalog table), which is what lets `nipa diff`
  rebuild the old side offline.
- `StoreChunkFrom(hash, reader)` stores an object from a stream without
  holding it in memory: a `.tmp-*` file in the object's directory, hashed
  while written, renamed only when the hash matches. `StatEntries(paths)`
  reads single `stat_cache` rows (`StatCacheGet`) for callers that touch a few
  files. `.nipa/writes/` holds the working-copy write markers
  (`BeginWorkingWrite` / `MarkInterruptedWrite`, `writemark.go`): one locked
  file per running write, so an unlocked one identifies a killed write.
  Explicit `Chmod`s (objects, config) pass through `fsperm.Mask`.
- Object GC: `PruneObjects(keep)` deletes every object not in `keep` plus
  `.tmp-*` files older than an hour. `usecase.pruneCache` (`gc.go`) keeps the
  chunks of the `Snapshot()` files `cachedInPristine` accepts (zstd text, or
  raw content covered by a pin), so binaries are not kept twice, and it is a
  no-op while `merge_state`,
  `revert_state` or `shelf_state` is pending. It runs after a successful clone, update, switch,
  tag switch, `Push.Run` and `WorkingCopy.Restore`. `Diff.Run`/`ReadBlob`
  never prune mid-flow, because callers such as `aicontext` (`--generate`),
  `assetscan.BlobLoader` and the MCP asset tools read blobs after `Run`;
  `Diff.PruneCache` runs at the boundary instead: the end of `nipa diff` and
  the MCP per-call `UseCases.Close` (no-op when no clone was opened). The
  daemon's `cachePruner` (`daemon/prune.go`) prunes a root once no operation
  was admitted for `Options.PruneQuiet`, default 30 s, after a diff, blob,
  preview or restore call (deferred `pruneAfterSuccess`: skipped for repos
  without `PruneObjects`, failures only `slog.Warn`, never fail the operation),
  and on demand through `nipa gc` (`usecase.GC`), which prints the objects and
  bytes freed, or the daemon's `GC` RPC (the desktop "Clean up" button),
  which takes the root's exclusive coordinator slot (queued, never beside an
  operation or the idle pruner, which only fires when that slot is free) and
  settles the pruner's pending request after a run that was not skipped. Processes coordinate through `.nipa/lock` (`localrepo/lock.go`,
  `flock` on Unix, `LockFileEx` on Windows, a fresh handle per acquisition so
  holders in one process conflict like separate processes): clone, update,
  switch, tag switch, push (and `Plan`), merge, revert, restore, diff and
  `ReadBlob` hold it shared for the whole call (`lockShared` right after
  `Init`; the daemon gets it per operation through the same usecases, never
  per watched root), and prune takes it exclusive without waiting, returning
  `Skipped` with `repository busy` (`nipa gc` prints `skipped: repository
  busy`). The automatic prune runs after the operation released its shared
  lock (defer order), so it never upgrades in place.
- `SaveTree` semantics (see `store.go`): one transaction. The wanted tree is
  flattened (nodes parent-first) and diffed in memory against the stored
  `tree_nodes`/`files` rows (`syncRows`): parent, hash, mode, size, binary flag,
  encoding and the chunk blob are all compared, never the tree hash alone,
  because sparse, filtered and locally built trees do not guarantee that equal
  hashes mean equal rows. Only new or differing rows are upserted and rows that
  left the tree are deleted one by one, so saving a 10k-file clone with 5
  changes reads the stored rows and writes ~5 files plus their ancestor nodes
  (~20 ms), not the whole table. `Snapshot()` returns every `files` row. The same transaction drops `stat_cache` rows whose path left `files`.
  `meta_tree_hash` is re-set each save. `SaveTree(nil)` deletes every row.
  Cached objects are never touched by `SaveTree`; only the GC above removes them.
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
- `ListDir(dir)` reads one level of the snapshot (`TreeNodeChildren` by
  `tree_nodes.parent_path`, `FileListByTree` by `files.tree_path`, both
  indexed; stored paths carry a leading slash, the root is `""`) and
  `SnapshotFile(path)` one row (`FileGet`), so per-directory callers never load
  the whole snapshot.
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

