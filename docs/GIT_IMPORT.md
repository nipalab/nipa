# Git import (`nipa import git`)

Design for moving a Git repository (history, branches, tags, LFS content)
into a Nipa project. Status: **planned, not implemented**. The phased
implementation plan is `docs/plans/GIT_IMPORT.md`.

This replaces the earlier shallow, push-based brief. That approach replayed
Git commits as ordinary `PushStream` pushes. It breaks on the repositories
Nipa targets:

| Problem with replaying as pushes | Where |
| --- | --- |
| Modifying a tracked binary needs the pusher's lock, so every historic edit of a `.png`/`.fbx` would need a lock first | `Push.Push` → `EnsureLocks` (`planPushLocks`) |
| Every commit fires `push` webhooks, MR bookkeeping and review dismissal | `Push.Push` tail (`reviews`, `hooks`) |
| A push always lands on a branch head, so a commit DAG (branches, merges) needs scratch branches and fork tricks | `pushBranch`, `checkBaseCommit` |
| Commits belong to the importer with `created_at = now`; the Git author and date survive only as message text | `commits.user_id`, `created_at` default |
| Two Git commits with the same tree, parents and message (e.g. "WIP" on two branches) get the same Nipa hash, and `UNIQUE(project_id, hash)` rejects the second | `treehash.CommitHash(tree, parents, message)` |
| LFS pointers would be imported as 130-byte text files, which defeats the point for game repos | none |

The new design keeps the **client-side reading of Git and the normal chunk
transfer** (presign, possession proofs, quota and dedup all apply unchanged).
It adds a **dedicated server import path** that writes commits with explicit
parents and origin metadata, and moves refs only at the end.

## Goals

- Import full history or a window of it: every selected branch and tag, with
  the merge DAG kept (two-parent merges).
- Keep the original author name, email and date as first-class commit fields,
  shown by `nipa log`, the web UI, the desktop app and blame.
- Resolve Git LFS objects into real content (stored like any Nipa binary).
- Resume and incremental sync: a re-run imports only commits that are not on
  the server yet. This supports a "mirror until cutover" migration.
- No `git` binary on the client (pure Go via `go-git`), no working-tree
  checkout, and bounded memory on large histories.
- Same behavior on `nipad` and `nipad-ee`.

## Non-goals

- Two-way sync, or exporting Nipa back to Git.
- Importing pull requests, issues or reviews from GitHub/GitLab.
- Server-side "import from URL" in v1. The core is designed so a later
  server job can reuse it (phase 6 of the plan).
- Rewriting history (filter-repo-style path removal). Users run
  `git filter-repo` first if they need it.

## User-facing flow

```
nipa import git <git-path-or-url> <nipa-url>
    [--refs <pattern>,...]        # default: refs/heads/*,refs/tags/*
    [--default-branch <name>]     # default: the Git HEAD branch
    [--depth N | --since <date> | --snapshot]
    [--lfs auto|local|off]        # default auto
    [--symlinks skip|file]        # default skip
    [--author-map <file>]
    [--source-key <name>]         # origin source part; default "default"
    [--update]                    # incremental: fast-forward existing refs
    [--dry-run] [--json] [--yes]
```

1. Create the project first (web UI or REST). Import needs **project admin**.
2. `nipa import git ../game https://nipa.example.com/acme/game --dry-run`
   prints a report and writes nothing:
   - commits, refs, distinct blobs and total bytes;
   - LFS objects, and how many are missing locally;
   - paths Nipa rejects, symlinks, submodules, octopus merges;
   - estimated upload size against the remaining quota.
3. Run it without `--dry-run` to import, with progress (commits/s, MiB/s,
   ETA). Ctrl-C is safe: the next run resumes.
4. Later runs with `--update` import new Git commits and fast-forward the Nipa
   branches. A Nipa branch that moved on its own (someone pushed in Nipa) is
   reported as diverged and skipped. It is never overwritten.

A remote URL (`https://…`, `git@…`) is fetched with go-git into a bare cache
under the user cache dir (`<cache>/nipa/git-import/<hash>`) and reused by
`--update`. Credentials come from the URL, `GIT_ASKPASS`/the credential
helper (`git credential fill` when `git` exists), or an SSH agent.

## Architecture

```
            client (nipa)                                   server (nipad / nipad-ee)
┌───────────────────────────────────────────┐      ┌──────────────────────────────────────┐
│ internal/gitimport (pure core)            │      │ usecase.Import                        │
│  Source ── go-git repo, LFS resolver      │      │  Begin   (admin, writable store,     │
│  Planner ─ ref select, topo order, window │      │           maintenance=write)          │
│  Converter blob→chunks cache, tree delta  │      │  Lookup  origin sha → commit          │
│  Sink (interface)                         │      │  Commit  explicit parents + author,   │
└──────────────┬────────────────────────────┘      │          delta vs parent 1 tree,      │
               │ client Sink                       │          no branch move, no locks/hooks│
┌──────────────▼────────────────────────────┐ gRPC │  SetRefs branches/tags/default,      │
│ internal/client/gitimport/sink.go         ├──────►          ff-only on --update, audit   │
│  UploadChunks (existing presigned path)   │      └──────────────────────────────────────┘
│  ImportStream / ImportLookup / SetRefs    │
└───────────────────────────────────────────┘
```

### Core package `internal/gitimport`

This package is pure: no client or server imports, and only `internal/chunker`,
`internal/treehash` and `internal/domain` hash helpers. It can run in the CLI
now and in a server job later.

- `Source`: opens a go-git repository (`PlainOpen`, or a bare cache after
  `Fetch`) and exposes refs, commits, trees and blob readers.
- `Planner`:
  - Selects refs by `--refs` patterns. Annotated tags are peeled to their
    commit and keep their message.
  - Builds the commit set reachable from those refs, cut by
    `--depth`/`--since`. A parent outside the window is dropped, and a commit
    left with no parent becomes a root.
  - Orders the commits topologically, parents before children (Kahn's
    algorithm, ties broken by committer time).
  - `--snapshot` imports one root commit per ref tip.
- `Converter`: turns a Git commit into a `Commit` event:
  - `Origin` (`git:<source>:<sha>`, see "Origin format"; `source` is
    `default` unless `--source-key` is given) and `Parents []Origin` (at most two; an
    octopus keeps the first two and reports the rest as a warning).
  - `Author{Name, Email, When}`, `CommitterWhen`, and the message unchanged.
  - `Changes`: the tree diff against **parent 1** through go-git `DiffTree`,
    which skips unchanged subtrees by hash. The output is `File`s (path, mode,
    size, binary flag, encoding, chunk hashes, file hash) plus removed paths.
  - Blob conversion is memoized by Git blob SHA in the state DB, so a blob is
    read and chunked once across the whole history.
  - Content goes through `chunker.ProbeBinary` + `chunker.ConfigForFile` +
    `chunker.Encode`, exactly like push. Hashes never come from raw bytes.
- `Sink` interface: `Lookup(origins)`, `Upload(chunks)`, `Commit(ev)
  (CommitRef, error)` and `SetRefs(refs)`. The CLI uses the gRPC sink; tests
  use a fake.

### Mapping Git to Nipa

| Git | Nipa |
| --- | --- |
| blob `100644` | file, mode `READ_WRITE` |
| blob `100755` | file, mode `EXECUTABLE` |
| LFS pointer blob (`version https://git-lfs…`) | the LFS object content (`--lfs`), path unchanged |
| symlink `120000` | skipped with a warning; `--symlinks file` stores the target path as file content |
| gitlink `160000` (submodule) | skipped; listed in the summary with its URL from `.gitmodules` |
| empty tree / empty commit | allowed: an empty-delta commit keeps its place in the DAG |
| merge (2 parents) | `parent_1_id` + `parent_2_id` |
| octopus (3+) | first two parents kept; the rest recorded in the warning list |
| branch | Nipa branch at the mapped commit |
| lightweight / annotated tag | Nipa tag (annotation message kept) |
| `HEAD` | default branch (overridable) |
| `.gitattributes` `lockable` | nothing to do: the server forces lockable extensions |
| `.gitignore` | imported as-is; the summary suggests `nipa ignore import` |

Paths that `validateRepoPath` rejects make the import stop before any write,
with the full list (dry-run shows them too). Users rewrite them in Git first.
Silently dropping files would corrupt history, so the importer never does it.

### LFS

`--lfs auto` (the default) resolves each pointer in this order:

1. The local LFS store, `<gitdir>/lfs/objects/<oid[0:2]>/<oid[2:4]>/<oid>`.
2. The LFS batch API (`<remote>.git/info/lfs/objects/batch`, `download`
   operation), with endpoint and credentials taken from `.lfsconfig`,
   `lfs.url` and the remote URL, like `git-lfs` does.
3. Otherwise the import fails. The dry-run lists every missing OID.

`--lfs local` skips step 2, and `--lfs off` imports pointers verbatim. The
SHA-256 OID is verified while the object streams into the chunker. Objects
are never held whole in memory.

### Server side

#### Schema

The schema change is one new migration pair (sqlite `000025`, postgres `000012`):

```sql
ALTER TABLE commits ADD COLUMN author_name TEXT;
ALTER TABLE commits ADD COLUMN author_email TEXT;
ALTER TABLE commits ADD COLUMN authored_at <DATETIME|TIMESTAMPTZ>;
ALTER TABLE commits ADD COLUMN origin TEXT;            -- see "Origin format"
CREATE UNIQUE INDEX commits_project_origin ON commits (project_id, origin)
    WHERE origin IS NOT NULL;
```

- All four columns are NULL for native commits, so existing behavior is
  unchanged.
- `user_id` is the matched Nipa user (see authors), else the importer.
  `created_at` stays the server insert time.
- Every reader prefers `author_*`/`authored_at` when they are set:
  - the commit log (`CommitLogEntry.author_name/email` and a new `authored_at`);
  - `CommitDetail` (new `author_name`, `author_email`, `authored_at` and
    `origin` fields);
  - the REST commit DTOs, web history, desktop History, blame and the MCP
    `nipa_log`.

#### Origin format

`origin` is VCS-neutral so later importers (SVN, Perforce, Mercurial, ...)
reuse the same columns, index and hash without another migration. The value
is fixed once written and is part of the commit hash, so the format is a
contract:

```
<system>:<source>:<native-id>[:<qualifier>]
```

| Part | Rule |
| --- | --- |
| `system` | lowercase registered scheme: `git`, `svn`, `p4`, `hg` (new systems are added to the server's registry) |
| `source` | identifies the source repository, so two repositories never share an origin |
| `native-id` | the source system's commit id |
| `qualifier` | optional; required when one native revision becomes several Nipa commits |

| System | Example | `source` | `native-id` | `qualifier` |
| --- | --- | --- | --- | --- |
| Git | `git:default:3f9c…` | import key: `default` for a single-repo import, else the `--source-key` the user chose | full SHA-1 (40 hex) or SHA-256 (64 hex), lowercase | none |
| SVN | `svn:9f1c…-uuid:1234:/branches/x` | repository UUID | revision number | the branch path, since one revision can touch several branches |
| Perforce | `p4:<server-id>:58211://depot/main` | server id (`p4 info` Server ID, else address) | changelist number | the stream or branch root, since one changelist can span several |
| Mercurial | `hg:default:a1b2…` | import key, as for Git | full changeset hash | none |

The server validates every origin, which the PR 3 usecase implements:

- The total length is at most 512 bytes. There are no control characters or
  whitespace except inside the qualifier.
- The `system` is in the registry.
- A per-system rule checks `native-id`: hex length for `git`/`hg`, a decimal
  integer for `svn`/`p4`.
- Anything else is a 400.

Lookups are always by exact origin. The importer computes the origins it
needs, so the index never serves prefix searches.

Extra source metadata (Git committer and signatures, SVN properties, p4 jobs)
is not stored. If it is ever needed, it goes in a later nullable
`origin_meta` JSON column; the origin format does not change.

#### Commit hash

`treehash.CommitHash` gets an origin-aware variant: when `origin` is set, the
hash also covers the origin string (length-prefixed, after the message).
Native commits keep the current formula. Commit hashes are computed only on the
server (`buildApplyRequest`), so no client changes. This fixes the
same-tree/same-parent collision without polluting messages with trailers.

#### Authors

`--author-map` is a mailmap-style file (`Name <git@email> = nipa-user-id`).
Without a map, the server matches the Git author email to **verified emails of
members of the project's org**. Other users are never matched, so the import
cannot be used to probe accounts. Unmatched authors keep `user_id` = the
importer, and the name and email are still displayed.

#### `usecase.Import` (`internal/usecase/import.go`)

- `Begin(projectID)`:
  - requires project admin (`PermissionAll`);
  - checks `Chunk.RequireWritable` (own-storage gate);
  - in phase 2, rejects a project that already has native commits unless the
    session is `--update`.
- `Lookup(projectID, origins)`: returns `origin → {commit_id, commit_hash}`
  for the commits already imported, which makes resume and `--update`
  server-authoritative.
- `Commit(projectID, ImportCommit)`:
  1. Resolves the parents by origin (or nipa id) in this project.
  2. Builds the tree from **parent 1's tree** with `buildNewTree` (shared with
     push). `validatePushFiles`, `validateRemovedFiles` and the file-hash check
     are reused.
  3. Resolves chunks through the same project-scoped link lookup as
     `ApplyPush`, then writes through a new `PushRepository.ApplyImport`.
     This is `ApplyPush` without the branch head update, plus the new columns.
  4. Runs no locks, hooks, MR bookkeeping or per-commit audit.
  5. If the origin already exists, returns the existing commit (idempotent, so
     a retry after a dropped stream is safe).
- `SetRefs(projectID, refs, defaultBranch, mode)`, in one transaction:
  - creates missing branches and tags at mapped commits and sets the default
    branch;
  - in `update` mode, fast-forwards an existing branch only when its head is an
    ancestor of the new commit (`Branch.Contains` walk). Otherwise the ref is
    reported `diverged`;
  - refuses to move protected branches (reported, not an error for the other
    refs);
  - emits one `push` webhook per moved branch (before/after, no file list) and
    `tag.created` per new tag, so CI sees the import exactly once;
  - writes one audit entry, `project.git_imported` (refs, commit count,
    origin head shas).
- Per-project serialization: `Begin` takes the project lock (`LockProject`,
  the advisory lock on postgres, an immediate tx on sqlite) around `SetRefs`
  only. Concurrent `Commit` calls are safe because the origin index makes them
  idempotent.

Commits written but not yet referenced by a ref (an interrupted import) are
invisible to every reader that starts from refs, and the next run reuses them
via `Lookup`. No GC of orphan commits is needed for correctness. A later admin
"discard incomplete import" action can delete them.

#### gRPC (`server.proto`)

```proto
rpc ImportLookup(ImportLookupRequest) returns (ImportLookupResponse);
rpc ImportStream(stream ImportStreamRequest) returns (stream ImportStreamResponse);
rpc ImportSetRefs(ImportSetRefsRequest) returns (ImportSetRefsResponse);

message ImportCommitHeader {
  ProjectContext context = 1;           // first message only
  string origin = 2;                    // "git:<source>:<sha>", see "Origin format"
  repeated string parent_origins = 3;   // ≤ 2
  string message = 4;
  string author_name = 5;
  string author_email = 6;
  google.protobuf.Timestamp authored_at = 7;
}
message ImportCommitEnd {}
message ImportStreamRequest {
  oneof part {
    ImportCommitHeader header = 1;
    PushStreamFiles files = 2;          // reused
    PushStreamChunks chunks = 3;        // reused
    PushStreamRemoved removed = 4;      // reused
    ImportCommitEnd end = 5;
  }
}
message ImportStreamResponse { string origin = 1; string commit_id = 2; string commit_hash = 3; }
```

- `ImportStream` is bidirectional: one commit at a time (header, parts, end),
  with one response per commit. The client pipelines: it uploads chunks for
  commit k+1 while the server applies commit k. The stream reuses the
  `PushStream` receive helpers and caps (`PUSH_MAX_FILES`, 1 GiB per commit).
- Maintenance class: write. Rate limiting: one token per stream, like
  `PushStream`.
- No REST routes in v1. The server job in phase 6 calls the usecase directly.

### Client state

`<git-dir>/nipa-import/state.db` (SQLite, `modernc.org/sqlite`) holds:

- `blobs(git_sha PK, file_hash, size, is_binary, encoding, chunks BLOB,
  uploaded BOOL)`: the conversion memo. It means a resumed run neither
  re-chunks nor re-uploads.
- `lfs(oid PK, git_sha)`.
- `meta`: the target URL and project, refs and options.

The commit mapping is **not** authoritative locally: `ImportLookup` on start
asks the server which origins exist. Deleting the state DB only costs
re-chunking time.

## Performance targets

- Each distinct blob is chunked and uploaded once. Chunks the project already
  has come back `already_stored`, and content held by another project is
  claimed by proof without re-upload.
- Server cost per commit is `O(changed paths + their ancestor dirs)`, the same
  as a push. No whole-tree rewrite.
- Memory is bounded by one file in the chunker plus the 16 MiB upload windows
  and the ordered commit list (about 100 bytes per commit).
- Benchmark (env-gated, `NIPA_BENCH_GIT_IMPORT`): a synthetic repo with 20k
  commits, 50k files and 2 GiB of LFS against the in-process server. Record
  commits/s and MiB/s in `docs/PERFORMANCE.md`.

## Security

- Only project admins can import. Imported author fields are claims, not
  identities, so the UI marks imported commits ("Imported from Git").
- Author matching is limited to the org's verified members.
- Quota, own-storage gate, maintenance and rate limits apply as for pushes.
- Every `origin` is validated against the "Origin format" rules (registered
  system, per-system id check, length cap).
- LFS endpoints and credentials stay client-side and are never sent to the
  Nipa server.

## Fidelity summary

| Kept | Not kept |
| --- | --- |
| content (incl. LFS), modes, paths | Git SHAs as identifiers (kept in `origin`) |
| commit DAG (≤2 parents), order, messages | octopus parents beyond 2 |
| author name/email/date | committer identity, GPG signatures, notes |
| branches, tags (annotations), default branch | symlinks (opt-in as files), submodules |
| | reflogs, stashes, hooks, remote refs not selected |
