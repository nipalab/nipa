# Nipa File Locking

Design for Perforce-style exclusive file locks in Nipa: server-enforced, binary-safe,
coexisting with the Git-style branching model. **Binary-first by default** — locks are
available for files that cannot merge; text paths are locked only when a path-ACL policy
grants it.

---

## Why locking

Nipa already covers the two halves of a P4 workflow:

- **Git-style cheap branches** for parallel exploration (`switch`, `merge`, MR flow).
- **Perforce-style content streaming** for binary assets (chunk store, incremental update).

Missing is the third leg: **who owns the right to change a file**. Without it, binary
files (`.ma`, `.blend`, `.fbx`, audio, level data) can still be clobbered on mergeback —
the exact problem locking exists to solve. `git-lfs` locks are advisory only; Nipa will
enforce on the server.

---

## Branching + locking: the scope decision

Locks and branches solve different problems — branches are cheap labels over the content
store, locks are write-permissions per path. They only clash over one question:

> A `path` exists in many branch states. What does "the file is locked" mean?

| Scope | Behaviour | Verdict |
|-------|-----------|---------|
| Per `(project, path)` — **global** | Anyone on *any* branch is blocked while you hold it | **Chosen.** If you could edit a path on another branch, the binary merge-clobber the lock was meant to prevent just moves to mergeback time. |
| Per `(project, branch, path)` | Serialized within a branch, parallel across branches | Rejected: parallel branch edits still collide at the merge, and binary collisions are the disease here. |

**Rule:** exclusive locks are **global per `(project, path)`**. A branch never bypasses a lock.
Branches still matter — the lock bites at *merge time against the shared line* (stream-style),
not at branch creation.

---

## Binary-first policy

Locks exist to serialize writes to files that **cannot merge**. Binary-ness is the cheap
server-side proxy for "merge-hostile" — and it's one Nipa already computes per file
(`is_binary` in the tree manifest). So binary-ness is the default gate, not the only gate:

- **Default:** `LockFile` on a **binary** path (per the current manifest's `is_binary`)
  succeeds. `LockFile` on a **text** path is rejected with a policy error.
- **Opt-in exception for text:** release freezes, compliance gates, and sole-owner config
  files legitimately need text locks (`version.txt`, `config/prod.yaml`, `Cargo.toml`).
  A path-ACL policy entry (the same ACL hook used everywhere else) can grant text locks for
  specific paths/roles. Without that entry, text locks stay off.
- **Binary is available-by-default, never auto-acquired:** locking stays explicit (`nipa lock`)
  like `p4 edit` with the `+l` file type. Some binaries are regenerated pipeline artifacts
  (build outputs, bundles, generated code) where locks would be noise — the team decides,
  not the tool.

The cheap rule singles out the 90% case (asset files) while leaving the wrench room for
the rest.

---

## Data model

Server-side (no client-local lock table; the lock lives at the single source of truth):

```sql
CREATE TABLE locks (
    id          INTEGER PRIMARY KEY,
    project_id  INTEGER NOT NULL,
    path        TEXT    NOT NULL,
    owner_id    INTEGER NOT NULL,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at  TIMESTAMP,                          -- NULL = until released
    UNIQUE (project_id, path)
);
```

- `UNIQUE (project_id, path)` gives exclusive-by-default: a second `ThreadLockFile` on the
  same path fails.
- `path` is the repo-relative path, resolved through the **same path-resolution helper used
  by the existing ACLs** (`internal/domain`) — one permission/ACL story, including the
  binary-first/text-lock policy exceptions.
- Consider `expires_at` as an optional TTL for stale-lock cleanup in the first cut;
  admins get a force path regardless (see *Force unlock*).

---

## gRPC surface

New methods on `NipaService` in `internal/grpc/proto/server.proto`:

| RPC | Req | Res | Behaviour |
|-----|-----|-----|-----------|
| `LockFile` | `{context{org,project}, path, ttl_seconds?}` | `{ok, expires_at?}` | Acquire exclusive lock. Validates against the manifest's `is_binary` — text paths are rejected unless a path-ACL policy grants text locks. Idempotent for the owner (re-lock by the same owner succeeds, refreshes TTL). Fails if held by someone else. |
| `UnlockFile` | `{context{org,project}, path, force?}` | `{ok}` | Releases the lock. `force=true` requires an ACL role (or lock owner). |
| `ListLocks` | `{context{org,project}}` | `{locks[]}` | Locks with owner + expiry, for `status`, the web UI, and `update`. |

Enrich the existing responses rather than adding more round-trips:

- `GetTreeManifest`/`Update` responses carry a `locked_paths` list so a clone/update marks
  locked files read-only locally.
- `StatusCommand`/status response includes `L` markers.

---

## Enforcement chokepoints

Writes flow through two server chokepoints today; both get a lock check.

1. **`Push` (the write path).** With the tree manifest arriving, if any
   changed path is locked **and the caller is not the owner** → reject with
   `domain.NewErrorNoPermission()` (or a dedicated `lock` code) naming the owner.
   The owner's own push passes. Chunk content may already be in the content
   store from signed uploads, but a Push is what makes it reachable, so the
   check belongs there.
2. **`MergeFastForward` / MR-target merge.** Before a merge is applied to the target branch,
   collect the union of paths the MR touches; if any is locked and the MR author isn't the
   owner → reject. This is the *merge lock* that makes branches + locks coherent: the lock
   guards the shared line, so nothing can be merged over a locked binary's content.

No change to `CreateBranch`/`GetTreeManifest`: branches read freely. Locking never blocks
**reading** — only **changing the bytes**. Enforcement checks the lock itself, not the file
type: a text lock that exists (because a policy granted it) is enforced exactly like a binary
one, so the binary-first gate lives entirely at `LockFile` time.

---

## Client UX

```
nipa lock models/boss.fbx              # acquire lease
nipa lock models/boss.fbx --ttl 2h     # lease with TTL
nipa unlock models/boss.fbx            # release
nipa status                            # L marker on locked paths
nipa update                            # locked paths become read-only on disk
```

- **`nipa lock <path>`** → server call, prints owner/expiry on success. Text paths return a
  "binary-only by default" error unless an ACL policy grants text locks for that path.
- **Auto-release on push** (mirrors `p4 submit` unlocking the changelist): when the owner
  pushes a locked path, the server releases that lock. `nipa unlock` covers everything else.
- **Read-only working copy:** paths listed in the `<Update>/<Manifest>` response are chmod'd
  read-only (000444) locally, so even the tools silently refuse to save over a foreign lock
  (`p4 opened` lease behaviour).
- **Switch while holding locks:** locks are global and server-side; they survive branch
  switches. The worked-copy re-marking follows the new branch's manifest.
- **Web UI:** `ListLocks` feeds the repository browse page (`Locked by X` badges), reusing
  the `web/` SPA.

---

## Edge cases

| Case | Behaviour |
|------|-----------|
| Owner's second lock call | Succeeds, refreshes TTL (idempotent). |
| Lock a text path | Rejected (binary-only by default); succeeds only where a path-ACL policy grants text locks. |
| Path switches text↔binary between versions | Gate re-evaluated from the current manifest at `LockFile` time. |
| Lock held by X, Y pushes | `Push` rejects with owner name; `update` marks read-only. |
| Owner crashes / workspaces rotate | `expires_at` TTL auto-expires; full cleanup needs admin force first |
| Owner deletes the file | `remove` cleans up orphaned lock rows for the path. |
| MR touches a locked path | Blocked at `MergeFastForward` until released or forced. |
| Lock on a path since renamed | Rename = delete+add; old lock swept, new path needs its own lock. |
| Force unlock | ACL role (`write`-admin equivalent) + `force` on `UnlockFile`; logged for audit. |
| Lock + dirty local copy conflict | `update` refuses to overwrite locally-modified files as today; lock state shows first. |

---

## Workflow example

1. Artist: `nipa switch feature/character-X` — cheap branch, no lock involved.
2. Artist: `nipa lock models/boss.fbx` — global lease acquired.
3. Teammate on `mainline`: `nipa update` → `models/boss.fbx` becomes read-only; a `push`
   containing it is rejected server-side with "locked by <artist>".
4. Artist edits, `nipa push` → lock auto-released, content lands in the branch.
5. MR into `mainline` merges cleanly — nothing else touched the path meanwhile.

---

## Decisions Locked In

| Decision | Rationale |
|----------|-----------|
| Global locks per `(project, path)` | Per-branch locks defer the binary collision to merge time — the bug they exist to prevent. |
| Server-enforced at `Push` + `MergeFastForward` | One source of truth; advisory-only locks are the `git-lfs` failure mode. |
| Locks never block reads/branches | Branching overhead stays zero; locking only gates byte changes. |
| Auto-release on owner push | Mirrors `p4 submit`; no lock churn on normal flows. |
| Read-only materialization on lock | Physical tool-level protection, matches P4 lease feel. |
| Reuse ACL path resolution + `domain.Error` | Locking extends the existing path-permission story instead of adding a second one. |
| **Binary-first by default** | `is_binary` (already in the manifest) is the cheap server-side "merge-hostile" test; locks on unmergeable files is the 90% case. Text locks only via explicit ACL policy (freezes, compliance). |
| `expires_at` TTL + admin force | Bounded stale-lock impact, no silent indefinite ownership. |

---

## Roadmap

| Step | Deliverable | Value |
|------|-------------|-------|
| 1 | `locks` table + migrations (`db/migrations/sqlite\|postgres`), `sqlc` queries | Foundation |
| 2 | RPCs `LockFile`/`UnlockFile`/`ListLocks` + usecase handler logic (gomock-tested), incl. the `is_binary` gate + text-lock ACL exception at `LockFile` | Server API |
| 3 | Enforce in `Push` (+ `MergeFastForward` MR check) | The actual guarantee |
| 4 | `nipa lock/unlock/status-L` CLI + read-only marking on `update` | Client UX |
| 5 | `GetTreeManifest`/`Update` lock payload + Web UI `ListLocks` badges | Multi-surface visibility |
| 6 | e2e tests: two users on a branch + mainline, TTL expiry, force unlock | Regression safety |

Header for step 3's MR check is `docs/BRANCH_MERGING.md` design (server-side MRs); the lock
check rides the same merge path once MRs land server-side.

---

## Notes for Later

- Audit: log acquire/release/force; surface via a future `nipa history`/web audit view.
- Lock **wildcards** (`models/**`) — explicit P4-style directory leases, if single-path proves
  noisy at scale. Keep the `UNIQUE(project_id, path)` storage and index-prefix matching for these.
- Alternative scope worth `ee/` (enterprise) gating: **streams** (locked mainline, instreams
  free) as a packageable feature on top of the global-lock core.
- Concurrency notes for `LockFile`: `INSERT ... ON CONFLICT(project_id, path) DO UPDATE ...`
  keeps it race-free in SQLite/Postgres without transactions.