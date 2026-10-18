# Cloud storage quota

Status: **implemented**. The free seam (`internal/usecase/storage_ledger.go`)
ships in the free server with a nil ledger; the enterprise implementation
(`ee/storageusage/`) is wired by `ee/cmd/nipad` when `STORAGE_LEDGER=cloud`.
Nothing here belongs in `internal/`: the free version ships no quota and no
usage tracking.

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

## Seam (free side)

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
    // Attribute server-generated content (merge output), project id only.
    AttributeProjectChunks(ctx context.Context, projectID snow.ID, chunks []LedgerChunk) error
}
```

- `usecase.Chunk.WithStorageLedger` attaches it; nil (free default) skips all
  accounting. `Chunk.AttributeProjectChunks` delegates to the ledger, so the
  merge strategies (`internal/usecase/merge_strategies.go`) can attribute the
  diff3 text chunks they store through the one-caller `chunkUploader` seam.
- Call sites: `usecase.Chunk.checkHeadroom` (from `PresignUploadURLs`, before
  any upload URL is signed), `ConfirmUploads` (after each present chunk's
  metadata row was recorded, one `Attribute` call per confirm batch) and
  `MergeForMergeRequest` (after the permission checks, before the commit
  lands).
- Quota failures use `domain.NewErrorQuotaExceeded` (HTTP 402); gRPC maps it to
  `codes.ResourceExhausted` and the client maps that back to a 402 domain error,
  so `nipa push` shows the message.
- `organizations.created_by_user_id` (base schema) records who created an org;
  it seeds the owner mapping lazily on first use.

## Package layout

```
ee/storageusage/
  ledger.go              # implements usecase.StorageLedger over postgres
  owner.go               # owner resolution / lazy seeding
  recount.go             # rebuild links and counters for one org
  api.go                 # REST routes on the shared /api/v1 WebService
```

SQL lives in `ee/repository/postgres/queries/storage_usage.sql` (generated into
`ee/repository/postgres/sqlc`), so `make sqlc` keeps one postgres config.

## Schema (postgres)

The five tables are part of the consolidated `000001_init_schema` migration
(development-phase policy: no new migration pair yet):

```sql
CREATE TABLE org_billing_owner (
    org_id     BIGINT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE owner_quota (
    user_id     BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    quota_bytes BIGINT,               -- NULL = unlimited
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE project_chunks (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    chunk_id   BIGINT NOT NULL REFERENCES chunks(id),
    size_bytes BIGINT NOT NULL,
    PRIMARY KEY (project_id, chunk_id)
);
CREATE TABLE project_usage (
    project_id BIGINT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    used_bytes BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE org_usage (
    org_id     BIGINT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    used_bytes BIGINT NOT NULL DEFAULT 0
);
```

Counters are caches derived from `project_chunks`; the link insert and the
counter updates happen in the same transaction, so they cannot drift except
through external interference. `recount` rewrites both.

## Owner resolution

`billingOwner(orgID)`:

1. `SELECT user_id FROM org_billing_owner WHERE org_id = $1` → done.
2. Seed lazily from `organizations.created_by_user_id` through
   `EnsureBillingOwner`, whose `ON CONFLICT DO UPDATE SET user_id =
   org_billing_owner.user_id` never overwrites a concurrent transfer.
3. Transfer (super admin) writes `org_billing_owner`; usage moves with the
   org because org counters are the source of the owner aggregate, so no
   recount is needed.

## Enforcement

`Attribute` (confirm batch and merge output), one transaction:

```
owner, quota := ownerQuota(orgID)        -- default quota when no row; 0/NULL = unlimited
used         := SELECT COALESCE(SUM(u.used_bytes), 0) ...
newBytes     := 0
for each chunk:
    chunkID  := SELECT id FROM chunks WHERE hash = $1
    inserted := INSERT INTO project_chunks (...) ON CONFLICT DO NOTHING
                RETURNING size_bytes        -- no row = already linked
    newBytes += inserted
if newBytes > 0 && quota > 0 && used+newBytes > quota:
    ROLLBACK; return domain.NewErrorQuotaExceeded(...)
UPDATE project_usage / org_usage (+= newBytes)
COMMIT
```

- All-or-nothing per batch. The retry after a quota raise reuses the
  already-stored bytes (`AlreadyStored`), so no re-upload.
- `Headroom` (presign) is the advisory early gate: `used + projectedNewBytes`
  against the same quota; it protects upload bandwidth, `Attribute` is
  authoritative (concurrent pushes can race past presign).
- The free `chunks` metadata row is inserted before `Attribute`; a crash in
  between leaves a recorded but unattributed chunk, which `recount` repairs.
  Rejected content stays in the store unrecorded until a chunk GC exists; it is
  not billed.
- `AttributedSizes` is a per-hash lookup (`project_chunks JOIN chunks`); the
  presign page is small (page size <= `CHUNK_MAX_PAGE_SIZE`).

## Buying additional storage

Purchasing does not need new seam methods: it changes
`owner_quota.quota_bytes`, which presign/confirm read on every operation.

- `ee/` owns quota values and enforcement.
- Billing/payment (e.g. Stripe) is an external service that calls the admin
  quota endpoint after a successful purchase; cloud can also run it as a
  periodic sync job.
- A `plan`/`purchased_bytes` model can be added later; with one explicit
  `quota_bytes` column any plan math is just how the value is produced.

SaaS self-registration seeds every new account with the free-tier baseline
(`SAAS_FREE_QUOTA_BYTES`, default 15 GiB) through the same `owner_quota` row;
see docs/SAAS_ONBOARDING.md for that flow and for the planned
monthly/yearly subscription integration (an external billing service driving
`billing_subscriptions` with nipad access tokens, enforced lazily at the
presign/confirm gates).

## REST API

`ee/storageusage.RegisterRoutes` is attached to the authenticated `/api/v1`
WebService through `serverapp.WithRoutes` (go-restful rejects a second
WebService with the same root path, so extensions share the main one). With the
ledger disabled the routes are not registered and answer 404, so clients can
feature-detect the capability.

| Route | Guard | Purpose |
|---|---|---|
| `GET /api/v1/orgs/{org}/storage` | org owner or global admin | usage, quota, per-project breakdown for one org |
| `GET /api/v1/billing/me` | authenticated | owner quota, aggregate usage, per-org breakdown |
| `PUT /api/v1/admin/billing/owners/{user}/quota` | super admin | set/clear an owner's quota (`quota_bytes` null = unlimited) |
| `PUT /api/v1/admin/orgs/{org}/billing-owner` | super admin | transfer an org's billing owner |
| `POST /api/v1/admin/orgs/{org}/storage/recount` | super admin | rebuild ledger + counters for one org |

## Config and wiring (`ee/cmd/nipad`)

- `internal/config`: `StorageLedger` (`STORAGE_LEDGER`, empty = off, `cloud`
  enables the ledger) and `StorageLedgerDefaultQuotaBytes`
  (`STORAGE_LEDGER_DEFAULT_QUOTA_BYTES`, default 15 GiB, 0 = unlimited).
- `SAAS_MODE=1` implies `STORAGE_LEDGER=cloud` in `ee/cmd/nipad`
  (`createStorageLedger` falls back to the cloud ledger when the setting is
  empty): registration seeds an `owner_quota`, so the ledger must exist for
  that quota to be enforced and visible.
- The free `cmd/nipad` ignores `STORAGE_LEDGER` (it wires no ledger), so a free
  server started with the setting still runs unlimited and the storage routes
  answer 404.
- `ee/cmd/nipad` builds the ledger after `eedb.MigrateUp`, attaches it with
  `chunkUsecase.WithStorageLedger(ledger)` and registers the routes with
  `serverapp.WithRoutes` only when the ledger is enabled.
- The ledger is a thin object over `*sql.DB` built with `dbtx.New`, so every
  query resolves the transaction carried in the context and `Attribute` is one
  atomic transaction.

## Web UI

- `web/src/api/endpoints.ts`: `getOrgStorage` (returns null on 404 for feature
  detection), `getMyBilling`, `setOwnerQuota`, `setOrgBillingOwner`,
  `recountOrgStorage`.
- `OrgSettingsPage`: storage card (used/quota progress, per-project table,
  super-admin recount and billing-owner transfer), hidden when the API 404s.
- `/settings/billing`: the owner's aggregate usage, per-org table and quota,
  plus a super-admin quota editor. The page is always linked from the header
  and the profile settings nav (no feature detection); when the server has no
  ledger it shows the "Storage billing is not enabled on this server." empty
  state.

## Recount / backfill

Required once when enabling the ledger on an existing deployment and useful for
repair. `RecountOrg` rebuilds one organization's links and counters in a single
transaction: for each project it deletes the links and recomputes them from the
actual history (recursive commit-tree walk over `commits`, `tree_nodes`,
`files`, `file_chunks`, `chunks`), then derives the project and org counters.
It is exposed as the super-admin REST route.

## Testing

- `ee/storageusage`: postgres testcontainers suite for every method — lazy
  owner seeding, link idempotency and exactly-once counter increments, quota
  rejection rollback (no links, no counter changes), unlimited paths, shared
  quota across an owner's orgs, transfer moving the aggregate, recount
  repairing drift.
- `internal/usecase`: the seam is covered with a gomock ledger (presign
  headroom, confirm attribution, merge chunk attribution).
- `ee/e2e`: real-ledger integration through `Chunk` — a push over quota fails
  with 402, the retry after raising the quota succeeds.
- Ginkgo client suite (`tests/`): ee runs enable `STORAGE_LEDGER=cloud`
  (`NIPA_TEST_STORAGE_LEDGER`, default cloud); the storage spec trips the
  quota, raises it over the admin API, verifies the retry and checks the
  storage/billing endpoints, while the free run asserts the routes 404.

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
