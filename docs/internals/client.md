# Client internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

- **Client** (`internal/client/`): `cli` (cobra commands: login (password or SSH
  key), clone, branch
  (`-a` list, `-c` create+switch, `-d` delete), tag (list, `-c` create, `-d`
  delete), add, remove, restore, status, push, update, switch (branch or `--tag`),
  merge, revert, log, diff, blame, assets check, provenance, acl, group, sparse-checkout, mr, lock, unlock, gc, shelve, ignore import, import git, serve, mcp), `usecase`
  (clone/login, push, update/switch (incl. sparse and detached-tag checkout),
  merge, revert, diff, log, blame (`blame.go`: path log via
  `GetPathCommitLog` capped at 500 scanned commits, each version through a
  dir-scoped `GetCommitDirManifest` + commit-scoped download, lines
  carried back with `diff.Lines` and attributed newest first until every
  line has an owner; working file vs pinned commit marks `Uncommitted`,
  text ≤ 2 MiB, older binary/large versions end the history like the cap),
  tag management,
  permission, merge-request orchestration, file locks over small interfaces; `threeway.go`
  holds the shared materialize/stage/delete core), `grpc` (gRPC transport,
  converts pb→server domain types; `Client.ServiceClient()` exposes the typed
  pb client for the daemon proxy), `securestorage` (keyring-backed token store), `config`
  (`~/.config/nipa/config.json`: `diffExternal`, `uploadWorkers`, `llm`), `difftool`
  (external diff runner), `ignore` (root `.nipaignore` + local `.nipa/ignore`
  pattern matcher for untracked working files), `domain` (client
  config/url/errors/commit/pending state), `localrepo` (`.nipa/` local metadata
  + SQLite),
  `daemon` (the loopback gRPC service behind `nipa serve`, see the flow below),
  `mcp` (the stdio MCP server behind `nipa mcp`), `assetscan` (asset change
  runner), `aiprompt`/`aicontext`/`llm` (generation; flows below).
  `localrepo.FindRepoRoot`
  searches up to 32 parent directories for a `.nipa/config` so every in-repo
  command works from subdirectories.
- **Diff engine** (`internal/diff/`): pure package shared by client and server
  candidates — Myers line diff (`Lines`/`LinesWith` with an equality hook for
  whitespace ignores), tree-state comparison
  (`Entry`/`Change`/`Compare`/`FromTree`/`LoadContent`), rename detection
  (`DetectRenames`), content assembly (`AttachContents`), a server-facing
  tree-vs-tree entry point (`TreeDiff`) and unified/stat/name rendering.
  `internal/merge` (three-way tree decisions + diff3 text merge, shared by the
  client and the server merge strategies) delegates its line diff to
  `diff.Lines`. Keep `internal/diff`
  free of client imports: the server merge-request diff reuses it.

Flow for `nipa clone <url> <target>`: `cli/clone.go` parses the URL →
`usecase.Repo.Clone` → ensure empty target → login → resolve default branch (or
`GetBranchByName` when a branch is given) → fetch tree manifest (gRPC
`GetTreeManifest`, recursive, with the URL path) → `localRepo.Init(target)` →
`SaveConfig` (`.nipa/config` json: url + branch + sparse prefixes) →
`syncWorkingCopy` (shared with update/switch: download missing chunks through
the commit-scoped `ChunkScope`, materialize files, record `stat_cache`
fingerprints; raw files no pin covers whose manifest chunk sizes
(`FileNode.chunk_sizes`, mapped by `client/grpc.toServerFile` into
`Chunk.SizeBytes`; older servers send none and take the cache path) add up to
the file size are written directly (`direct.go`): a truncated
`.nipa-materialize-*` temp file per file, chunks already in `.nipa/objects`
copied locally, the rest written by `downloadChunks`' hash-verifying workers
at every offset they occupy, a file is chmodded
(same read-only rule as `materializeFile`) when its last chunk landed, and
the working files are `replaceFile`d (bounded pool) only after the whole
download and `materializeAll` succeeded; any failure removes the unreplaced
temp files, removals still run last; with a client that implements
`DownloadChunksStream` (the gRPC client) a raw chunk no cached file shares is
never buffered: `directPlan.receive` copies the response body into the first
temp-file offset it occupies through a BLAKE3 `chunker.Hasher` and a pooled
256 KiB buffer, checks size and hash, copies it file to file to its other
offsets, and `settle` books it once (`downloadChunksStream` dedups under a
lock, since streamed chunks are booked from the transfer workers), so memory
no longer grows with chunk size or transfer concurrency; nothing a failed or
repeated `receive` wrote is used, because files are swapped in only after
every chunk settled. Temp files are not reopened per chunk: every write,
streamed or buffered, borrows the file's handle from the plan's `tempHandles`
(`usecase/direct_handles.go`) and writes by offset (`WriteAt`), so concurrent
workers share one handle per file. At most `maxOpenTemps` (64) handles stay
open between writes (the least recently used idle one is closed to make
room; handles being written are never closed, so a burst can exceed the
bound by the files in flight), a file's handle is closed when its last chunk
settled, and `finish`/`cleanup` close everything before a temp file is
renamed or removed (Windows refuses both on an open file); a close error of
an evicted handle fails the file when it completes. Chunks the object cache keeps are streamed too
(`syncStreamTarget`): into the cache through `localrepo.StoreChunkFrom`
(temp file, hashed on the way, renamed only when the hash matches), and a
chunk both a cached and a direct file need is stored first and placed from
there; only a repository without `StoreChunkFrom` (test stubs) buffers them.
`downloadMissing` (diff, blob, shelve, merge, the restore cache path) streams
into the cache the same way. Planning walks the tree with `walkTreeFiles` and
copies chunk lists only for the files it rewrites (`treeFileHash` compares an
unchanged file without copying). Removals (`removeLeftFiles`) run on the
`runPool` and trust a fresh `stat_cache` fingerprint before hashing a file.
Every sync and restore brackets its writes with `beginWorkingWrite`
(`usecase/sweep.go`): `localrepo.BeginWorkingWrite` keeps a locked marker in
`.nipa/writes/`, so a marker nobody holds means a write was killed, and the
next write then sweeps `.nipa-materialize-*` files older than itself minus 5 s
from the working copy (younger ones re-arm the marker; nothing is walked on a
clone that was never interrupted; `nipa gc` also removes ones older than an
hour). Modes set by `Chmod` go through `fsperm.Mask`, which drops the group
and other bits the process umask clears. Everything else (zstd text, pinned raw, unknown chunk sizes)
goes through the object cache and `materializeAll`) → `SaveTree` → `SaveCommit` (branch head commit id). Every
server-supplied directory and file name is a single path segment
(`domain.ValidateLocalPathSegment`: no empty/`.`/`..`, `/`, `\`, NUL, and no
`.nipa` in any case, trailing-dot/space or `NIPA~1` spelling; on Windows also no
`:`, trailing dot/space or device names like `CON`/`aux.c`, which stay valid on
other OSes because real repos such as the Linux kernel track `aux.c`). The
grpc manifest assembler and `GetMergeBase` reject a bad name, `syncWorkingCopy`
re-checks the whole tree (`validateSyncTree`) before writing anything, and
`materializeFile`/`createSizedTemp` refuse any unsafe path
(`checkWorkingPath`), so merge/revert/shelve/restore writes are covered too;
the root's own name is only a label.

Working-copy write guard (`usecase/write_guard.go`): names alone do not stop a
write through a folder on disk that is a symlink or junction (`assets ->
~/.ssh`, or `-> .nipa`). Every write or delete of a tracked path goes through
`checkWriteInsideRoot` / `checkRemoveInsideRoot` (`checkWorkingPath` plus a
`dirGuard.check`): `materializeFile`, `createSizedTemp` (direct plan),
`guardedRemoveCached` (`removeLeftFiles`, three-way deletions),
`removeWorkingFile` (shelve), `Conflicts.Resolve` (write and delete) and
`RevisionRestore`. The rule: every folder below the root on the way to the path
must not be a symlink (`ModeSymlink`) or a junction/name-surrogate reparse
point (Go reports those as `ModeIrregular` without `ModeDir`; a folder with
`ModeDir|ModeIrregular`, e.g. a OneDrive placeholder, is a real folder),
wherever it points, even inside the clone; Nipa never tracks or materializes
symlinks (`nipa import git --symlinks file` stores the target as content), so
nothing legitimate is lost. The root itself is never Lstat'ed, so a clone
reached through a symlink works. The final component may be a link: writes
rename over it (`replaceFile`; the Windows read-only path only chmods a
regular file, `isReadOnlyFile` uses `Lstat`) and deletes unlink it, never its
target. Refusals are 400 user errors `refusing to write|remove %q: %q is a
symbolic link` and are not bypassed by `--force`. Cost: a `dirGuard` caches
each verified folder (and folders found missing, which the operation creates,
so nothing below them is Lstat'ed), so a guard costs at most one `Lstat` per
distinct folder; `beginWorkingWrite` (every sync and restore) and
`applyThreeWay` open a `guardWrites(root)` scope that shares one guard across
the planning check and every write-time check of that operation (scopes nest,
keyed by the cleaned root, dropped when the last closes, so a later operation
never trusts an earlier one's folders); outside a scope `guardFor` returns a
fresh guard. Planning checks run before anything is read, downloaded or
written: `syncWorkingCopyOpts` checks every file it rewrites and every
removal (`checkSyncTargets`, before `localChangeConflicts`), `restoreFiles`
the changed files (`checkRestoreTargets`, before `restoreDirect`), and
`applyThreeWay` (write mode) its writes and deletions before
`MissingChunks`. Residual risk: check-then-write is not atomic; a local process
that swaps a verified folder for a link mid-operation is not stopped (no
`openat`-style walk). `.nipa/` itself and root-level files written by
`SaveProvenance` (rename over a link) and `ignore.AppendRules` (`os.WriteFile`,
follows a symlinked `.nipaignore`; user-initiated) are outside the guard. A repo that already tracks such a path
fails to sync with a 400 "refusing to sync unsafe path" error. `isNipaPath`
(stage/push/restore/shelve exclusion) uses the same case-insensitive
`domain.IsMetadataSegment`. Clone,
update and switch record the branch head commit id locally so `HEAD`/`@` (and
branch creation via `nipa branch -c`, which forks from the pinned commit)
resolve offline.

Issue keys: `usecase.Issue` (`issue.go`, `Resolve`/`Search` over the
`ResolveIssues`/`SearchIssues` RPCs) backs `nipa branch --issue KEY [-c name]`
(also `-c --issue KEY`, which pflag reads as `-c "--issue"` plus an argument).
`BranchFor` validates the key client-side (`issuetracker.ParseAnyKey`), names
the branch with `issuetracker.BranchName` (a `-c` name not led by the key is
prefixed with it; no name gives `KEY-<slug of the title>`, max 48 slug bytes,
or `KEY` when the title is unknown), refuses an unknown issue (`not_found`)
or a key outside the project's keys (empty answer), and only skips the check
on 404/409/503 (no tracker, integration off; an `Unimplemented` older server
maps to 404) or an `unavailable` tracker. `CommitLogEntry.IssueKeys`
(`domain.IssueKeyRef`) and `MergeRequest.IssueKeys` come from the gRPC
`issue_keys` fields; `nipa log` prints an `Issues:` line in the full format,
` [KEY, ...]` after the subject with `--oneline` and `issue_keys` in `--json`, `nipa mr view` an `issues:` block.

Local changes guard: `Update.Run`/`Switch`/`SwitchTag` sync with
`syncOptions.keepLocalChanges` unless the `Update` came from `WithForce(true)`
(`nipa update --force`, `nipa switch --force`, the MCP tools' `force`).
`localChangeConflicts` (`usecase/sync_guard.go`) looks only at the files the
sync is about to replace: a tracked file whose content is neither the synced
nor the incoming version (a fresh `stat_cache` fingerprint answers without
reading, otherwise the file is hashed on the `runPool`), or an untracked file
that differs from the incoming one. Any hit fails the sync with a user error
listing up to 20 paths before anything is downloaded or written. Clone and the
merge/revert paths call `syncWorkingCopy` unguarded; the daemon's
`Update` is built `WithForce(true)` (`cmd/nipa/serve.go`) because its clients
preview with `SyncPlan` and confirm in their own UI.

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

Flow for ignore rules (`.nipaignore` at the repo root, versioned; `.nipa/ignore`
per clone, never tracked): `internal/client/ignore.New` compiles both files
(local rules last, so they win) with a pragmatic gitignore subset (`#` comments,
`!` negation, `*`/`?`, `**`, leading/mid `/` root anchoring, trailing `/` =
directory-only; last match wins). `WorkingCopy.Status` and `Diff.scanWorking`
walk the working tree through `walkWorkingFiles`, which drops ignored paths
unless they are tracked or staged (tracking always wins over ignores) and prunes
ignored directories that contain no protected paths. `nipa add` errors on an
explicitly ignored target with a `use -f` hint (`AddOptions{Force}` / `nipa add
-f` stages it anyway); directory and whole-repo adds skip ignored files
silently. Add stages only tracked paths that are new, modified or missing:
files whose fresh `stat_cache` fingerprint matches the snapshot hash are
skipped (no content is read), and the remaining paths are staged in one
transaction (`StageAddMany`, client DB runs WAL + `synchronous=NORMAL`), so
`nipa add *` on a large clone is cheap. The matcher is client-side only: a
hostile client can still push such paths, and a tracked file that later matches
a rule stays tracked until explicitly removed. Sparse manifests always include the root `.nipaignore` (and `.nipaassets.yml`, `.nipaprovenance.yml`)
(`loadTreeManifest`'s `keepRootTeamFile`, still behind the read filter), so
partial checkouts apply the team rules too. The pattern compiler itself lives
in the pure `internal/pathmatch` (`Parse`/`Compile`/`Join`/`Matches`), shared
with asset budgets. Nipa never reads `.gitignore`: `ignore.ImportGitignore`
(`nipa ignore import`, the desktop `GitignoreOffer` banner shown once per
clone while `CanImportGitignore`) appends the root `.gitignore` rules
`.nipaignore` lacks under a `# imported from .gitignore` header and lists
lines with unsupported syntax (`[...]`, `\`). `ignore.DetectGenerated` groups
new paths under the outermost generated folder (`node_modules`, `.next`,
`__pycache__`, `.godot`, ... anywhere; Unity `Library`/`Temp`/`Logs`/`obj`/
`UserSettings` next to `ProjectSettings`; Unreal `Intermediate`/`Saved`/
`Binaries` next to a `.uproject`) with the `.nipaignore` pattern to add
(`ignore.AppendRules`). `AddWithReport` fills `ProvenanceReport.Generated`
for untracked staged paths and `nipa add` prints a warning plus a
`nipa remove` hint per folder (stages anyway); the desktop `useStageGuard`
asks first (`GeneratedFoldersDialog`: Cancel, Stage anyway, Ignore folders =
`IgnoreFolders` then stage the rest) before its deletion check.

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

Flow for `nipa restore <path>...` (git-restore equivalent, `internal/client/usecase/restore.go`):
`WorkingCopy.Restore` loads the local snapshot, expands file/directory targets
over tracked paths (unknown paths error `not tracked`), refuses any target with
staged changes ("push or reset them first" — staged content is not stored
separately), then re-materializes the rest from the local object store through
`materializeFile`, skipping files whose working hash already matches so mtimes
stay stable; refreshed fingerprints are batched into `stat_cache`.
`materializeFile` and conflict resolution go through `replaceFile`
(`materialize_other.go` / `materialize_windows.go`, which clears
`FILE_ATTRIBUTE_READONLY` for the rename): a read-only working file is
replaced and stays read-only, since engine plugins own that state. Offline
while the chunks are cached in `.nipa/objects`; snapshot chunks missing from
the cache go through `pristine.ensureSnapshotContent` (`usecase/pristine.go`):
each missing chunk is first recaptured by re-chunking the working file with
`chunker.Encode` + `storedEncoding` (skipped when a fresh `stat_cache` entry
already shows a different hash; chunks are content addressed, so only the
missing hashes the re-chunk reproduces are stored), and the rest is downloaded
with `downloadMissing` scoped to the pinned commit and those paths through the
optional `WorkingCopy.WithDownloader(auth, client)` seam (CLI `restore`, the
daemon's cached `WorkingCopy` via `RepoOps.Auth`/`Downloader` from
`serveRepoOps`, the MCP restore tool via `UseCases.Auth`/`Connector`); with no
downloader, or when the server is unreachable (gRPC `Unavailable` or a
dial `net.OpError`, `offlineError`; other errors pass through), it is a user
error `content not cached locally; connect to the server`. A successful
restore prunes the cache like update does (`pruneAfterSuccess`), so a
downloaded unpinned binary does not stay in `.nipa/objects`. The daemon
exposes it as the unary `Restore` RPC returning the refreshed status.

Restore reads as little as it restores: when every target names one tracked
file, `restoreSelection` looks them up with `SnapshotFile` instead of loading
the snapshot, `restoreStatCache` reads their fingerprint rows
(`localrepo.StatEntries`, up to 512 paths) instead of the whole `stat_cache`,
and the prune only runs when the restore put content into the object cache.
`changedFromSnapshot` keeps the chunk layout the hash pass produces
(`rehashLayout`), and `restoreDirect` (`usecase/restore_direct.go`) rebuilds
raw, unpinned files whose content is not cached without the cache: it asks
the pinned commit's manifest for the chunk sizes (`restoreManifest`: the
server's path filter takes folders and answers 404 for a file path, so the
request names the folders the files sit in, the root's own files come from a
non-recursive `GetCommitDirManifest`, and the sparse set is asked for above
256 files or when a root file is restored through a downloader without
`GetCommitDirManifest`), builds a `directPlan`, copies cached chunks, copies the chunks the
changed working file still holds from that file by offset (`salvage`: no
second read-and-hash; trusted only while the file's size and mtime are what
they were when hashed), and streams the rest from the server. A downloader
without `GetCommitTreeManifest`, an unreachable server, a missing commit pin
or a manifest that does not match the snapshot leaves the file to the cache
path described above. `restoreTargets` sorts the tracked paths once and
binary-searches each target's `target/` range, so expanding targets costs
the files they cover, not targets × tracked files.

"Get this version" (`usecase/restore_revision.go`, `RevisionRestore.Run`,
daemon `RestoreRevision`) writes one file of a commit over the working file
(an explicit overwrite: local edits to that file are replaced, nothing is
staged). The path goes through `cleanBlobPath` and `checkWorkingPath` before
any request, and `checkWriteInsideRoot` refuses a path below a symlinked
folder (the working-copy write guard below). It holds
the shared repo lock, refuses a pending merge, revert or shelf apply, a path
outside the sparse set and a folder, and brackets the write with
`beginWorkingWrite`. The file's entry comes from the manifest of its folder
(`GetCommitDirManifest`, never the commit's whole tree; a 404 is told apart
as missing commit or missing path by asking for the commit's root). Raw
content with known chunk sizes is assembled by a one-file `directPlan`
(cached chunks copied, the rest streamed, swapped in when complete) whether
or not the path is pinned, since the cache never keeps a past revision;
zstd text and files without chunk sizes are streamed into the object cache
and written by `materializeFile`, and only that path prunes the cache
afterwards. Chunks the working file already holds are not reused (it is not
hashed), and no `stat_cache` fingerprint is recorded, so the next status
hashes the file once.

Flow for `nipa diff [<rev1> [<rev2>]] [--] [<path>...]`: `cli/diff.go` splits
revisions from paths (paths only after `--`; `<a>..<b>`/`<a>...<b>` expand to two
revisions and `...` sets merge-base mode) → `usecase.Diff.Run`. With no revisions
it is offline while the old side is cached (otherwise the old side of
deletions and modifications, binaries only with `--ext-diff`, goes through
`ensureSnapshotContent` like restore, using the `Diff`'s own auth/client as the
downloader; `ReadBlob` with `BlobSnapshot` does the same): `Snapshot()` (paths, hashes, modes and per-file chunk
hashes) + `ListStaged()` → `walkWorkingFiles` (shared with `WorkingCopy.Status`).
Tracked files whose `stat_cache` fingerprint is fresh and whose file hash equals
the old side are reused without reading; everything else (staged additions,
stale fingerprints, real changes) is hashed with `chunkFile` → `diff.Compare`
over path-keyed `diff.Entry` maps; untracked-unstaged files are excluded, missing
tracked files are deletions, staged new files are additions. `--no-cache` reads
every working file. With revisions, each token
resolves to a commit ID first (branch name via gRPC `GetBranchByName`, then tag
name via `GetTagByName`; `HEAD`/`@`
via localrepo `LoadCommit`, falling back to the configured branch; otherwise
`snow.ParseBase36` → gRPC `GetCommit`), then `GetCommit(id).Tree` is flattened
with `diff.FromTree`; missing chunks are downloaded with `downloadMissing`
through the commit-scoped `ChunkScope` (binary content only with
`--ext-diff`). `<a>...<b>` calls `GetMergeBase` with
the two commit IDs and uses `MergeBaseTree` as the old side (the client sends
`omit_tree` and streams the base tree through `StreamTreeManifest` with
`commit_id`; an older server's inline tree is used as is). Renames always run
through `diff.DetectRenames` (exact hash, line similarity for text, chunk
overlap otherwise); `-w`/`-b` live in `diff.Options` and `diff.FilterIgnored`
drops files whose differences are fully ignored. `--ext-diff` runs
`internal/client/difftool` with git's 7-argument contract (command from
`NIPA_EXTERNAL_DIFF`/`diffExternal`) and only when requested. `--staged`/`Paths`
narrow the result; rendering (patch, stat, name-only, name-status), colors and
the pager live in `cli/diff.go` + `internal/diff`.

Flow for `nipa shelve [-m name] [-u] [-- paths]`, `nipa shelve list|show [-p]|
apply <id> [--keep]|apply --continue|apply --abort|resolve <path> [--ours|--theirs]|drop` (local only;
`usecase/shelve.go`): a shelf is `.nipa/shelves/<id>/` (`manifest.json` =
`domain.Shelf`, entries with the shelved `ShelfFile` and the `Base` version,
`staged`/`untracked`/`locked` flags, `base_stored` when the base text chunks
were copied in; chunks under the shelf's own `objects/`), written by
`localrepo.CreateShelf` in a `.tmp-<id>` folder (fsynced files and dirs,
then one rename; stale temps swept after an hour), so `PruneObjects` never
sees shelf content and `DropShelf` removes a folder. `Create` refuses a
pending merge, revert or shelf apply, picks changed paths (status
candidates; untracked only when named exactly or with `IncludeUntracked`),
makes the snapshot content of the tracked ones available first
(`ensureSnapshotContent`), writes the shelf, and only then unstages,
restores tracked files (`WorkingCopy.restoreSnapshot`, Restore's core) and
removes new files with their emptied folders. `Apply` refuses entries with
local changes or outside the sparse set, then runs `applyThreeWay` (base =
the shelved base, ours = the synced snapshot, theirs = the shelf; a
`shelfOverlay` serves shelf chunks, `noDownload` refuses fetches), unstages
what the shelf had unstaged and every conflict, and either drops the shelf
(unless keep) or saves `shelf_state` (`ShelfState`: applied paths,
conflicts, kinds, keep). While it is pending, status reports
`pending: shelf`, `Conflicts.List` answers op `shelf` (`ConflictSides`:
base `shelf_base`, ours snapshot, theirs `shelf`), `Resolve` does not stage
and the last resolution clears the state and drops the shelf unless kept;
`Resolve` (one file from the snapshot or the shelf, offline),
`Continue` (text free of markers; binary and delete/modify conflicts must be
resolved with a side first) and `Abort` (applied paths back to the
snapshot, shelf kept) end it; push, merge, revert, shelve and apply refuse
(`refuseShelfPending`) and the cache GC skips. `Diff.ReadBlob` serves
`BlobShelf`/`BlobShelfBase` (base from the shelf when stored, else from the
base commit).

