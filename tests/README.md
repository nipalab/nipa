# Client E2E Suite

Black-box end-to-end tests for the `nipa` client CLI against a real server.
The same Ginkgo suite runs against the free (`nipad`) and enterprise
(`nipad-ee`) servers so both editions are verified to behave identically from
the client's point of view.

Coverage (176 specs): every in-repo command and flag — `clone`, `branch`,
`add`, `remove`, `status`, `push`, `update`, `switch` (branch and `--tag`
detach), `merge`, `revert` (single, range, conflicts, `--mainline`), `log`,
`diff` (all output modes and revision shapes), `sparse-checkout`, `tag`,
`lock`/`unlock`, `mr`, `acl` (rules and path defaults), `group` — plus the
runtime commands `nipa serve` (daemon gRPC API), `nipa mcp` (MCP over stdio)
and the webhook delivery pipeline (push/branch/tag/mr events, signatures,
filters, ping, retries/redelivery), multi-user permission/lock scenarios,
`NIPA_OUTPUT=json` and the exit-code contract (0/1/2/127).

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

## Authentication and identities

The CLI stores tokens in the OS keyring and prompts on stdin with
`term.ReadPassword`, neither of which works in CI. `securestorage` therefore
honors `NIPA_TOKEN_FILE`: when set, tokens are read/written as a JSON map
(`host -> token`) in that file (0600, atomic writes). The suite logs in once
in-process via the gRPC client and seeds that file; every CLI invocation runs
with the same `NIPA_TOKEN_FILE`.

Multi-user specs create accounts through the admin REST API and give each one
its own token file (`newIdentity` / `runNipaAs` / `cloneRepoAs`); `.promote()`
re-mints the token after granting admin rights because the admin claim is
embedded at login time.

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
    support_*.go          env, CLI exec, REST, auth/identities, JSON DTOs,
                          workspaces, seeds, external diff, daemon, MCP
    clone_test.go         nipa clone
    branch_test.go        nipa branch
    push_test.go          nipa push (+ add/remove/status glue)
    update_test.go        nipa update
    switch_test.go        nipa switch (branches, detached tags)
    merge_test.go         nipa merge
    revert_test.go        nipa revert (ranges, conflicts, mainline)
    log_test.go           nipa log
    diff_test.go          nipa diff (formats, revisions, ext-diff)
    sparse_test.go        nipa sparse-checkout
    tag_test.go           nipa tag
    lock_test.go          nipa lock/unlock
    mr_test.go            nipa mr
    acl_test.go           nipa acl + permission defaults
    group_test.go         nipa group
    meta_test.go          JSON output modes and error envelopes
    daemon_test.go        nipa serve (daemon gRPC API)
    mcp_test.go           nipa mcp (MCP over stdio)
    webhook_test.go       webhook deliveries (events, filters, signature)
```

## Adding specs

- One `Describe` per command in a `<command>_test.go` file; keep the spec text
  edition-neutral so the same suite runs against both servers.
- Drive the CLI through `runNipa(cwd, args...)` (or `runNipaAs(identity, ...)`)
  and assert on exit codes, combined output and working-copy state. Exit codes
  are a contract: `0` ok, `1` generic, `2` conflict/precondition (409), `127`
  not found (404). Some commands print through cobra's stderr writer, so use
  `result.Output()` for text assertions and `result.Stdout` for `--json`.
- Seed server state with `seedRepo`/`seedRepoDelete` (in-process usecases) and
  create isolated projects with `newProject(prefix)`.
- Message uniqueness matters: the server hashes commits from tree, parents and
  message only, and `commits.hash` is globally unique, so identical first
  commits in two projects would collide. Always push through `pushRepo` /
  `seedRepo`, which append a unique suffix to the message.
- Daemon specs use `startDaemon()` (spawns `nipa serve`, reads the discovery
  file and dials the loopback gRPC API with the capability token); MCP specs
  use `startMCP()` / `startMCPWithTokenFile()` and speak newline-delimited
  JSON-RPC over the child's stdio.
- Webhook specs use `startHookReceiver()` (in-process HTTP receiver recording
  signed deliveries) plus the REST helpers `createWebhook`/`updateWebhook`/
  `rotateWebhookSecret`/`testWebhook`/`listWebhookDeliveries`/`redeliverWebhook`.
  The generated server config sets `WEBHOOK_EGRESS_ALLOWLIST: 127.0.0.1` so the
  loopback receiver is reachable; deliveries are asynchronous, so specs wait on
  the receiver (`wait`/`waitCount`) or the delivery ledger with `Eventually`.
