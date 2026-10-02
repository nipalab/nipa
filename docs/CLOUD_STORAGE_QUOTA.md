# Cloud storage quota (Phase 2)

Status: **not implemented**. Phase 1 (free side) is done and this document is
the implementation plan for the cloud (enterprise, `ee/`) part. Nothing here
belongs in `internal/`: the free version ships no quota and no usage tracking.

## Model

- The **billing unit is a user** ("owner"). A user can create any number of
  organizations; every org is billed to exactly one owner user, so the owner's
  quota covers all of their orgs.
- The owner is the admin of their own billing: they can see usage and buy
  additional storage. Platform super admins can override quotas and reassign an
  org's billing owner.
- **Metric**: stored (post-compression) chunk bytes, measured server-side.
  Chunks are charged per project on first link; identical content is charged
  again for another project of the same owner (per-project breakdown stays
  cheap). Owner usage = sum of the usage counters of the owner's orgs.
- **History counts**: chunks are never pruned, so usage is monotonic between
  recounts. Soft-deleted orgs/projects keep counting until a recount policy or
  a hard delete says otherwise.
- No quota row or a NULL quota means unlimited; the free version always runs
  unlimited because it wires no ledger at all.

## Phase 1 contract (already implemented)

`internal/usecase/storage_ledger.go`:

```go
type LedgerChunk struct {
    Hash      domain.Hash
    SizeBytes int64 // server-measured stored size
}

type StorageLedger interface {
    // Sizes of hashes already linked to the project (absent = not linked).
    AttributedSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error)
    // Reject with a quota error when the projected new bytes do not fit.
    Headroom(ctx context.Context, org *domain.Organization, project *domain.Project, projectedNewBytes int64) error
    // Link chunks to the project and update counters in one transaction.
    Attribute(ctx context.Context, org *domain.Organization, project *domain.Project, chunks []LedgerChunk) error
}
```

- `usecase.Chunk.WithStorageLedger` attaches it; nil (free default) skips all
  accounting.
- Call sites: `usecase.Chunk.checkHeadroom` (from `PresignUploadURLs`, before
  any upload URL is signed) and `ConfirmUploads` (after each present chunk's
  metadata row was recorded, one `Attribute` call per confirm batch).
- Quota failures use `domain.NewErrorQuotaExceeded` (HTTP 402); gRPC maps it to
  `codes.ResourceExhausted` and the client maps that back to a 402 domain error,
  so `nipa push` shows the message.
- `organizations.created_by_user_id` (base schema) records who created an org;
  it is the seed for the owner mapping, not the mapping itself.

## Package layout

```
ee/storageusage/
  migrations/            # own embedded FS, see "Migrations"
  ledger.go              # implements usecase.StorageLedger
  owner.go               # owner resolution / transfer
  recount.go             # backfill & repair
  api.go                 # REST routes (self-registered, see "REST API")
  store.go               # sqlc-generated access (own queries dir)
```

Only `cmd/nipad` imports `ee/` (same rule as `ee/storage/s3`).

## Migrations

ee tables must not share the free `schema_migrations` table. Run a second
golang-migrate instance against `ee_schema_migrations`:

- Add an exported helper to the free `db` package (interface-level plumbing):

  ```go
  func MigrateUpWithTable(db *sql.DB, dialect string, migrationsFS fs.FS, table string) error
  ```

  backed by `sqlite.Config{MigrationsTable: table}` /
  `pgxmigrate.Config{MigrationsTable: table}`.
- `cmd/nipad` applies ee migrations after `db.MigrateUp` when the ledger is
  enabled. Cloud runs sqlite (the postgres migration set is still incomplete);
  add postgres ee migrations when the server supports postgres.

## Schema (sqlite)

```sql
CREATE TABLE ee_org_billing_owner (
    org_id  INTEGER PRIMARY KEY REFERENCES organizations(id),
    user_id INTEGER NOT NULL REFERENCES users(id)
);

CREATE TABLE ee_owner_quota (
    user_id     INTEGER PRIMARY KEY REFERENCES users(id),
    quota_bytes INTEGER,              -- NULL = unlimited
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE ee_project_chunks (
    project_id INTEGER NOT NULL,
    chunk_id   INTEGER NOT NULL REFERENCES chunks(id),
    size_bytes INTEGER NOT NULL,
    PRIMARY KEY (project_id, chunk_id)
);

CREATE TABLE ee_project_usage (
    project_id INTEGER PRIMARY KEY REFERENCES projects(id),
    used_bytes INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE ee_org_usage (
    org_id     INTEGER PRIMARY KEY REFERENCES organizations(id),
    used_bytes INTEGER NOT NULL DEFAULT 0
);
```

Counters are caches derived from `ee_project_chunks`; the link insert and the
counter updates happen in the same transaction, so they cannot drift except
through external interference. `recount` rewrites both.

## Owner resolution

`ensureBillingOwner(org)`:

1. `SELECT user_id FROM ee_org_billing_owner WHERE org_id = ?` → done.
2. Seed lazily from `organizations.created_by_user_id` (NOT NULL: every org,
   including the seeded default org, has a creator), then insert the mapping.
3. If the creator user no longer exists (hard delete), require a global admin to
   transfer the org before further uploads instead of silently allowing
   unlimited.

Transfer (platform super admin only) updates `ee_org_billing_owner`; usage moves
with the org because org counters are the source of the owner aggregate, so no
recount is needed.

## Enforcement

`Attribute` (confirm batch), one transaction:

```
owner      := ensureBillingOwner(org)
quota      := SELECT quota_bytes FROM ee_owner_quota WHERE user_id = owner
              (missing row -> STORAGE_LEDGER_DEFAULT_QUOTA_BYTES; 0/NULL = unlimited)
used       := SELECT COALESCE(SUM(u.used_bytes), 0)
              FROM ee_org_usage u JOIN ee_org_billing_owner b ON b.org_id = u.org_id
              WHERE b.user_id = owner
newBytes   := 0
for each chunk:
    chunkID  := SELECT id FROM chunks WHERE hash = ?      -- written by the free confirm path
    inserted := INSERT INTO ee_project_chunks (project_id, chunk_id, size_bytes)
                VALUES (?, ?, ?) ON CONFLICT(project_id, chunk_id) DO NOTHING
                RETURNING size_bytes
    if inserted: newBytes += size_bytes
if quota > 0 && used+newBytes > quota:
    ROLLBACK; return domain.NewErrorQuotaExceeded(fmt.Sprintf(
        "storage quota exceeded: %d bytes used, %d bytes quota, %d bytes needed", used, quota, used+newBytes))
for each inserted: UPDATE ee_project_usage / ee_org_usage (+= size_bytes)
COMMIT
```

- All-or-nothing per confirm batch. The retry after a quota raise reuses the
  already-stored bytes (`AlreadyStored`), so no re-upload.
- `Headroom` (presign) is the advisory early gate: `used + projectedNewBytes`
  against the same quota; it protects upload bandwidth, `Attribute` is
  authoritative (concurrent pushes can race past presign).
- The free `chunks` metadata row is inserted before `Attribute`; a crash in
  between leaves a recorded but unattributed chunk, which `recount` repairs.
  Rejected content stays in the store unrecorded until a chunk GC exists;
  it is not billed.

`AttributedSizes` is a per-hash lookup (`ee_project_chunks JOIN chunks`); the
presign page is small (page size <= `CHUNK_MAX_PAGE_SIZE`).

## Buying additional storage

Purchasing does not need new seam methods: it changes
`ee_owner_quota.quota_bytes`, which presign/confirm read on every operation.

Recommended split:

- `ee/` owns quota values and enforcement.
- Billing/payment (e.g. Stripe) is an external service that calls an ee admin
  or internal endpoint to set the quota after a successful purchase; cloud can
  also run it as a periodic sync job.
- A `plan`/`purchased_bytes` model can be added later; with one explicit
  `quota_bytes` column any plan math is just how the value is produced.

## REST API

Registered as a separate `restful.WebService` on the same container in
`cmd/nipad` (after `api.SetupRoute()`), reusing
`api.NewAuthFilter(auth).Auth()` so `domain.ClaimFromContext` works. The free
`internal/http/api` package is not modified.

| Route | Guard | Purpose |
|---|---|---|
| `GET /api/v1/orgs/{org}/storage` | org owner or global admin | usage, quota, per-project breakdown for one org |
| `GET /api/v1/billing/me` | authenticated | owner quota, aggregate usage, per-org breakdown |
| `PUT /api/v1/admin/billing/owners/{user}/quota` | `IsSuperAdmin` | set/clear an owner's quota (NULL = unlimited) |
| `PUT /api/v1/admin/orgs/{org}/billing-owner` | `IsSuperAdmin` | transfer an org's billing owner |
| `POST /api/v1/admin/orgs/{org}/storage/recount` | `IsSuperAdmin` | rebuild ledger + counters for one org |

All read endpoints feature-detect on the SPA side: with the ledger disabled
they are not registered and return 404, so the storage UI hides itself.

## Web UI

- `web/src/api/endpoints.ts`: `getOrgStorage`, `getMyBilling`, `setOwnerQuota`,
  `setOrgBillingOwner`, `recountOrgStorage`.
- `OrgSettingsPage.tsx`: storage card (used/quota progress, per-project table),
  hidden when `getOrgStorage` returns 404.
- New `/settings/billing` page for the owner: aggregate usage, per-org table,
  quota, and the "buy additional storage" entry point (links to the payment
  flow / support).
- AdminUsersPage or a small admin section can expose quota editing.
- Keep `npm run lint`, `npm test` and `make web` green.

## Recount / backfill

Required once when enabling the ledger on an existing deployment and useful for
repair. For each project, rebuild `ee_project_chunks` from the actual history:

```sql
WITH RECURSIVE tree_ids(id) AS (
    SELECT tree_id FROM commits WHERE project_id = :project
    UNION
    SELECT tn.id FROM tree_nodes tn JOIN tree_ids ti ON tn.parent_tree_id = ti.id
)
SELECT DISTINCT fc.chunk_id, ch.size_bytes
FROM files f
JOIN file_chunks fc ON fc.file_id = f.id
JOIN chunks ch ON ch.id = fc.chunk_id
WHERE f.tree_id IN (SELECT id FROM tree_ids);
```

Run per project in one transaction: delete the project's links, insert the
recomputed set, set `ee_project_usage`, then refresh `ee_org_usage` and the
owner aggregate. Expose it as an offline/subcommand path
(`nipad -recount-storage [org-slug|all]`) so large repos do not block the API.

## Config and wiring (`cmd/nipad`)

- `internal/config`: `StorageLedger string` (`STORAGE_LEDGER`, empty = off) and
  `StorageLedgerDefaultQuotaBytes int64` (`STORAGE_LEDGER_DEFAULT_QUOTA_BYTES`,
  0 = unlimited).
- Mirror `createChunkStore`:

  ```go
  chunkUsecase := usecase.NewChunk(pushRepository, chunkStore, transferCfg)
  if strings.EqualFold(cfg.StorageLedger, "cloud") {
      if err := db.MigrateUpWithTable(dbConn, "sqlite3", storageusage.MigrationsFS, "ee_schema_migrations"); err != nil { ... }
      ledger := storageusage.NewLedger(dbConn, storageusage.Config{DefaultQuotaBytes: cfg.StorageLedgerDefaultQuotaBytes})
      chunkUsecase = chunkUsecase.WithStorageLedger(ledger)
  }
  ```

- The ledger is a thin object over `*sql.DB`; it must be safe for concurrent
  confirms (the existing client serializes confirmations, and SQLite WAL +
  busy timeout covers the rest).

## Testing

- `ee/storageusage`: sqlite-backed tests for every method — lazy owner seeding
  (creator, legacy first-owner fallback), link idempotency and exactly-once
  counter increments, quota rejection rollback (no links, no counter changes),
  unlimited paths, transfer moving the aggregate, recount repairing drift.
- `usecase` seam is already covered with a mock ledger; add an integration test
  wiring the real ee ledger through `Chunk` (confirm over/below quota).
- gRPC/REST: 402 payload, admin guards, 404 feature detection when disabled.
- `internal/e2e`: push that trips a quota, raise it, retry push succeeds
  without re-uploading bytes.
- Web: storage card hidden on 404, quota editing for admins.

## Open questions

1. Should owners be able to lower their own quota, or only buy above the
   current value? (Recommendation: purchase-only; admins can set anything.)
2. Do soft-deleted orgs keep billing? (Current stance: yes; decide before
   enabling.)
3. Chunk GC is out of scope; until it exists, rejected/unreferenced content
   occupies disk but is never billed.

## Acceptance criteria

- With `STORAGE_LEDGER=cloud`, an owner's orgs share one quota; another owner's
  orgs are unaffected.
- A push that would exceed the quota fails with a 402 whose message names used,
  quota and needed bytes; no chunks are linked; the retry after a quota change
  succeeds without re-uploading.
- `GET /api/v1/billing/me` and `GET /api/v1/orgs/{org}/storage` report numbers
  that match the ledger after a recount.
- `recount` on a database pushed before the ledger existed reconstructs the
  same usage the online path would have produced.
- With the ledger disabled the free behavior is byte-for-byte unchanged (no ee
  tables, no extra queries, no endpoints).
