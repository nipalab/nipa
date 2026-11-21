# Storage reclaim plan (enterprise)

Plan for letting a billing owner get storage back. Written 2026-11-21.
All three phases are implemented.

## Today

- Deleting a project is a soft delete (`projects.deleted`). Nothing told the
  storage ledger, so the project's bytes kept counting against the owner's
  quota forever, and `RecountOrg` counted deleted projects too.
- There is no org delete usecase and no project restore.
- Nothing ever removes history rows of a deleted project (commits, trees,
  files, `file_chunks`), and nothing ever removes chunk content from S3:
  `chunks` is one global, content-addressed table (`chunks.hash UNIQUE`) and
  the S3 key is `<prefix>/<hash[:2]>/<hash[2:]>`, shared by every project and
  org on the server.
- Content rejected by the quota after upload stays in S3 unrecorded.

Usage is counted per project (`project_chunks`, `project_usage`), so releasing
a project's bytes from the owner's quota is cheap; freeing physical storage is
the hard part, because a chunk may be shared with any other project.

## Phase 1: deleting a project releases its quota (implemented)

Goal: an owner who deletes a project sees their usage drop immediately.

- New seam method `StorageLedger.ReleaseProject(ctx, projectID) (int64, error)`
  in `internal/usecase/storage_ledger.go`.
- `Project.WithStorageLedger` attaches it (wired in `serverapp.BuildUsecases`
  from `UsecaseDeps.StorageLedger`, so the free server, which has no ledger,
  is unchanged). `Project.Delete` calls it inside the audited write, after the
  soft delete, so the delete, the release and the audit entry commit together
  when the audit recorder is transactional. The audit entry of
  `project.deleted` gains `released_bytes` when a ledger is attached.
- `ee/storageusage.Ledger.ReleaseProject`, one transaction under the owner's
  advisory lock (`LockBillingOwner`, the same lock `Attribute` takes):
  read `project_usage.used_bytes`, delete the project's `project_chunks`
  links, zero `project_usage`, subtract the bytes from `org_usage`.
  Idempotent: a second call releases 0.
- Race with an in-flight confirm or merge: `Attribute` now checks, under the
  owner lock, that the project is not deleted and returns 404 otherwise. Any
  confirm that took the lock before the release is undone by the release,
  because the release reads the links after it gets the lock.
- Recount skips deleted projects: `ListProjectIDsByOrg` and
  `SetOrgUsageFromProjects` filter `p.deleted = false`; deleted projects are
  handled like external (BYO) ones (links dropped, usage zero) through
  `ListUnbilledProjectIDsByOrg`. `ListProjectUsage` reports 0 for a deleted
  project.
- Settles open question 2 of `docs/CLOUD_STORAGE_QUOTA.md`: soft-deleted
  projects stop billing.

Not in phase 1: physical deletion, restore, org deletion.

Known cost: the release deletes all of a project's `project_chunks` rows in
one transaction. That is a one-off per delete, not on the push path, but a
project with millions of chunks holds the owner lock for the duration; phase 2
moves bulk deletes into batches.

## Phase 2: retention window, restore, then purge (implemented)

Goal: deleted projects are restorable for a while, then their history rows go
away for good.

What was built (details in `docs/internals/server.md`, "Flow for project
deletion, restore and purge"):

- Config: `STORAGE_PURGE_RETENTION_DAYS` (default 30; 0 never purges and
  keeps deleted projects restorable) and `STORAGE_PURGE_BATCH_SIZE` (commits
  per transaction, default 100). Only `nipad-ee` purges; the free `nipad` has
  restore without a window.
- Restore: `GET /api/v1/orgs/{org}/deleted-projects` and `POST
  /api/v1/orgs/{org}/deleted-projects/{id}/restore` (org owner or global
  admin), the "Deleted projects" tab in org settings, `project.restored`
  audit. Restore charges the history back through
  `StorageLedger.RestoreProject` and fails with 402, changing nothing, when it
  does not fit.
- Purge: `ee/projectpurge`, an hourly background job; each step is its own
  short transaction that re-locks the project row, so a restore wins and two
  instances serialize without leader election. Audits `project.purged`.
- Migration `000017_project_purge` (postgres) adds the indexes the purge
  needs.

Findings that shaped it:

- `UNIQUE(org_id, slug)` ignores `deleted`: a deleted project keeps its slug
  until it is purged, so restore never clashes on the slug, and the purge is
  what frees it.
- Tree reads are content-addressed across projects: a kept (unchanged)
  directory is written as a bare node, and reads resolve it to the oldest node
  with the same hash that has children or files, in any project. Purging is
  still safe because every project wrote its own full copy of every directory
  it keeps (first commits write full trees; a kept directory was in the
  project's own previous head), so the lookup falls through to the next copy.
  Covered by `TestPurgeExpired_RemovesTheProjectForGood`.
- Every commit owns its root tree (the push repository always inserts fresh
  tree rows), so deleting a commit's root cascades exactly that commit's
  nodes, files and `file_chunks`.
- All foreign keys into a project's commits, branches and merge requests come
  from the project's own tables (branches, tags, merge requests and their
  children, file locks), so the purge deletes those first, detaches commit
  parents, then deletes commits in batches.
- Without indexes on the columns referencing `commits(id)` (and on
  `commits(project_id)`), each deleted commit would scan those tables across
  all projects; the migration adds them.

Not done: org delete (no org delete exists yet; when added, it should
soft-delete and let this purge remove the projects).

## Phase 3: chunk GC (physical reclaim) (implemented)

Goal: S3 objects nobody references are deleted.

What was built (`ee/chunkgc`, seam `usecase.ChunkGCGuard`; details in
`docs/internals/protocol.md`):

- Migration `000018_chunk_gc` (postgres): `chunks.gc_marked_at` plus indexes on
  `project_chunk_uploads(hash)` and `project_chunks(chunk_id)` so the
  "is anything referencing this chunk" checks are index lookups.
- A chunk is garbage when no `file_chunks`, `project_chunk_uploads` or
  `project_chunks` (ledger) row references it.
- Collector, every 6 hours, one instance at a time (advisory lock):
  mark (id ranges), adopt orphan objects older than the grace as marked rows
  (bucket listing, `CHUNK_GC_ORPHAN_SCAN`), sweep rows marked longer than
  `CHUNK_GC_GRACE_HOURS` (objects first, then rows, under `FOR UPDATE SKIP
  LOCKED`).
- `CHUNK_GC_MODE`: `off`, `dry-run` (default: marks and reports, deletes and
  adopts nothing) or `on`. Metrics `nipa_chunk_gc_chunks_total` /
  `nipa_chunk_gc_bytes_total` by phase.
- BYO buckets are never touched.

The race and how it is closed:

- "Already stored" (no upload) is only answered for chunks the project is
  linked to, and a linked chunk is never garbage. Otherwise the client proves
  possession and confirm reads the object back. The danger is confirm reading
  an object, a sweep deleting it, and confirm recording a row and link for
  gone content.
- Confirm (whole batch) and the server's own uploads call `Protect` first,
  which clears the mark with an `UPDATE` that waits for a sweep holding the
  row. After a sweep commits the row is gone, the object read fails, the chunk
  is reported missing, and the client presigns and uploads it again (it
  already retries three times). After `Protect` commits, the sweep no longer
  selects the row. Every link write happens after one of those calls.
- Objects without a row (never-confirmed uploads) cannot be row-locked; the
  grace covers them: only objects older than the grace are adopted, and an
  adopted row is only swept once its mark is older than the grace, which
  startup checks is at least twice the presign TTL.
- Covered by `TestGuardWaitsForAnInFlightSweep` and the e2e
  `TestEnterpriseChunkGCReclaimsPurgedContent`.

Known limits:

- A chunk referenced only by an own-storage project keeps its object in the
  Nipa bucket if one exists there (rare: the same content stored by a Nipa
  project that was later purged). Leaks storage, never loses data.
- The free server's local chunk store has no collector.

## Later, maybe

Pruning history of live projects (old revisions, deleted branches' unique
commits). It conflicts with the forward-only history model; leave it out
until someone asks.
