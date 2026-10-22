# Git import (`nipa git-import`)

Design for importing a Git repository into Nipa. Status: **planned, not
implemented**. This document is the implementation brief.

## Goal

A one-shot CLI importer that replays the tip and the last N commits of one Git
branch into an existing Nipa project:

```
nipa git-import <git-repo> <org/project> [--branch <name>] [--depth N] [--state <file>]
```

Preserved: file and binary content, commit messages, commit ordering (linear
first-parent chain), file modes (executable bit), and — via message trailers —
the original author and date. Everything is transferred through the normal
client transport, so server ACLs, path filters, quota and chunk accounting
apply exactly as for a regular user.

## Why client-side

Nipa's push model is "apply a file delta against a base commit"
(`internal/usecase/push.go`, `Push.Push`; wire format in
`internal/grpc/proto/server.proto`, `PushRequest`). A migrator can therefore be
a plain client: for each Git commit, diff its tree against the previously
imported one, upload the changed blobs as chunks, and push the delta.

A server-internal importer (direct `PushRepository.ApplyPushAll`, like the MR
rebase replay in `internal/usecase/merge_strategies.go`) would be faster and
could preserve real timestamps/authors with schema additions, but it requires
filesystem access to the Git repository and bypasses the permission and lock
gates. It is deliberately out of scope here.

No `.nipa` working copy and no temp materialization on disk: blobs are read
from the Git object database and chunked in memory.

## Prerequisites

- The org and project already exist, and the target branch has **no commits
  yet**. Org/project creation is REST-only today (`POST /api/v1/orgs`,
  `POST /api/v1/orgs/{org}/projects`; `Project.Create` also creates the empty
  default branch), so the importer stays gRPC-only and documents "create the
  project first" (web UI).
- The user is logged in (`MakeSureLoggedIn`) and has write permission on the
  project.
- The imported branch must not be protected; a protected default branch
  rejects direct pushes with "push through a merge request". Import into a
  freshly created project (whose default branch is unprotected) or protect the
  branch only after the import.

## Dependency

Add `github.com/go-git/go-git/v5` (Apache 2.0) to `go.mod`. It provides pure-Go
packfile/ref/tree/blob access, so no `git` binary is required on the client.
The alternative (shelling out to `git log`/`git cat-file`) adds no dependency
but requires Git installed; rejected for portability.

## Flow

1. Open the repository with go-git; resolve `--branch` (default: the
   repository's HEAD branch).
2. Walk the first-parent history from the branch tip, collect up to `--depth`
   commits (default N, e.g. 100), then replay them **oldest → newest**.
   `--depth` counts commits, not days; the root of the imported window is
   imported as a full-tree root commit.
3. For each commit:
   - Build `path -> (blob, git mode)` by walking the commit tree recursively.
     Skip symlinks (mode `120000`) and submodules (`160000`) and record a
     warning per skipped path.
   - Diff against the previous commit's map to get changed and removed paths
     (the first commit treats every path as added).
   - For each changed path: read the blob, probe text/binary with the same
     heuristic push uses, `chunker.Encode` with `chunker.ConfigForFile`, and
     derive `FileHash = chunker.FileHash(chunkHashes)`. Stream the chunks
     through the existing gRPC `UploadChunks`; the server answers
     `already_stored` for chunks it has, so content unchanged across history
     is uploaded once.
   - Build `pb.PushFile`s (git `100755` -> `EXECUTABLE`, `100644` -> default
     `0644`) and call gRPC `Push` with:
     - `base_commit_id` = the previously imported Nipa commit (base36),
       omitted for the first push into the empty branch. The empty-head path
       is supported: with no base commit and no branch head, the empty tree
       hash matches (`internal/usecase/push.go`, base validation).
     - `message` = the transformed commit message (see trailers).
     - `removed_files` = the deleted paths.
   - Record `git sha -> {nipa commit id, nipa commit hash, nipa tree hash}` in
     the state file.
4. Print a per-commit line and a final summary (`N commits imported, M files,
   warnings`).

Merge commits: shallow mode replays the first-parent chain, so each imported
commit has exactly one parent and the tree snapshot already includes the
merged content. `parent_2_commit_hash` is intentionally not sent — it must
reference an already-imported commit of the same project, which a second
parent outside the imported window cannot satisfy.

## Commit message trailers

Git authorship and dates cannot be set through the API: a commit is attributed
to the authenticated user (`commits.user_id`) and `created_at` is server-set at
insert. The message is the only preserved free-form field (and it is part of
the Nipa commit hash, so it stays stable). Preserve the origin metadata there:

```
<original subject and body>

Original-author: Ada Lovelace <ada@example.com>
Original-date: 2024-03-01T12:34:56+01:00
Git-commit: 3f9c1b2…(full 40-char sha)
```

`Original-author` comes from the Git author (not committer), `Original-date`
from the author timestamp in RFC 3339, `Git-commit` from the full SHA. The
trailer block is appended only if not already present (resume safety). Keep
this unconditional: it is the documented fidelity contract, and the
`Git-commit` trailer is what makes the Git → Nipa mapping auditable.

## State and resume

`--state <file>` (default `<git-repo>/.nipa-git-import.json`, kept out of the
Git working tree) stores:

- target org/project/url and branch,
- the Git head SHA the import started from,
- the ordered `git sha -> nipa commit id/hash` map of completed commits,
- skipped paths (symlinks/submodules) and their warnings.

On start, an existing state file for the same target resumes after the last
completed commit; the importer verifies the branch head still equals that
commit before continuing and aborts with a clear error otherwise. The state
file is written after every successful push (atomic temp+rename).

## What is not preserved

| Git | Why / Nipa behavior |
| --- | --- |
| SHA-1 object ids | Nipa hashes are BLAKE3 over its own tree/commit serialization (`internal/treehash`); recorded as a `Git-commit:` trailer and in the state map |
| Author / committer identity | Commits belong to the authenticated user; author preserved as a trailer |
| Timestamps | `created_at` is server-assigned; preserved as a trailer |
| Symlinks | No entry type on the wire; skipped with a warning |
| Submodules | Gitlinks are not tree content; skipped with a warning |
| Rename/copy metadata | Nipa stores content-addressed trees; renderers do rename detection |
| GPG signatures, notes, stash, refs beyond the chosen branch | Out of scope |
| Branches and tags | Out of scope for shallow v1; `CreateBranch` already accepts `from_commit_id` and tags can point at any commit, so a later `--full`/`--all-branches` extension is additive |

## Non-goals

- Incremental re-import ("sync from Git later"). The state file deliberately
  records the mapping so a future extension can diff a newer Git tip against
  the last imported SHA and push only that range.
- Reconstructing Nipa-level merge requests from Git pull requests.
- Preserving `.gitattributes`, LFS pointers (LFS smudge happens before the
  importer sees the blob only if the user checked out LFS content; pointers are
  imported verbatim), or hooks.

## Implementation notes

- **New package** `internal/client/gitimport/`:
  - `importer.go` — repo opening, first-parent walk, tree diff, chunking,
    push loop. Depends only on a small transport interface (the same shape as
    `pushClient` in `internal/client/usecase/push.go`: `Connect`, `Push`,
    `UploadChunks`, `GetTreeNodeManifest`) so tests can stub it; the real
    implementation is `internal/client/grpc.Client`.
  - `trailers.go` — message assembly/normalization.
  - `state.go` — JSON state load/save (atomic write).
- **CLI**: `internal/client/cli/gitimport.go`, registered next to the other
  commands in `internal/client/cli/root.go`. Standard lazy login
  (`MakeSureLoggedIn`), `organization/project` parsed with the existing
  `clientDomain.ParseNipaUrl`-style handling or org/project flags. There is no
  `CreateProject` gRPC RPC; do not add one for this tool.
- **Chunking**: always use `chunker.Encode`/`chunker.ConfigForFile` and pass
  the same probe used by push, so stored content hashes stay consistent with
  every other client path. Never hash raw bytes directly.
- **Delta bookkeeping**: keep the previous commit's `path -> blob hash` map in
  memory; the Git blob SHA is enough to detect unchanged files without reading
  content.
- **Large histories**: chunk streaming is bounded by the existing
  `UploadChunks` batching (16 MiB windows, two uploader goroutines) — the
  importer must not accumulate all blobs of a commit in memory; materialize
  one file at a time.

## Tests

- `internal/client/gitimport` unit tests with a fixture repository built in
  `t.TempDir()` through go-git plumbing (root commit, edits, a deletion, an
  executable file, a merge, a symlink). Use a stub transport (gomock or a
  hand-written fake) and assert:
  - replay order and `base_commit_id` chain,
  - only changed paths are pushed per commit, deletions use `removed_files`,
  - trailer formatting (and idempotence on resume),
  - executable mode mapping, symlink/submodule skipping plus warnings,
  - state file round-trip and resume behavior; a moved branch head aborts.
- Keep the importer independent of cobra so tests need no CLI.
- `go vet ./...`, `go test ./...`, `make lint` clean. An e2e case in
  `tests/suite` (build a small Git repo, import, clone, compare files and
  commit count) can follow once the command exists.

## Docs

Add an "Importing from Git" section to the README pointing at this document:
create an empty project, run the command, expected fidelity and limitations.
