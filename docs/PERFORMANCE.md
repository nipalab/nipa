# Nipa Performance & Capacity

Benchmarks, load scenarios and SaaS capacity planning for both editions.
This doc defines what to measure, how to reproduce it, and which numbers a
release must ratify; it complements `docs/OBSERVABILITY.md` (what the server
exposes) and `docs/CLOUD_STORAGE_QUOTA.md` (metering).

**Status:** harness implemented (2026-10-15) — upload bench, clone bench,
GB-scale memory check, package micro-benchmarks and the `tests/load` scenarios
all run today. SLO targets and the capacity envelope are **proposals to
ratify before v0.1.0**; the baseline table at the bottom is filled by the
release run.

**Scope decisions:**

- Client-side measurement first: the benches drive the real client usecase
  graph (chunking, transfer limiter, materialization), not a synthetic HTTP
  load generator, because that is what users experience.
- The free `nipad` is deliberately single-instance SQLite; its ceiling is a
  documented envelope, not a scalability target.
- SaaS shape = `nipad-ee` (postgres + S3) behind a TLS reverse proxy, N pods,
  one postgres cluster and one S3 bucket. Capacity work targets that shape.
- CDN/edge caching and per-tenant request rate limiting are post-beta.

## Current state (audit)

| Area | State |
|---|---|
| Upload bench | `internal/e2e/bench_test.go` `TestEndToEnd_UploadThroughput` — `make bench-upload mb=512`, text/binary/packed, logs MiB/s, encoding, chunk/put counts |
| Download bench | `TestEndToEnd_CloneThroughput` — `make bench-download mb=512`, times a fresh clone plus a no-op update |
| GB-scale check | `TestEndToEnd_GBScaleMemoryCeiling` — `make bench-scale gb=2`, pushes + clones one N-GiB file and asserts peak heap growth stays below the file size (streaming, not buffering) |
| Fuzzing | `make fuzz FUZZTIME=1m` — chunker encode/decode round trip, Myers edit script, text merge identities, ignore matcher, `ParseNipaUrl`; seeds run in every `go test` |
| Micro-benchmarks | `internal/chunker`, `internal/diff`, `internal/treehash` — `make bench-unit` (encode, Myers diff, tree hashing) |
| Load scenarios | `tests/load` — `make load-test args='-scenario push|clone|mixed ...'`, see `tests/load/README.md` |
| Transfer tuning | Client adaptive limiter, concurrency 16 → range [4, 64], pinned by `NIPA_UPLOAD_WORKERS` / `uploadWorkers` (`internal/client/grpc/transfer.go`) |
| Big-repo hygiene | `SaveTree` diffs against the stored rows and writes only the difference (~20 ms for 10k files and 5 changes, see "Wide-tree scale"), `stat_cache` keeps status/diff off the file system |
| Metrics | Request-duration histograms, DB-pool gauges/waits, chunk ops/bytes, verify failures (`docs/OBSERVABILITY.md`) |
| Postgres pool | **Untuned**: no `SetMaxOpenConns`/`ConnMaxLifetime` in production code — Go's unlimited default can exhaust `max_connections` across pods |
| Rate limiting | Token buckets per IP / account / user, memory or Redis store (`docs/RATE_LIMIT.md`) |

## Benchmarks

Run these on the release machine and paste the output into the release notes /
baseline table. They are skipped unless the env var is set, so CI stays fast.

| Target | What it measures | Baseline run |
|---|---|---|
| `make bench-upload mb=512` | hash+encode+presign+PUT+confirm for one 512 MiB file per class | release commit |
| `make bench-download mb=512` | manifest + chunk download + materialization (fresh clone) and a no-op update | release commit |
| `make bench-scale gb=2` | one 2 GiB binary: push/clone rates + peak heap vs file size | release commit |
| `make bench-unit` | chunker encode (text/packed/unknown), Myers diff, tree hash throughput | release commit |
| `make bench-scale-files files=10000` | wide-tree scale on SQLite: initial and one-file push, recursive manifest, commit and merge-request diff, clone, `ApplyPush` alone, `SaveTree` with 5 changes | per change that touches these paths |
| `make bench-scale-files-ee files=10000` | the same push/manifest/diff scenario and `ApplyPush` on postgres + MinIO (Docker); `NIPA_BENCH_LATENCY=500us` adds a one-way delay in front of postgres | per change that touches these paths |

Expected shape (loopback, modern laptop): encode is roughly 0.5–1 GiB/s for
packed/unknown binaries and BLAKE3 ~4 GiB/s, so network and disk dominate; the
material numbers to compare across releases are MiB/s and peak heap in
`bench-scale`. Always re-run with the same `NIPA_BENCH_WORKERS` when comparing.

### Wide-tree scale (10 directories, 10k single-chunk text files)

Baselines recorded 2026-11-02 on a Ryzen 7 6800H laptop, loopback, postgres 17
and MinIO in local containers. Use them to judge changes to the push write
path, manifest loading, diffs and `SaveTree`; the scenarios push all files,
then change one file inside a 1000-file directory.

| Step | SQLite (`nipad`) | Postgres + MinIO (`nipad-ee`) |
|---|---|---|
| Initial push | 2.6 s (was 3.4 s) | 16.2 s (was 30.3 s) |
| `ApplyPush` alone | 0.19 s (was 0.68 s) | 0.29 s (was 3.05 s) |
| One-file change push | 0.14 s (was 0.75 s) | 0.15 s (was 0.87 s) |
| Recursive manifest | 42 ms | 44 ms |
| Commit diff / merge-request diff | 12 ms / 12 ms (was 96 / 98) | 14 ms / 13 ms (was 96 / 99) |
| Fresh clone | 2.1 s | 2.6 s (was 3.3 s) |
| `SaveTree`, 5 changed files | 19 ms (was 0.85 s; 150k allocs) | n/a (client) |

Reading the numbers: the postgres push is dominated by chunk transfer to MinIO
on loopback. `ApplyPush` used to cost one statement per chunk, file and link, so
it was cheap on loopback but grew with every millisecond between the server and
postgres. It now resolves all chunks in one lookup and inserts files and links
in bulk (`internal/repository/pushbatch`), so it no longer depends on latency:

| `ApplyPush`, postgres, 2000 files | Before | After |
|---|---|---|
| Loopback | 0.47 s | 0.06 s |
| 0.5 ms round trip | 3.9 s | 0.09 s |
| 1 ms round trip | 7.0 s | 0.10 s |

`NIPA_BENCH_LATENCY` puts a precise one-way delay in front of postgres. At 10k
files and a 1 ms round trip `ApplyPush` now takes 0.34 s; the old code issued
about 30k statements, which extrapolates from the runs above to roughly 35 s of
apply time inside one transaction.

`SaveTree` compares the new tree with the stored rows in memory and writes only
the rows that differ, so 5 changed files cost a read of the stored rows plus
about a dozen writes instead of a rewrite of all 10k.

Commit and merge-request diffs no longer load whole manifests: they walk the
two trees in lockstep (`Branch.changedTreeEntries`) and skip every directory
pair whose stored hash is equal, so they cost the changed directories (here one
1000-file directory per side) instead of the repository. Merge and fast-forward
write checks and binary-lock planning (`commitChanges`) use the same walk, and
`VisibleChunks` skips directories it has already visited across the requested
commits. The stored hash is compared before permission filtering; both sides
use the same filter, so equal hashes stay equal after it.

## Load scenarios (`tests/load`)

`make load-test args='-scenario push -clients 16 -duration 60s'` against a
staging-like server. The runner creates a seed branch plus one branch per
worker, one temp worktree per worker, records client-observed p50/p95/p99 per
operation and wire bytes, and scrapes the server's `/metrics` before/after
printing counter deltas.

| Scenario | Load shape | Watch |
|---|---|---|
| `push` | N workers, own branch, `-files` incompressible files per round | push p99, `nipa_db_pool_wait_*`, `SQLITE_BUSY` (free) |
| `clone` | N workers cloning the same branch repeatedly | clone p99, GET wire rate, server read amplification |
| `mixed` | half pushing, half reading (log/manifest/branches/no-op update) | read p99 vs write p99 interference |

Findings to record per run: max clean `-clients` per edition, error rate,
pool waits, wire throughput. On free this establishes the envelope; on ee it
is the pod-sizing input.

## SLO proposals (ratify at v0.1.0)

| Target | Proposal | Where measured |
|---|---|---|
| Manifest RPC, 10k files recursive | p95 < 1s, p99 < 2s | `tests/load -scenario mixed` |
| No-op update, 10k-file clone | < 1s | `make bench-download` |
| Push, single client on 1 GbE | ≥ 50 MiB/s wire | `make bench-upload` |
| Clone, 5 GiB repo on 1 GbE | ≥ 60% of link rate, no OOM | `make bench-download mb=...` |
| Server pod, 64 concurrent transfers | peak < 1 GiB | `bench-scale` + pod profiling |
| Browse REST endpoints | p99 < 500ms on a 100k-file project | `tests/load` metrics delta |
| ee error rate at design concurrency | < 0.1% | `tests/load` |

## ee / SaaS capacity

- **Scale-out shape.** `nipad-ee` keeps no per-instance state beyond the
  postgres pool and S3 client; every instance leases its snowflake node id
  (`ee/nodelease`, `SNOWFLAKE_LEASE_TTL_SECONDS`) so pods can be added or
  replaced freely. Storage and quota accounting live in postgres
  (`ee/storageusage`).
- **Postgres pool sizing is an open item.** The pool is untuned today
  (stdlib default = unlimited connections). Before beta, add
  `DB_POOL_MAX_OPEN`/`DB_POOL_MAX_IDLE`/`DB_POOL_CONN_MAX_LIFETIME` knobs and
  size per pod so `pods × max_open + headroom < max_connections`; the
  `nipa_db_pool_*` gauges and `tests/load` pool-wait deltas verify the setting.
- **S3.** The enterprise store hands clients absolute presigned URLs
  (`DirectTransferStore`), so upload/download bytes bypass the server; pods
  mostly serve metadata. Watch egress cost and per-object request rate rather
  than pod bandwidth; chunk sizes (64 KiB text / 1 MiB unknown / 4 MiB packed)
  set the object-count factor, and text single-blob zstd keeps text objects
  few. A CDN in front of downloads is a post-beta optimization only if GET
  latency demands it.
- **Memory.** Both transfer directions are bounded (16 MiB windows, queue
  depth 2, limiter ≤ 64 concurrent chunks). `bench-scale` asserts heap does
  not scale with file size; pod limits should still be set from the `mixed`
  scenario peak, not guessed.
- **Quota enforcement under load.** `StorageLedger` headroom checks are
  advisory at presign and authoritative at confirm (`docs/CLOUD_STORAGE_QUOTA.md`); the load run should include a near-quota project to confirm 402s
  arrive before bytes are accepted, not after.
- **Background load.** Webhook and email dispatchers poll and retry; high
  concurrent MR activity multiplies deliveries. The `mixed` scenario plus a
  webhook subscriber is the next harness increment if delivery lag shows up.

## Web browse endpoints

`GET .../tree` (incl. `history=1`) and `GET .../blob` only load what they
return: `Branch.treeAt` reads one directory level through `shallowDirAt` when
the caller can read everything (restricted callers still take the full
filtered manifest path, because their tree hashes are computed over the
visible subset), the history walk compares stored child hashes one level per
commit, and `FileEntry` walks only the file's ancestors. `GetBlob` checks the
declared size (413) before reading chunks, serves `ETag` = file hash with
`If-None-Match` 304s, and `diff.LoadContent` preallocates from the file size.

### Deferred: simplify the `tree_nodes` content lookup

Tree nodes are deduplicated by hash: identical subtrees share a hash, but only
one `tree_nodes` row holds the files and children. `TreeNodeGetChildByName`,
`TreeNodeListChildren`, `FileListByTree` and `ChunkListByTree`
(`db/queries/sqlite/tree.sql` and the postgres twins) therefore resolve the
"content" row on every call with a `tree_nodes` self-join on `hash`, two
`EXISTS` probes and `ORDER BY id LIMIT 1`; `ChunkListByTree` nests it twice.

Not done because the shallow loads left only a handful of these queries per
request and the hash lookup is indexed; revisit if profiling shows it on large
directories or deep history walks. Plan:

1. Add `tree_nodes.content_id` (the row that holds the files/children, itself
   for the holder), set where push creates nodes in
   `internal/repository/sqlite` and `ee/repository/postgres`. Pre-release, so
   fold it into the initial migrations and recreate dev databases.
2. Rewrite the four queries to use `content_id` instead of the self-join and
   `EXISTS` probes; run `make sqlc`.
3. Cheap, independent step: fold `ChunkListByTree` into one
   `files JOIN file_chunks` query.
4. Cover with push and e2e tests on both editions; the write path is where a
   bug would corrupt trees.

Other deferred ideas: LRU cache of decoded chunks, cache of last-commit
results keyed by (project, commit, path, filter signature), TTL cache for
`resolveProject`.

## Free-edition envelope

SQLite + local chunks is single-instance by design (WAL, `busy_timeout(10s)`,
immediate transactions — see AGENTS.md). Use `tests/load -scenario push` to
find the clean concurrency for the target hardware; document the recommended
team/repo-size ceiling in the README at beta rather than promising SaaS
numbers. Users past the envelope should move to `nipad-ee`.

## Recorded baselines

Fill in on the release commit (one row per release; keep the machine notes).

| Date | Commit | `bench-upload` 512 MiB | `bench-download` 512 MiB | `bench-scale` 2 GiB (push/clone, peak heap) | Load: max clean clients (free/ee) |
|---|---|---|---|---|---|
|  |  |  |  |  |  |
