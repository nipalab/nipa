# Client E2E Suite

Black-box end-to-end tests for the `nipa` client CLI against a real server.
The same Ginkgo suite runs against the free (`nipad`) and enterprise
(`nipad-ee`) servers so both editions are verified to behave identically from
the client's point of view.

Coverage (about 325 specs, counted as the `It(`/`PIt(` calls in the
non-support spec files): every in-repo command and flag — `login` (SSH key:
key-only sign-in, lazy sign-in for clone through `NIPA_SSH_KEY`,
unregistered-key rejection, username binding, deleted keys, default
`~/.ssh` key discovery and ssh-agent), `clone`, `branch`,
`add`, `remove`, `restore`, `status`, `push`, `update`, `switch` (branch and
`--tag` detach), `merge`, `revert` (single, range, conflicts, `--mainline`),
`log`,
`diff` (all output modes and revision shapes), `sparse-checkout`, `tag`,
`lock`/`unlock`, `mr`, `acl` (rules and path defaults), `group`, `shelve`
(create, list, show, apply with merge/conflicts/continue/abort, drop) — plus the
runtime commands `nipa serve` (daemon gRPC API), `nipa mcp` (MCP over stdio)
and the webhook delivery pipeline (push/branch/tag/mr events, signatures,
filters, ping, retries/redelivery), multi-user permission/lock scenarios
(org owners' implicit full project access and plain members' deny-by-default),
`NIPA_OUTPUT=json` and the exit-code contract (0/1/2/127), and the AI
surfaces: MCP tools/prompts end to end (merge request review loop, scoped
`--allow`, asset diffs, Godot merge, locks), `nipa assets check` and
`nipa push --generate`. REST-only
surfaces are covered too: email notifications, the admin audit log, users/
orgs/projects/branches/browser admin routes, signed chunk URLs, storage
quota, rate limiting and project service tokens (lifecycle, per-scope route
allowlist, commit ancestry, CI checks, webhook management, audit tagging,
expiry and creator demotion/deletion).

The enterprise run additionally enables the SaaS onboarding stack
(`SAAS_MODE`) and covers it through the REST surface: registration with the
email-confirmation gate (unconfirmed login refused, one-time verification
token read out of the in-suite email receiver, single-use enforcement,
resent emails voiding the old token, duplicate/weak-password rejections, gRPC
login of the confirmed account) and the identity routes without providers.
On free the same spec asserts the routes stay absent (404).

## Usage

```sh
tests/run.sh free            # free server: sqlite + local chunks
tests/run.sh ee              # enterprise server: postgres + minio containers
tests/run.sh both            # free first, then ee
```

Useful options and environment:

```sh
tests/run.sh free --filter "nipa push"    # Ginkgo focus regex
tests/run.sh free --filter "nipa mcp"     # all MCP specs (mcp_test.go + mcp_workflow_test.go)
tests/run.sh free --filter "nipa mcp workflows"  # only the MCP workflow specs
tests/run.sh ee --keep                    # keep postgres/minio after the run
SKIP_BUILD=1 tests/run.sh free            # reuse binaries already in bin/ (rebuild after server changes)
NIPA_TEST_PORT=7000 tests/run.sh free     # free server port (default 6745)
NIPA_TEST_EE_PORT=7001 tests/run.sh ee    # enterprise server port (default 6747)
NIPA_TEST_EMAIL_TRANSPORT=http tests/run.sh free  # email transport: sendgrid, scaleway or http (all carry Message-ID/In-Reply-To/References)
NIPA_TEST_SAAS= tests/run.sh ee           # disable the SaaS onboarding config + spec
NIPA_TEST_POSTGRES_DSN=... NIPA_TEST_S3_ENDPOINT=... tests/run.sh ee   # external services
NIPA_TEST_REDIS_PORT=6380 tests/run.sh ee # redis port for the ee rate limit store (default 6379)
NIPA_TEST_REDIS_ADDR=host:6379 tests/run.sh ee # external redis; with external postgres/s3 and no address the ee run uses the memory store
NIPA_TEST_RATELIMIT=1 tests/run.sh free   # rate-limit profile (see below)
```

Rate-limit profile (free only): `NIPA_TEST_RATELIMIT=1` also starts a second
free `nipad` with low per-scope limits (`auth_ip`, `api_user`, `chunk_ip`,
`global_ip`; trusted proxies `127.0.0.1/32`) on `NIPA_TEST_RATELIMIT_PORT`
(default 6749, log in `tests/.tmp/logs/free-ratelimit.log`), exports its
address as `NIPA_TEST_RATELIMIT_HOST` and focuses the run on
`"rate limiting"` unless `--filter` is given. The per-identity `auth_id`
specs run against the regular server on every run; the low-limit specs skip
without `NIPA_TEST_RATELIMIT_HOST`.

Two-factor-off server (free only): every free run also starts a free `nipad`
with `MFA_ENABLED: false` on `NIPA_TEST_MFAOFF_PORT` (default 6751, log in
`tests/.tmp/logs/free-mfaoff.log`) and exports it as `NIPA_TEST_MFAOFF_HOST`.
The regular servers run with MFA on; the MFA-off spec skips without the host.

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
logs in as the migration-seeded `nipa`/`nipa`, creates a uniquely
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
                          workspaces, seeds, external diff, daemon, MCP,
                          SSH keys, hook/email receivers, pipe/PTY runners
                          (support_pty_test.go: runNipaPiped, runNipaPTY,
                          colorEnv)
    clone_test.go         nipa clone
    branch_test.go        nipa branch
    push_test.go          nipa push (+ add/remove/status glue)
    restore_test.go       nipa restore (discard modifications/deletions)
    update_test.go        nipa update
    switch_test.go        nipa switch (branches, detached tags)
    shelve_test.go        nipa shelve (untracked selection, binary/text
                          conflicts, continue/abort, tag and sparse clones,
                          refusals while an apply waits) and the daemon
                          shelf RPCs, SyncPlan and JSON bridge
    merge_test.go         nipa merge
    revert_test.go        nipa revert (ranges, conflicts, mainline)
    status_test.go        nipa status/add/remove staging (--no-cache, dirs)
    log_test.go           nipa log (detached, pager only on a terminal)
    diff_test.go          nipa diff (formats, revisions, ext-diff, color)
    ignore_test.go        .nipaignore / .nipa/ignore rules (add -f, sparse)
    sparse_test.go        nipa sparse-checkout
    tag_test.go           nipa tag
    lock_test.go          nipa lock/unlock (branch scope, released on delete)
    mr_test.go            nipa mr (reviews, threads, assignees, checks)
    acl_test.go           nipa acl + permission defaults
    org_owner_test.go     org owners' implicit project access vs. members
    group_test.go         nipa group
    meta_test.go          JSON output modes and error envelopes
    onboarding_test.go    SaaS registration + email confirmation (ee only)
    org_invitation_test.go organization invitations
    profile_test.go       profile email-notification toggle
    ssh_test.go           SSH key login (deleted keys, ~/.ssh, ssh-agent)
    daemon_test.go        nipa serve (gRPC API, LoginSSH, JSON bridge token,
                          error mapping and host/origin checks)
    mcp_test.go           nipa mcp (tool lists, --allow, read tools, push/MR
                          basics, permission errors, prompts list)
    mcp_workflow_test.go  nipa mcp workflows: MR review loop over MCP
                          (diff budgets, comments, threads, review by a second
                          identity, checks, timeline, Nipa-Agent trailers,
                          nipa_review_mr prompt), --allow scoping, Unity scene
                          diffs with GUIDs and .meta warnings, MR asset
                          summaries, Godot structural merge, lock scopes
    assets_test.go        nipa assets check (.nipaassets.yml, dry run, MCP)
    generate_test.go      nipa push --generate against a fake LLM endpoint
    webhook_test.go       webhook deliveries (events incl. mr.ready_for_review,
                          filters, signature)
    email_test.go         email notifications (lifecycle, thread headers,
                          retries/redelivery)
    audit_test.go         admin audit log REST API
    admin_rest_test.go    admin REST (auth refresh/logout, users, orgs,
                          projects, branches, browser, signed chunk URLs)
    storage_quota_test.go storage quota (ee) / absent routes incl. billing
    ratelimit_test.go     rate limiting (auth_id; low-limit profile scopes)
    mfa_test.go           two-factor login (REST, gRPC, CLI prompt/NIPA_OTP,
                          daemon, SSH/refresh bypass, attempt cap, recovery
                          codes, admin reset, audit, emails, MFA off)
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
- `admin_rest_test.go` signs expired chunk URLs itself with `chunkSigningKey`,
  a copy of `CHUNK_URL_SIGNING_KEY` from `run.sh`; keep the two in sync.
- Known product bugs are kept as pending specs (`PIt`) naming the cause, so the
  run stays green and the spec flips to `It` with the fix. Currently:
  `nipa add .` rejected by `validateRelPath` (`status_test.go`) and clone
  defaulting `--branch` to `main` instead of the server default branch
  (`admin_rest_test.go`).
