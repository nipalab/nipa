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
  (`~/.config/nipa/config.json`: `diffExternal`, `uploadWorkers`,
  `downloadWorkers`,
  `uploadWindowMiB`, `llm`), `difftool`
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
`runPool` and trust a fresh `stat_cache` fingerprint before hashing a file,
then remove the folders they emptied; a file that became a folder or a
folder that became a file is freed earlier (file/folder swaps, below).
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
`MissingChunks`. The guard is the early, readable refusal; the writes
themselves go through a `workTree` (`usecase/work_tree.go`): one `os.Root`
opened lazily on the clone root (created first when missing; the root path
itself may be a link) and owned by the `guardWrites` scope, so an operation
opens it once and closes it when the scope and the last holder (a
`directPlan` until `cleanup`) release it; outside a scope `acquireWorkTree`
returns a private one for the call. A file's steps resolve its folder once:
`workTree.dir` opens it as a sub-root through the root (`Root.OpenRoot`,
`MkdirAll` only when missing) and the temp create, read-only probe, chmod
and rename then name only the file, then the sub-root is closed (one extra
handle per file in flight; resolving through the root on every step instead
cost ~2x path-based syscalls, a per-file sub-root measured ~+8 us per file, ~20%
on a 5000 x 4 KiB local materialize on tmp, and no extra cost per chunk).
Through it run `materializeFile`, `createSizedTemp` and the `directPlan`
(temp handles reopened by `tempHandles.openFile`, chmod in `complete`, rename
in `finish`, removal in `cleanup`, the latter three resolving through the
root), `guardedRemoveCached` (Lstat, hash read, removal; a tracked file
replaced by a link out of the clone is kept, like a local change),
`removeWorkingFile` (file and emptied parents), `Conflicts.Resolve`/`write`
(temp, chmod on the handle, rename, delete) and `sweepMaterializeTemps`
(walks `Root.FS`). `os.Root` has no CreateTemp, so `createTempIn` opens
`prefix+<random uint32>` with `O_CREATE|O_EXCL` (0600, the name shape
`isMaterializeTemp` expects).
`replaceIn` takes a `replaceFS` (`*os.Root`, or `pathFS` for
`SaveProvenance`'s path-based `replaceFile`); the Windows read-only dance
(chmod dst, rename, chmod back) runs through it too. A folder swapped for a
link after the guard verified it makes the runtime refuse the step
(`path escapes from parent`, unexported, matched by message in
`isRootEscape`); `refuseEscape` turns it into a 400 `refusing to write|remove
%q: a folder on the way to it is a link that leads out of the working copy`.
Limits: `os.Root` still follows a swapped-in link that stays inside the
root (e.g. `-> .nipa`), only the up-front guard refuses those, so a race can
redirect a write within the clone but never out of it; an open root or
sub-root keeps referring to its folder if that folder is *moved* (renamed)
elsewhere mid-write, which `os.Root` does not prevent; on Unix `Root.Chmod`
may act on a leaf swapped for a symlink (used only on our own temp in
`directPlan.complete`). Reads of working files (`salvage`, status hashing,
`statEntryFor`) stay path-based. Root-level `.nipaignore`
(`ignore.AppendRules`) and `.nipaprovenance.yml` (`SaveProvenance`) are
written temp+rename, replacing a link rather than writing its target. A
linked `.nipa` (or `.nipa/objects|writes|shelves`) is refused when the local
repo opens (`docs/internals/localrepo.md`). A repo that already tracks such a path
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

Up-to-date early exit: `Update.RunResult` (`Run` is the same call without the
`UpdateResult`) resolves the branch head (`GetBranchByName`) and returns
`UpdateResult{UpToDate: true}` right there, with no manifest fetch, no
`syncWorkingCopy` (snapshot load + per-file stat), no tree/commit save and no
`pruneAfterSuccess`, when all of these hold: the update is not forced; the
head id is non-empty and equals `LoadCommit().CommitID`; and the synced-tree
record (`LoadSyncedTree`, `docs/internals/localrepo.md`) names that same commit
and a sparse set equal to `cfg.Sparse` (`sameSparseSet`: order, duplicates and
empty entries ignored). Detached (`runDetached`) the target is the checked-out
commit itself, so the test is only "not forced + record matches the local
commit and sparse set"; connect and the login check still run first, as on
every update. No record (older clone, tree saved by merge/revert) always means
the full path. The record is written with the manifest's view token
(`saveSyncedTree` -> `SaveSyncedTreeView(tree, commitID, sparse, view)`) in
every flow that saves a manifest fetched with `cfg.Sparse`: clone, `Run`,
`runDetached`, `Switch`, `SwitchTag` and the post-push save (commit = the
pushed commit; `localrepo.ApplyPush` writes the same record when the push is
applied locally and keeps the token of the record it started from, and
`localrepo.ApplyTreeDelta` writes it with the token of the delta answer, see
below). Merge and revert keep plain `SaveTree`, which drops the
record, because their trees are fetched unscoped or built locally. The commit
id is part of the record (not just the sparse set) because the local commit
can move without the stored tree: push saves its commit before it saves
the tree, and `Switch`/`SwitchTag` save the tree before config and commit, so
an interrupted run leaves commit and record disagreeing and the next update
syncs fully. Consequence, by design: the early exit does not restore a tracked
file deleted from disk; `--force` (or `nipa restore`) does. The CLI prints
`Already up to date.`; `sparse-checkout`, the MCP `update` tool and the
daemon call `Run` and stay silent. MCP passes its `force` input through
(`WithForce(in.Force)`), so an unforced MCP update can exit early too; only
the daemon is always forced (never early). A sync that will write or remove
anything drops the record first (`ClearSyncedTree`, via the optional
`syncedTreeClearer` in `syncWorkingCopyOpts`, after the local-changes guard
and before the first write), so a sync that fails or is killed part way,
forced or not, leaves no record and the next update syncs fully instead of
exiting early on a half-written working copy.

Delta sync (`Update.syncTo`, `usecase/sync_delta.go`, span `sync.tree`):
`Run`, `runDetached`, `Switch` and `SwitchTag` all move the clone through
`syncTo(goal)`, which costs work in proportion to the change when it can.
- *Request*. The target commit is known before the manifest is asked for
  (branch head from `GetBranchByName`, the tag's commit, the checked-out
  commit), and the manifest is requested by commit id through the optional
  `deltaManifestClient` (`GetTreeManifestDelta`; only a branch without
  commits is asked by branch name). When the synced-tree record names a
  commit, was fetched with a sparse set equal to `cfg.Sparse` and carries a
  view token (`deltaBase`), the request names it as the base: commit id, the
  stored view token and the stored root hash (`SnapshotTreeHash`, which lets
  the server vouch for the root hash of the result). Otherwise no base is
  sent and the full manifest comes back with its token; the span says why
  (`nipa.sync.tree_sync` = `full`, `nipa.sync.tree_sync_reason` = `record`
  no record, `sparse` the sparse set changed, `view` no token stored, `empty`
  no target commit, `client`/`repo` a transport or repository without the
  delta calls, which is what the test stubs and older servers' answers look
  like). A base equal to the target is fine: the delta is an unchanged root.
- *Declined*. A full manifest in answer to a request that named a base
  (unknown base commit, the view token is no longer the caller's view) is
  used as it is through the full path below (`declined`); no second request.
- *Delta*. `syncDelta`: (1) `PlanTreeDelta` works out, from the stored rows
  of the changed directories only, the files the delta writes and removes
  (`docs/internals/localrepo.md`); (2) `syncDeltaWorkingCopy` builds a
  `syncPlan` from just those and runs it; (3) `ApplyTreeDelta(in, planned)`
  writes the rows, `tree_hash` and the record in one transaction. The order
  is the full sync's: working copy first, rows after, with the record dropped
  in between, so a refused or interrupted sync leaves the stored tree at the
  base and no record. Span attribute `nipa.sync.tree_sync` = `delta`.
- *Silent fallback*. A delta that leads to another commit than the one asked
  for (`commit`), a plan or apply that reports the stored tree is not the
  base (`base`: `ErrTreeDeltaBase`, e.g. an unchanged file the tree does not
  hold), a root hash that does not match (`mismatch`), or any other plan or
  apply failure (`error`) makes `syncTo` fetch the full manifest (no base)
  and run the full path; `slog.Debug` carries the hashes, the user sees
  nothing. When the apply fails after the working copy was synced, the full
  sync finds the files in place. Errors that are not fallbacks, exactly as in
  the full sync: a failed request, local changes in the way, an unsafe path,
  download and write failures.
- *What the working copy sync looks at* (`syncDeltaWorkingCopy`). The same
  `syncPlan.run` as the full sync (`syncWorkingCopyOpts` builds its plan from
  the whole manifest and snapshot, the delta from `TreeDeltaChanges`), so the
  guards and mechanics are shared: `checkWorkingPath` on every written path,
  `checkSyncTargets` (no write or removal below a link), the local-changes
  guard on the files it replaces or creates (`localChangeConflicts`, which
  now reads only those fingerprints with `statEntriesFor`), `ClearSyncedTree`
  before the first write, pins and direct writes, downloads, `stat_cache`
  rows of the written files, `removeLeftFiles` (a locally changed file the
  target removes is kept on disk). It does not load the snapshot or the stat
  cache, and does not stat untouched files: like the up-to-date exit, an
  unforced delta sync does not restore a tracked file deleted from disk, and
  a local edit of a file the delta does not touch is never in the way.
  Sparse checkouts need nothing special: base and target are fetched with
  the same sparse set (the view token and `deltaBase` both check it), and
  ignore rules never applied to sync (tracking wins).
- *Chunk scope*. The download of a delta sync names the written files as its
  scope paths (`narrowChunkScope`, up to 256 files, else the checkout's own
  scope): the server's `VisibleChunks` walk then covers only the directories
  leading to them instead of the whole commit tree.
- *Forced* (`WithForce(true)`: `--force`, MCP `force`, always the daemon).
  The delta is still used for the manifest and the rows. The local-changes
  guard is off as before, and `syncOptions.restoreMissing` keeps the forced
  sync's promise that every tracked file exists afterwards: it loads the
  snapshot once, stats every untouched tracked file and writes the missing
  ones back from the snapshot rows (`materializedFromSnapshot`, through the
  object cache). So a forced update still reads all file rows and stats all
  files, but no longer transfers and decodes the manifest, diffs every row
  (`syncRows`) or sweeps the object cache; it never exits early. That is
  what the daemon gains: its updates stay O(files) on the client for the
  stat pass and the snapshot read only.
- *Prune*. A delta sync returns a `pushCacheEffect` (the chunks of the rows
  it replaced or removed and of the files it wrote), and `Run`/`Switch`/
  `SwitchTag` prune through `pruneAfterPush`: only those objects are checked
  (`pruneChunks`), with the full sweep as before when more than
  `maxTargetedPruneChunks` need a lookup and after every full sync.
- *Left on full manifests*: clone (no base exists; it now asks through
  `fetchManifest` to get the token), the post-push refetch
  (`saveLandedTree` fallback; same, by the pushed commit's id),
  `Update.TargetTree`/`SyncPlan` (a read-only preview that needs the whole
  target tree to list overwritten paths), merge and revert (they fetch
  unscoped head trees of two branches to build a three-way result and save
  with plain `SaveTree`, so there is no recorded base), and
  `restore`/`RestoreRevision` (path-scoped manifests of an arbitrary
  commit, not a move of the clone).
- *Known limits*. A view token is only stored from a fetched manifest: a
  clone of an empty branch has none until its first full update, also after
  its own first push.

File/folder swaps (`usecase/sync_swap.go`): a path that is a file in one
commit and a folder in the other is freed by `syncPlan.run` for the full and
the delta sync alike (`TestSync_FileFolderSwaps`, and swaps are generated in
`TestEndToEnd_DeltaUpdateProperty`). Before, writes ran before removals, so
a written file's temp/`MkdirAll` hit the old file ("not a directory") or its
rename hit the old folder ("file exists"). `planSwaps` runs after
`checkSyncTargets` and before `localChangeConflicts`, forced or not: one
`Lstat` per written file and per distinct folder above one finds a file
where a folder is needed (from the top; it must be a removal of this sync)
and a folder where a file is written (walked without following links: its
files must be removals of this sync). Those tracked files must hold no local
change (`trackedFileChanged`: stat fingerprint, else hash, the rule
`guardedRemoveCached` keeps files by); anything else (an untracked or ignored
file, an edited tracked file, a link) refuses the sync with a 400 listing up
to 20 paths, before anything is written, **also with `--force`**: a forced
sync overwrites edits at paths it writes but never deletes a changed file the
target drops, and a swap cannot keep it. Empty folders hold nothing and go.
Execution keeps the "nothing until the download is done" rule as far as the
layout allows: `removeBlocking` deletes the files where folders are needed
right after `ClearSyncedTree` and before the direct plan creates temp files
in those folders (so before the download); `clearFolders` deletes the
folders a file replaces (tracked files, then folders deepest first, a
read-only folder made writable first) after the download, right before
`materializeAll`/`directPlan.finish`; `swapPlan.left` drops what they took
from the final removals. An interrupted or failed sync leaves the stored tree
at the base with no record, so the next update is a full sync, which treats a
removed path as gone: `guardedRemoveCached` answers "gone" for a path below a
file (`isNotDir`, which also matches `os.Root`'s errno-less "not a
directory") and keeps a folder that stands where a tracked file was.
`removeLeftFiles` then prunes the folders its removals emptied
(`pruneEmptiedFolders`, deepest first, never the root, never a link), so the
working copy has the folders a fresh clone has. `applyThreeWay` (merge,
revert, shelve apply) plans and frees swaps the same way after its download
and before its writes (a removal taken counts as deleted and staged);
`Restore` (and shelve's put-back) refuses content in the way and removes an
empty folder; `RevisionRestore` refuses a file where a folder is needed;
shelve create removes new files before restoring tracked ones. On the
staging side a tracked file that is now a folder, or below a path that is a
file now, reads as missing (`statTracked`/`isMissing` in status, add and
`classifyStaged`), so a local swap stages and pushes.

Half a swap (the path is staged, what it displaced is not) would leave a name
as a file and as a folder; the server refuses that tree (`buildNewTree`, see
`server.md`). The client avoids and catches it:
- `nipa add` (`stageDisplaced` in `expandAddTargets`) stages what an existing
  target displaces, the way a missing target stages what was tracked there
  (`stageTrackedTarget`): a file target that is not tracked as a file stages
  every tracked file below it (`add a/x` after the folder `a/x` became a file
  stages the deletions of `a/x/*`), and any target stages the tracked files
  that are folders on the way to it, itself included when it is a folder
  (`add a/b/c` or `add a/b` after the file `a/b` became a folder stages the
  deletion of `a/b`). Those paths cannot exist in the working copy any more,
  so staging them is what the working copy says, and what `add .` already
  did. Cost: the target's row and its parent folders' rows in one batched
  read, plus one index seek below an untracked file target (its tracked
  files are listed only when there are any).
- `nipa push` and its plan (`Push.checkStagedTree` -> `stagedTreeClashes`,
  `push_clash.go`) run right after `classifyStaged`, before a chunk is read
  or uploaded, and refuse with a 400 naming each path, up to 20 of the tracked
  files left below it, and the `nipa add <path>` that fixes it. It covers a
  staged list built some other way (`nipa remove` of the deletions, the daemon
  staging single paths). Cost: the rows of the staged files' parent folders in
  one batched read (the staged rows come from `stagedBase`), one index seek
  below each written file that is not tracked yet, and nothing at all in a
  repository that tracks nothing; the staged list and the stored tree are
  untouched by a refusal.
- `localrepo.ApplyPush` (`planPushApply.rebuild`) refuses the same tree with
  `clientDomain.PushApplyClashError` and writes nothing, so the local mirror
  of `buildNewTree` cannot store what the server would not.
- A sync whose writes hold a path as a file and as a folder (a commit stored
  before the server check) stops in `checkWriteLayout` (`checkSyncTargets`)
  before anything is written, naming the paths, instead of failing halfway on
  a rename with "file exists". An update of an existing clone onto such a
  commit is refused by `planSwaps`; `markKeptTracked` then labels the tracked
  files the target keeps and says the commit is damaged rather than telling
  the user to move them.

Not handled: case-insensitive renames (`A` -> `a`) are not treated as swaps;
`internal/merge.ThreeWay` has no file/folder conflict, so a
merge where one side edits `a/b` and the other turns it into `a/b/c` is
refused by the swap check instead of reported as a conflict.

Push reads (`Push.stagedBase`): `Run` (through `pushStagedEffect`) and `Plan`
read the tracked rows of the staged paths (`SnapshotFiles`) and the tree hash
(`SnapshotTreeHash`) instead of `Snapshot()`; a repository without those reads
gets the whole snapshot, with the same error precedence (snapshot, staged
list, "nothing staged").

Post-push prune (`pruneAfterPush`, `push_prune.go`): a push that was applied
to the stored tree locally (`saveLandedTree` returned `applied`) and did not
conclude a merge or revert changed only the rows of the paths it sent, so the
objects it can have orphaned are known: the chunks of the previous rows of the
pushed and removed files, and the chunks of the rows it wrote (the push
caches text and pinned raw content; an exact pin that lands is released, so
that content loses its keeper). `pruneChunks` checks only those: candidates
without an object are dropped first (`MissingChunks`; when none is cached the
prune is done, without the lock), then, under the same guard as the full sweep
(`pruneGuard`: exclusive lock without waiting, no pending merge/revert/shelf
state), the rewritten rows settle the chunks they keep, every remaining chunk
is looked up with `FilesWithChunk`, and an object is deleted (`RemoveObjects`)
only when no tracked file that `cachedInPristine` accepts lists it: the keep
rule of `pruneCache`, restricted to the candidates, so it never deletes what
the full sweep would keep. The full sweep still runs after a push that
concluded a merge or revert (their materialized content has no row), that
saved a fetched manifest (sparse checkout, `base`/`mismatch`/`error`
fallback), that leaves more than `maxTargetedPruneChunks` (16) chunks to look
up, and for repositories without the by-path reads. What the targeted prune
does not collect, and the next full sweep does (clone, a real update, switch,
tag switch, restore, `nipa diff`, lock pins, the daemon's idle pruner, `nipa
gc`, or one of the pushes above): objects no row of this push referenced
(chunks an earlier failed push cached for content that was edited or unstaged
since, content a daemon blob/preview read left behind), stale `.tmp-*` files,
and the orphans of a push whose prune was skipped (repository busy or a
pending state). `nipa gc` is unchanged.

Post-push tree save (`Push.saveLandedTree`, span `push.tree`): after the
server accepted the push and `SaveCommit` pinned the new commit, the stored
tree is brought to the new head without fetching its manifest. The server
only accepts a push onto the branch head, so the new tree is the base tree
plus the pushed files minus the removed paths, and `localrepo.ApplyPush`
(`docs/internals/localrepo.md`) derives it from the stored rows and checks its
root against `PushResult.TreeHash`. The manifest is still fetched
(`fetchManifest` by the pushed commit's id + `saveSyncedTree`, which records
the view token; `GetTreeNodeManifest` + `SaveSyncedTree` for a client without
the delta call) in exactly
these cases, silently (`slog.Debug` with the expected and computed hashes;
span attributes `nipa.push.tree_sync` = `local`/`refetch` and
`nipa.push.tree_sync_reason`): `sparse`, a sparse checkout (`cfg.Sparse` not
empty) never applies locally; `base`, the synced-tree record does not name the
base commit the push was sent with as a full checkout (older clone, crash
between `SaveCommit` and the tree save, a tree `SaveTree`d by a conflicted
merge/revert or `--no-commit`), or, with no base commit, anything is stored;
`mismatch`, the computed root differs from the server's (a permission-filtered
manifest, a server tree still carrying legacy pre-mode directory hashes, any
disagreement); `error`/`config`, the apply or the config read failed. The
commit already landed, so no failure of the local apply fails the push: only
the manifest fetch and its save can. Callers: plain `nipa push`, clean
reverts (each target of a sequence) and clean three-way merges push a delta
onto a synced head and apply locally; the push that resolves a conflicted
merge or revert falls back (`base`). `ClearStaged` and the pin release follow
either path. A crash between `SaveCommit` and the tree save leaves the
record naming the old commit, so the next update syncs fully and the next
push falls back.

Local changes guard (see also the file/folder swap check above, which no
`--force` turns off): `Update.Run`/`Switch`/`SwitchTag` sync with
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
`nipa add *` on a large clone is cheap. Add reads only what its targets name
(`trackedScope`, `tracked_scope.go`): a file target costs its `files` row
and its `stat_cache` row (`SnapshotFiles`, `StatEntries`), a directory target
one range of the snapshot (`SnapshotFilesUnder`, which also yields the
deletions to stage and the walk's ignore exemptions) plus the fingerprints of
the tracked files below it, and only the root (`.`), or a repository without
the by-path reads (`scopedSnapshotRepo`; the test stubs), loads `Snapshot()`
and `LoadStatCache()` as every add did before. The ignore exemption is asked
lazily (`newLookupIgnoreState` + `trackedScope.protects`), only for paths a
rule ignores; a failed lookup is returned, never read as "ignored".
`recordProvenance`, `forgetProvenance` (`nipa remove`) and
`RefreshStatEntries` (the daemon watcher) use the same scope. On a
100,000-file clone a one-file add is two indexed reads (`make
bench-small-change`). The matcher is client-side only: a
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

