<!--
Handoff notes for the implementing session:
- Builds on PR #178 (branch `improve-update-clone`): parallel/streaming materialization in
  `internal/client/usecase/materialize.go` (`materializeAll`, `materializeFile`), the parallel
  chunk store workers in `downloadMissing` (`update.go`), and restore's stat-cache trust
  (`changedFromSnapshot` in `restore.go`). Start from that branch or after it merges.
- Decisions already made with the user: binaries are not cached except under locks this client
  took; text (zstd) stays cached; GC + direct-to-file download are in scope; no backward
  compatibility (unreleased project).
- Global rules: never commit to git; new code needs >=80% test coverage; keep comments minimal.
-->

# Cut client disk usage: phased plan

## Context
Every chunk a client downloads or pushes is kept in `.nipa/objects` forever. A 2 GB clone
takes ~4 GB on disk (objects + working files), and each update or push that changes
binaries adds more. Most users are Windows game devs with large binary assets. NTFS has
no copy-on-write, and hardlinks are unsafe with editors that write files in place, so the
fix is to stop keeping a second copy of binaries:

- **Text** (zstd-encoded chunks): stays cached. It's small and needed for offline diff and merge.
- **Binaries** (raw-encoded chunks): not kept. Only files under a lock this client took keep
  their old version, so offline compare and restore still work for files being edited.
- **GC** drops objects nothing references.
- **Direct-to-file download** writes binary chunks straight into the working file.

Target steady state: ≈ 1× working tree + compressed text + old versions of locked files.

The project is unreleased: no backward compatibility, no migration, and no switch back to
the old behaviour. Old clones are simply re-cloned.

Each phase is a self-contained change: it builds, has tests with ≥80% coverage on new code,
and passes `go vet` and `go test -race ./internal/client/... ./internal/e2e/...` before the
next phase starts.

---

## Phase 1: GC and local schema cleanup
Delivers: the cache stops growing without limit. Behaviour is otherwise unchanged (all
snapshot chunks are still kept).

- `internal/client/localrepo/schema/schema.sql`:
  - Drop the dead `snapshot_id` columns and their indexes.
  - Add `pinned_paths(path TEXT PRIMARY KEY, pinned_at INTEGER)`, used from phase 3.
  - Update queries in `localrepo/queries`, run `make sqlc`, and update `SaveTree`/`syncRows`.
- `localrepo/objects.go`: `PruneObjects(keep map[Hash]struct{}) (files int, bytes int64, err error)`.
  It walks `.nipa/objects/xx/*`, deletes objects not in `keep`, and sweeps stale `.tmp-*` files.
- `usecase/gc.go` (new), `pruneCache(lr)`:
  - Keep set = every chunk of every `Snapshot()` file. Phase 3 narrows it.
  - No-op while `merge_state` or `revert_state` is pending.
- Call `pruneCache` after a successful clone, update, switch and tag switch (`syncWorkingCopy`
  callers) and after `Push.Run`.
- `cli/gc.go` (new): `nipa gc` prints the objects and bytes freed.
- Tests:
  - localrepo: prune keeps and deletes the right objects, sweeps temp files.
  - usecase: GC skipped while a merge/revert is pending; old objects disappear after an update.
  - `tests/suite`: `nipa gc` spec.
- Docs: `AGENTS.md` localrepo section ("never erased", `snapshot_id`).

## Phase 2: Fallback when cached content is missing
Delivers: every reader copes with a missing chunk. Nothing is evicted yet, so it's safe to
ship on its own.

- `usecase/pristine.go` (new), `ensureSnapshotContent(ctx, files)`: for snapshot files whose
  chunks `MissingChunks` reports:
  1. If the working file still has the snapshot hash (fresh `stat_cache` via `statMatches`,
     else hash it), re-chunk it with `chunker.Encode` plus `storedEncoding`, verify
     `FileHash`, and `StoreChunks`. This works offline.
  2. Otherwise call `downloadMissing` scoped to the pinned commit (`LoadCommit`) and the paths.
  3. With no downloader wired, fail with a user error ("content not cached locally; connect
     to the server").
- Optional downloader seam: `WorkingCopy.WithDownloader(auth, client)` and the same for `Diff`.
  Wire it in the CLI builders and in `cmd/nipa/serve.go` `serveRepoOps`.
- Call `ensureSnapshotContent` before content reads:
  - `Restore` (`restore.go`), for the materialize jobs.
  - `ReadBlob` with `BlobSnapshot` (`blob.go`). This is the desktop/daemon compare.
  - The working-copy `Diff` old side (`diff.go` `attachLoader`/`similarityLoader`), only for
    entries whose content is loaded (text, or binary with `--ext-diff`).
  - `threeway.go` already re-downloads missing chunks; keep it as is.
- Tests: delete objects by hand, then check restore, blob and diff:
  - recapture from an unchanged working file (no network).
  - download when the working file was modified.
  - user error when there's no downloader.

## Phase 3: Binary cache policy and lock pins
Delivers the disk saving.

- `localrepo`: `PinPaths`, `UnpinPaths`, `ListPins`. A pin is an exact path or a directory
  prefix, matched with the same rule as `domain.PrefixCovers`.
- `usecase/pristine.go`: `cachedInPristine(file, pins)` is true when the encoding is zstd or a
  pin covers the path. The phase 1 keep set becomes the chunks of snapshot files where this
  is true.
- `syncWorkingCopy`: after materializing, prune (raw chunks of unpinned files go away).
- Push: the uploader skips `StoreChunks` for raw chunks of unpinned paths (the scanner knows
  each file's encoding). After a successful push, unpin the exact pushed paths (mirrors the
  server's `ReleaseLanded`). Directory pins stay.
- Lock and unlock:
  - `usecase.FileLock.Lock`: after the server grants the lock, `PinPaths([path])`, then
    `ensureSnapshotContent` for the covered raw files. A capture failure is returned as a
    warning and never undoes the lock.
  - `Unlock`: `UnpinPaths`, then prune.
  - Daemon: `ProxyLockFile`/`ProxyUnlockFile` in `internal/client/daemon/proxy.go` do the same
    through a new `RepoOps` entry (`daemon/ops.go`), so desktop, Unity and Godot are all covered.
- Locks taken elsewhere (web UI, another machine) aren't pinned. Reads use the phase 2 fallback.
- Tests:
  - unit: pin matching; lock pins and captures; unlock unpins and prunes; push skips raw
    caching and unpins.
  - daemon: proxy lock/unlock pin.
  - e2e: clone with binaries → no raw chunks in `.nipa/objects`; lock + edit + offline →
    `diff --ext-diff` and `restore` work; edit an unlocked binary → `restore` re-downloads.
- Docs: `AGENTS.md` (client localrepo design, lock flow), `docs/DAEMON.md` (lock pins).

## Phase 4: Direct-to-file download
Delivers: half the disk writes during clone and update, no temporary 2× peak, and file
building overlapped with the download.

- `syncWorkingCopy` splits the jobs:
  - **Direct** = raw encoding, not pinned, every chunk size known from the manifest
    (`materializedFile.ChunkSizes`; for raw, stored size = raw size).
  - Everything else uses the existing object-cache path (`materializeAll`).
- Direct files:
  - Create a `.nipa-materialize-*` temp file next to the target and `Truncate(SizeBytes)` it.
  - Build `hash → []{file, offset}` (one chunk may appear in several files or offsets).
  - Chunks already in objects are copied locally. Only the missing ones go to `DownloadChunks`.
- `downloadMissing` gets a per-chunk sink. Its parallel store workers verify the hash, then
  either `StoreChunks` (cached path) or `WriteAt` into each target. Each write opens and closes
  the file, so a 10k-file clone doesn't hold that many Windows handles.
- A per-file remaining-chunk counter. When it reaches 0: chmod (same read-only rules as
  `materializeFile`), `replaceFile`, `statEntryFor`.
- On failure, remove the temp files. Working files are only replaced after a complete write,
  and removals still run last.
- Tests:
  - offsets; one chunk shared by two files; unknown chunk sizes fall back to the cached path.
  - a mid-download failure leaves working files intact and no temp files behind.
  - a read-only file stays read-only.
- Bench: extend `TestEndToEnd_CloneThroughput` to log the size of `.nipa/objects` against the
  working tree. Clone time shouldn't regress from ~1.1 s for 1.5 GiB.

---

## Final check (after phase 4)
- `make test-client` (free) on Linux. Run `go test ./internal/client/...` on Windows (or in
  Windows CI) for `replaceFile` and read-only handling.
- Manual: clone a large binary project on Windows and compare the size of `.nipa` with the
  working tree; then lock, edit and restore a texture offline from the desktop app.
