# Load scenarios

`tests/load` drives the real client usecase graph in-process (one gRPC client,
many worker goroutines, one temporary worktree per worker) against a running
`nipad` or `nipad-ee`. It complements:

- `internal/e2e` benches (`make bench-upload` / `bench-download` / `bench-scale`)
  — single client against an in-process server, no orchestration;
- `tests/run.sh` — black-box correctness suite, not a load tool.

## Usage

Point it at a scratch server (the migration-seeded default org/project and
`nipa`/`nipa` credentials work out of the box):

```sh
tests/run.sh free --keep            # or start bin/nipad manually
go run ./tests/load -scenario push -clients 16 -duration 60s -size-mb 32
go run ./tests/load -scenario clone -clients 16 -duration 60s
go run ./tests/load -scenario mixed -clients 16 -duration 60s
make load-test args='-scenario push -clients 16 -json'
```

Flags (all have `NIPA_LOAD_*` env fallbacks where noted):

| Flag | Default | Meaning |
|---|---|---|
| `-host` | `127.0.0.1:6745` (`NIPA_LOAD_HOST`) | server host:port |
| `-org` / `-project` | `default` | target project |
| `-base-branch` | `main` | scenario branches fork from here |
| `-scenario` | `push` | `push`, `clone` or `mixed` |
| `-clients` | `8` | concurrent worker worktrees |
| `-duration` | `30s` | run length |
| `-size-mb` | `8` | MiB written per file and per push round |
| `-files` | `4` | files per push round |
| `-user` / `-pass` | `nipa` (`NIPA_LOAD_USER`/`PASS`) | login credentials |
| `-metrics` | `http://<host>/metrics` | Prometheus endpoint; `off` disables |
| `-json` | `false` | machine-readable summary |
| `-max-error-rate` | `0.05` | exit non-zero above this error fraction |
| `-keep-state` | `false` | keep worktrees and branches for inspection |

## Scenarios

- **push** — each worker clones its own branch, then loops: write `-files`
  incompressible files of `-size-mb`, stage them, `push`. Measures end-to-end
  push latency (chunking, uploads, confirm, commit) and wire bytes.
- **clone** — each worker repeatedly clones its branch into a fresh temp
  worktree, measures full clone (manifest + chunk download + materialization)
  and deletes it.
- **mixed** — half the workers push (as above), half read: commit log, tree
  manifest, branch list and a no-op `update` in a loop. Approximates the
  browsable-SPA + editor-client mix.

All scenarios seed a `load-<run>-seed` branch (one `-size-mb` file) first so
clones download real content, then fork `-clients` branches
`load-<run>-<i>`. Branches and worktrees are cleaned up unless `-keep-state`.

## What it reports

- per-op count, errors and client-observed latency p50/p95/p99;
- wire bytes: PUT/POST (upload) and GET (download) MiB/s;
- total error rate; the run exits 1 when the rate exceeds `-max-error-rate`
  or when any worker fails outside a recorded op window (setup clone errors,
  a push that stops the worker, ...), which also shows up as `worker_errors`
  in the `-json` summary;
- a delta of the server's Prometheus counters/gauges scraped before and after
  the run, summed across labels, top 30 by absolute delta. Watch in particular:
  `nipa_db_pool_wait_count_total`, `nipa_db_pool_wait_seconds_total`,
  `nipa_db_pool_in_use`, `nipa_chunk_ops_total`, `nipa_chunk_bytes_total`,
  `nipa_chunk_verify_failures_total` (see `docs/OBSERVABILITY.md`).

Duration histograms (`nipa_http_request_duration_seconds`,
`nipa_grpc_request_duration_seconds`) land in the delta as summed `_count` and
`_sum` series, so they read as an overall average, not per-route percentiles —
use Prometheus or Grafana for real percentiles during a soak run.

## Interpreting results

- Free `nipad` is single-instance SQLite: high `-clients` push concurrency is
  expected to surface `SQLITE_BUSY` above its comfortable envelope. That is a
  finding about the free-edition ceiling, not a regression — record the
  largest `-clients` that stays clean.
- `nipad-ee` is the SaaS shape: watch postgres pool waits and S3 wire rates.
  Run against a staging-like deployment (real postgres + S3, not minio) before
  ratifying capacity numbers.
- Scenario setup uses the migration-seeded admin credentials; never point it
  at a production server without a dedicated project (`-org`/`-project`) and
  note that it creates and deletes branches.
