# Client E2E Suite

Black-box end-to-end tests for the `nipa` client CLI against a real server.
The same Ginkgo suite runs against the free (`nipad`) and enterprise
(`nipad-ee`) servers so both editions are verified to behave identically from
the client's point of view.

Phase 1 covers `nipa clone`, `nipa push` and `nipa update` (plus the
`nipa add` / `nipa remove` / `nipa status` glue needed to drive them).

## Usage

```sh
tests/run.sh free            # free server: sqlite + local chunks
tests/run.sh ee              # enterprise server: postgres + minio containers
tests/run.sh both            # free first, then ee
```

Useful options and environment:

```sh
tests/run.sh free --filter "nipa push"    # Ginkgo focus regex
tests/run.sh ee --keep                    # keep postgres/minio after the run
SKIP_BUILD=1 tests/run.sh free            # reuse binaries already in bin/
NIPA_TEST_PORT=7000 tests/run.sh free     # free server port (default 6745)
NIPA_TEST_EE_PORT=7001 tests/run.sh ee    # enterprise server port (default 6747)
NIPA_TEST_POSTGRES_DSN=... NIPA_TEST_S3_ENDPOINT=... tests/run.sh ee   # external services
```

Makefile shortcuts: `make test-client`, `make test-client-ee`,
`make test-client-both`.

## How it works

`tests/run.sh`:

1. builds `bin/nipa`, `bin/nipad` and/or `bin/nipad-ee`;
2. for `ee`, starts `tests/docker-compose.ee.yml` (postgres 17 + minio), waits
   for readiness and creates the S3 bucket via `tests/cmd/s3bucket`;
3. writes a per-edition `config.yaml` into `tests/.tmp/server-<edition>/` and
   starts the server there (the server requires `config.yaml` in its working
   directory and applies migrations at startup);
4. runs the Ginkgo suite (`go test ./tests/...`) with `NIPA_TEST_*` env;
5. stops the server and tears the containers down (unless `--keep`).

The suite is a normal Go test package, so `go test ./...` compiles it but
skips it when `NIPA_TEST_HOST` is unset.

## Authentication

The CLI stores tokens in the OS keyring and prompts on stdin with
`term.ReadPassword`, neither of which works in CI. `securestorage` therefore
honors `NIPA_TOKEN_FILE`: when set, tokens are read/written as a JSON map
(`host -> token`) in that file (0600, atomic writes). The suite logs in once
in-process via the gRPC client and seeds that file; every CLI invocation runs
with the same `NIPA_TOKEN_FILE`.

Setup data (organization, project, seed commits) is created per run: the suite
logs in as the migration-seeded `supernipa`/`supernipa`, creates a uniquely
named org via the REST API and one project per spec, and seeds content with the
client usecases in-process (`support_seed_test.go`), independent of the binary
under test.

## Layout

```
tests/
  run.sh                  orchestrator (build, containers, server, suite)
  docker-compose.ee.yml   postgres + minio for the enterprise run
  cmd/s3bucket/           creates the S3 bucket before nipad-ee starts
  suite/
    suite_test.go         RunSpecs, BeforeSuite (login + org), project factory
    support_*.go          env, CLI exec wrapper, REST client, auth, workspaces, seeds
    clone_test.go         nipa clone specs
    push_test.go          nipa push specs
    update_test.go        nipa update specs
```

## Adding specs

- One `Describe` per command in a `<command>_test.go` file; keep the spec text
  edition-neutral so the same suite runs against both servers.
- Drive the CLI through `runNipa(cwd, args...)` and assert on exit codes,
  combined output and working-copy state. Exit codes are a contract: `0` ok,
  `1` generic, `2` conflict/precondition (409), `127` not found (404).
- Seed server state with `seedRepo`/`seedRepoDelete` (in-process usecases) and
  create isolated projects with `newProject(prefix)`.
- Message uniqueness matters: the server hashes commits from tree, parents and
  message only, and `commits.hash` is globally unique, so identical first
  commits in two projects would collide. Always push through `pushRepo` /
  `seedRepo`, which append a unique suffix to the message.
