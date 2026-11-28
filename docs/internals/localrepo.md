# Client localrepo internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

## Client localrepo design (`internal/client/localrepo`)

- Linked metadata is refused: `Init` (`checkMetadataDirs`, `metadir.go`)
  Lstats `.nipa` and, when present, `.nipa/objects`, `.nipa/writes` and
  `.nipa/shelves` (O(1), at most four Lstats, nothing per object) and fails
  with a 400 `refusing to use ".nipa[/x]": it is a symbolic link` (or
  `junction or reparse point`, `domain.LinkKind`) before creating or opening
  anything; the clone root itself may be reached through a link. No supported
  workflow shares or links `.nipa` or its object store (each clone owns its
  cache). The destructive paths also act only through the real folder:
  `openRealDir` opens it as an `os.Root`, then checks the path still Lstats
  as a non-link folder that `os.SameFile`s the opened one, so a link swapped
  in after `Init` is refused too. `PruneObjects` lists and deletes through
  the objects root (a symlinked fan-out folder is skipped as a non-directory
  entry, and a fan-out swapped mid-prune cannot lead out of the objects
  folder), `BeginWorkingWrite` reads, probes and removes stale markers
  through the writes root, and `NewShelfWriter`'s temp sweep and
  `DropShelf` through the shelves root (`NewShelfWriter` fails when it cannot
  open it). Object and marker *creation* stays path-based below the folders
  `Init` verified.
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
    `commit_hash`, the pending `merge_state` / `revert_state` /
    `shelf_state` JSON, and `synced_tree`
    (`{"commit":"<base36 id>","sparse":["a","b"],"view":"<base64>"}`, sparse
    sorted and deduped, `[]` for a full checkout, `view` left out when the
    manifest came without a token): which commit's tree the stored rows hold,
    the sparse set it was fetched with and the server's view token of that
    manifest, or of the push response when a push was applied locally
    (`synced.go`, `ApplyPush` below). The token names the view (read rules, sparse set,
    recursive flag), never the commit; it is what a delta manifest request
    sends as `base_view` (`docs/internals/protocol.md`).
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
  reads `stat_cache` rows by path (`StatCacheListByPaths`, `pathBatch` = 500
  paths per query) for callers that touch a few files. `.nipa/writes/` holds the working-copy write markers
  (`BeginWorkingWrite` / `MarkInterruptedWrite`, `writemark.go`): one locked
  file per running write, so an unlocked one identifies a killed write.
  Explicit `Chmod`s (objects, config) pass through `fsperm.Mask`.
- Object GC: `PruneObjects(keep)` deletes every object not in `keep` plus
  `.tmp-*` files older than an hour; a push that knows which objects it can
  have orphaned deletes just those instead (`usecase.pruneAfterPush`,
  `docs/internals/client.md`). `usecase.pruneCache` (`gc.go`) keeps the
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
  `SaveSyncedTreeView(root, commitID, sparse, view)` is `SaveTree` plus the
  `synced_tree` record in the same transaction (`SaveSyncedTree` is the same
  without a token, for manifests fetched through a client that returns none);
  plain `SaveTree` (and an empty `commitID`) deletes the record, so it can
  only ever describe the rows it was written with. `ClearSyncedTree` deletes only the record (rows stay); a sync calls it
  before writing to the working copy. `LoadSyncedTree` returns nil for a missing or unreadable record, which
  `Update` reads as "unknown, do the full sync" (`docs/internals/client.md`).
  Cached objects are never touched by `SaveTree`; only the GC above removes them.
- `ApplyPush(clientDomain.PushApply)` (`push_apply.go`) turns the stored tree
  into the tree of the commit a push created, from the pushed files and
  removed paths instead of a manifest, in one transaction. Base check first
  (`clientDomain.ErrPushApplyBase`): the checkout is full, and the
  `synced_tree` record names `BaseCommitID` with an empty sparse set, or, for
  a push onto an empty branch (`BaseCommitID` empty), there is no record and
  no root row. Then `planPushApply` mirrors the server's `buildNewTree`: every
  directory from a touched path up to the root is rebuilt from its stored
  files (`FileHashesByTree`: path, hash, mode, no chunk blobs) and child
  directories (`TreeNodeChildHashes`); an untouched child keeps its stored
  hash and rows, a rebuilt directory left without files and children is
  dropped (the root never is), and so is an untouched child whose hash is the
  empty tree hash. A path left as a file and as a directory is refused with
  `clientDomain.PushApplyClashError` (nothing written), like the server does. Hashes come from `treehash.TreeHash`; the tracked mode
  class (1/2/3) is mapped back to the server's stored mode
  (`hashModeOfClass`) first, because `treehash` classifies stored modes and
  would read class 1 as an executable permission. Reads are three indexed
  queries per rebuilt directory, writes one row per pushed file, removed
  file and rebuilt directory, never the whole tree. If the computed root is
  not `TreeHash` it returns `*clientDomain.PushApplyMismatchError`
  (expected, computed) before any write. On a match it writes rows identical
  to what `SaveSyncedTreeView` writes from the new head's manifest
  (`pushedFileRow`: mode as `domain.ClassifyPerm` class, binary forced for
  `chunker.IsLockablePath`, `chunker.NormalizeEncoding`, directory mode 0),
  deletes the `stat_cache` rows of the removed paths (`StatCacheDelete`, not
  the full sweep), and sets `tree_hash` and the `synced_tree` record
  (commit = the pushed commit, `[]`, and the view token `pushApplyView`
  picks). A token may only sit next to rows that are exactly the manifest of
  the commit in that view, because a delta against it is applied to those
  rows and "unchanged" keeps whatever they hold. The root hash check proved
  the rows are the new commit's whole stored tree, so what remains is whether
  the view hides any of it. `PushApply.View` (the push response's
  `serverDomain.PushView`, `docs/internals/protocol.md`) decides: no token in
  the response (an older server) keeps the base record's token as before; a
  pushed path the pusher cannot read (`HidesPushed`) records none (the rows
  hold a file the manifest hides; a delta would keep it for good when its
  directory never changes again, `TestEndToEnd_PushOfUnreadablePathDropsDeltaBase`);
  an unrestricted view records the response's token whatever the base held
  (none after an empty clone, a plain `SaveSyncedTree` or a refetch through a
  token-less client, or one from older rules); a restricted view records it
  only when the base rows were that view already: a push onto an empty branch
  (no rows) or a base recorded with the same token. Any other restricted base
  records none, which costs what keeping a stale token would: the server
  would decline it, so the next move fetches the full manifest. The decision
  is in memory, no extra query.
  (`TestApplyPush_RecordsTheViewThePushVouchesFor`.)
- `PlanTreeDelta(clientDomain.TreeDeltaApply)` / `ApplyTreeDelta(in, planned)`
  (`tree_delta.go`) apply a delta manifest (`treedelta.Delta`, the answer of
  `GetTreeManifestDelta`) to the stored rows instead of saving the target's
  manifest. Both run `planTreeDelta` in a transaction; the plan only reads,
  the apply also writes, so the caller can sync the working copy in between
  (rows are saved after the working copy, as in the full sync).
  - *Base check* (`checkDeltaBase`, `clientDomain.ErrTreeDeltaBase`): the
    `synced_tree` record names `BaseCommitID` with the same sparse set.
    `ApplyTreeDelta` with a `planned` result also accepts a missing record
    (the sync between plan and apply drops it, `ClearSyncedTree`) and then
    relies on `tree_hash` still being `planned.BaseTreeHash` and on the
    changes leading to `planned.TreeHash` again; any tree saved in between
    changes `tree_hash`. With `planned == nil` the record alone decides.
  - *Plan* (`treeDeltaPlan.dir`, recursive over the changed directories
    only): reads the directory's row, its files (`FileHashesByTree`: path,
    hash, mode) and its children's hashes (`TreeNodeChildHashes`). A file
    listed `unchanged` must be held (else `ErrTreeDeltaBase`: the base was
    not the stored tree); a file listed in full becomes a row
    (`deltaFileRow`, the row `flattenTree` writes); held files not listed are
    deleted. A directory listed `unchanged` keeps its stored hash and rows,
    or stays absent when the tree does not hold it ("keep what you hold, if
    anything"), or is removed when it is held with the empty-tree hash; a
    changed one recurses; held directories not listed leave with their whole
    subtree (range delete on the path prefix, `FileDeleteInRange` /
    `TreeNodeDeleteInRange`). Hashes are recomputed bottom-up with
    `treehash.TreeHash` over mode classes mapped by `treedelta.HashMode`; a
    changed directory that ends up without entries is dropped (the root never
    is). An `unchanged` root changes no row and no `tree_hash`.
  - *Verification*: when the delta carries `RootHash` the computed root must
    equal it, and with `planned` it must equal `planned.TreeHash`; otherwise
    `*clientDomain.TreeDeltaMismatchError` (expected, computed) and nothing
    is written. A delta without a root hash (a filtered or sparse view, an
    unprovable tree) is applied unverified: the computed hashes are the only
    ones, exactly as `treedelta.Apply` documents.
  - *Result* (`clientDomain.TreeDeltaChanges`): `Written` (path, the
    manifest file, and the stored row it replaces or nil when added; a file
    sent in full that equals the stored row costs no write and is not
    listed), `Removed` (the stored rows that leave, read by path, or by range
    for a subtree), `BaseTreeHash` and `TreeHash`. These are the only files a
    caller has to materialize or delete.
  - *Writes*: subtree range deletes, then the node upserts (parents first),
    file upserts, file deletes and node deletes; `stat_cache` rows of the
    removed paths (`StatCacheDelete`); `tree_hash`; the `synced_tree` record
    with the answer's view token. The result equals what
    `SaveSyncedTreeView` writes for the target's manifest (unit test
    `TestApplyTreeDelta_EqualsSavingTheManifest`, end to end
    `TestEndToEnd_DeltaUpdateProperty`). Reads and writes follow the changed
    directories, never the tree (`TestApplyTreeDelta_CostFollowsTheChange`).
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
- Scoped reads (`scoped.go`), for commands whose cost must follow the change
  and not the repository (`usecase.trackedScope`, `Push.stagedBase`, the
  post-push prune): `SnapshotFiles(paths)` returns the tracked rows among the
  given paths (`FileListByPaths`, `IN (sqlc.slice)`, `pathBatch` paths per
  query; untracked and empty paths are absent); `SnapshotFilesUnder(dir)` the
  rows below a directory at any depth, sorted, and `TracksUnder(dir)` whether
  there is one (`FileListInRange`/`FileFirstInRange`: a range of the `files`
  primary key from `/<dir>/` up to `/<dir>0`, the next byte after the slash,
  so there is no `LIKE`: `%` and `_` in names are plain bytes and `a` never
  matches `ab`); `SnapshotTreeHash()` the `tree_hash` meta row, what
  `Snapshot().TreeHash` holds. `FilesWithChunk(hash)` returns the files whose
  chunk list holds a chunk (`FileListByChunk`: `instr` on the `chunks` blob,
  re-checked on hash boundaries in Go). There is no chunk index, so this one
  is a table scan inside SQLite that returns only matching rows (about 16 ms
  on 100,000 files, against ~370 ms for `Snapshot()` plus the cache listing);
  a chunk-to-file index would need a schema change. `RemoveObjects(hashes)`
  deletes named objects through the objects root (`openRealDir`), skipping
  missing ones and linked fan-out folders, and returns the count and bytes
  freed.
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

