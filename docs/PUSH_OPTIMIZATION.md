# Push Delta-Path Optimization Plan

Status: planned, not implemented.

## Context

Pushing a modified large file already uploads only changed chunks: the client
content-defines chunks, the server checks its content-addressed store, and
`AlreadyStored` chunks are skipped. The remaining cost is per-chunk bookkeeping:

- serial `Exists` checks during presign,
- redundant `Size` calls during confirm,
- per-chunk SQL for chunk ID lookups and `file_chunks` inserts,
- re-sending refs for chunks the client already knows the server has.

No wire-protocol changes are required. Phase 1 is server-only and low risk;
Phase 2 adds client-side state.

## Guiding principle

Only changed chunks should cross the network. Optimize the metadata and
bookkeeping around that, without changing upload correctness.

---

## Phase 0 - Baseline and guardrail

Add a delta-push benchmark before changing behavior:

- Extend `internal/e2e/bench_test.go` with a second pass gated by
  `NIPA_BENCH_MB`: push 1 GiB, overwrite a small region in the middle, stage,
  push again.
- Reuse the existing HTTP PUT counter to assert only the affected chunks are
  uploaded, and log wall time plus PUT count/bytes.
- Run it against the local free server and optionally ee/minio to expose S3
  HEAD latency.
- This locks in the current "only changed chunks are uploaded" guarantee while
  the rest is optimized.

---

## Phase 1 - Server bookkeeping (low risk, no client changes)

### 1. Parallelize presign existence checks

`presignUploadURLs` calls `chunkStore.Exists` serially
(`internal/usecase/chunk.go:213`). With S3 this is one HEAD per chunk:
roughly 256-4k serial requests for a 1 GiB file.

- Use a bounded worker pool or `errgroup` (16-32 workers) over the page,
  preserving result order.
- Keep metrics and tracing intact.
- Add a test with a fake store that records concurrency and propagates errors.

### 2. Confirm fast path

`confirmUploads` calls `chunkStore.Size` for every hash before checking
metadata (`internal/usecase/chunk.go:365-410`).

- Add a batched metadata lookup, e.g. `ChunkListByHashes`:
  - SQLite: `WHERE hash IN (sqlc.slice('hashes'))`, called in batches
    (around 400).
  - Postgres: `WHERE hash = ANY(sqlc.arg(hashes)::bytea[])`.
- Rewrite confirm:
  - Already recorded and not marked unverified: skip `Size` and
    `InsertChunkIfNotExists`; use the stored size for ledger/metrics.
  - Unrecorded or unverified direct upload: stat/verify exactly as today,
    then insert.
- Presign already verified object existence for these hashes, so skipping the
  second stat is safe.
- Tests: a recorded chunk never calls `Size`; missing objects still land in
  `missing`; unverified direct uploads still hash-verify; ledger attribution
  still uses correct sizes.

### 3. Batch apply SQL

Every new file version writes its full chunk list, even for a one-chunk edit:
4k chunks means roughly 4k `ChunkGetByHash` plus 4k `FileChunkInsert` calls
inside one transaction (`internal/repository/sqlite/push_repository.go:65`,
`ee/repository/postgres/push_repository.go:65`).

- Postgres: insert links with one statement per file using
  `unnest(hashes) WITH ORDINALITY JOIN chunks`, or batched ID prefetch plus a
  multi-row insert.
- SQLite: batched ID prefetch (`IN`) plus one insert per file via `json_each`
  over chunk IDs, or a prepared-statement loop if JSON typing is awkward.
- Add the generated sqlc queries to both query sets and run `make sqlc`.
- Tests: sqlite repository tests and the postgres testcontainer suite; verify
  duplicate chunks across files are deduped and missing chunks still fail with
  `missing chunk data`.

**Expected result:** repeated 1 GiB pushes no longer spend seconds in serial
S3 HEADs or thousands of SQL round trips. Uploaded bytes are unchanged.

---

## Phase 2 - Client remote-chunk cache (medium risk, biggest win)

Skip presigning chunks the clone already knows are on the server. Confirm still
includes every hash, so ledger attribution and missing-download detection stay
correct.

### 1. Local state

- Add to `internal/client/localrepo/schema/schema.sql`:

  ```sql
  CREATE TABLE IF NOT EXISTS remote_chunks (
      hash BLOB PRIMARY KEY
  ) WITHOUT ROWID;
  ```

  `Init` re-runs the schema with `IF NOT EXISTS`, so existing clones upgrade
  automatically.

- Add handwritten methods in `internal/client/localrepo` (batches of around
  400):
  - `FilterRemoteChunks(hashes) (unknown []Hash, err)` returns hashes not yet
    known.
  - `MarkRemoteChunks(hashes)` uses `INSERT OR IGNORE` in one transaction.
- This table is per clone, and the clone URL is fixed; stale entries are
  handled by the retry path below.
- Add tests for batching, duplicates, and empty input.

### 2. Seed from downloads

- In `downloadMissing` (`internal/client/usecase/update.go:434`), after hash
  verification succeeds, mark the downloaded hashes remote-known.
- This covers clone, update, switch, merge, revert, and diff downloads.
- Do **not** seed from `SaveTree`: merge/revert save locally generated trees
  there, which are not necessarily on the server.

### 3. Filter push refs

- In `chunkUploader.upload` (`internal/client/usecase/push.go:383`), filter
  each window through `FilterRemoteChunks`.
- Add `UploadChunksFiltered(ctx, scope, allChunks, skip map[Hash]bool,
  onChunk...)` in `internal/client/grpc/chunks.go`:
  - Presign/upload only chunks not in `skip`.
  - Confirm **all** hashes (including skipped), preserving ledger attribution
    and missing detection.
  - If confirm reports missing hashes, remove them from `skip`,
    presign/upload them, confirm again once; then error if still missing. This
    self-heals after server data loss.
  - Keep `UploadChunks` as a wrapper with an empty skip set.
- After a successful call, mark all window hashes remote-known.
- Keep storing all chunks in the local object cache and call `onChunk` for
  skipped chunks so progress stays accurate.
- Improve `Push.Plan` to subtract remote-known chunks from its upload
  estimate.

### 4. Tests

- Unit: skipped hashes produce no `GetChunkUploadUrls` refs, but all hashes
  reach confirm.
- Unit: confirm missing triggers a retry that uploads only the missing chunks.
- Unit: `downloadMissing` marks chunks.
- e2e: clone, edit a small region, push; assert the server receives refs only
  for changed chunks and that the commit lands.
- Cloud: an existing chunk in another project is skipped at presign but still
  attributed to the new project via confirm.

**Expected result:** after a clone or an earlier push, unchanged chunks cost
near zero server work per push, regardless of file size.

---

## Phase 3 - Tune what remains

- Add trace attributes/counters: chunks scanned, refs sent, presign checks,
  confirm recorded/new, apply link rows.
- Tune presign workers, SQL batch sizes, and possibly the 16 MB upload window
  based on the 1 GiB delta benchmark.
- Consider confirming once per push instead of per upload window only if
  tracing shows the extra RPCs matter.
- Do not change chunk profiles without benchmark evidence; they are a
  deliberate delta/request tradeoff.

---

## Out of scope

- rsync-style signatures to avoid reading the local file: the scan is
  CPU/disk-bound, not the bottleneck, and adds significant complexity.
- Chunk-size changes.
- Any proto or server-schema change.

## Verification

- `go test ./...`, `go vet ./...`
- `go test ./ee/...` for the postgres/minio suites
- `make test-client` (free and ee)
- `make lint`
- `NIPA_BENCH_MB=1024` delta benchmark before/after Phase 1 and Phase 2

## Suggested execution order

Do Phase 1 first and re-measure. Phase 2 is only worth its state/fallback
complexity if presign/confirm overhead still dominates after Phase 1.
