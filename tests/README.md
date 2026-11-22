# Client E2E Suite

Black-box end-to-end tests for the `nipa` client CLI against a real server.
The same Ginkgo suite runs against the free (`nipad`) and enterprise
(`nipad-ee`) servers so both editions are verified to behave identically from
the client's point of view.

Coverage (393 specs, counted as the `It(`/`PIt(` calls in the
non-support spec files): every in-repo command and flag — `login` (SSH key:
key-only sign-in, lazy sign-in for clone through `NIPA_SSH_KEY`,
unregistered-key rejection, username binding, deleted keys, default
`~/.ssh` key discovery and ssh-agent), `clone`, `branch`,
`add`, `remove`, `restore`, `status`, `push`, `update`, `switch` (branch and
`--tag` detach), `merge`, `revert` (single, range, conflicts, `--mainline`),
`log`,
`diff` (all output modes and revision shapes), `sparse-checkout`, `tag`,
`lock`/`unlock`, `mr`, `acl` (rules and path defaults), `group`, `shelve`
(create, list, show, apply with merge/conflicts/continue/abort, drop),
`ignore import` (.gitignore to .nipaignore: merge, idempotence, unsupported
syntax warnings, errors), `provenance init/show/set/report` (every `set`
flag, partial updates, recorded/edited/modified/missing round trip through a
clone, path and input errors), `blame`, `gc`, `import git` — plus the
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
expiry and creator demotion/deletion), project deletion and restore on both
editions (CLI cut off with exit 127, the deleted-projects list and its
`purge_after` restore window, owner/admin-only delete/list/restore, reserved
slugs, invalid restores, history intact after a restore; on ee the storage
ledger releasing the bytes on delete, a 402 restore over quota and the
charge-back), and maintenance mode (project
read-only/closed windows across CLI, `nipa serve` and `nipa mcp`, a frozen
server through the `MAINTENANCE_FILE` override that `run.sh` configures and
exports as `NIPA_TEST_MAINTENANCE_FILE`, and the announcement emails).

The enterprise run additionally enables the SaaS onboarding stack
(`SAAS_MODE`) and covers it through the REST surface: registration with the
email-confirmation gate (unconfirmed login refused, one-time verification
token read out of the in-suite email receiver, single-use enforcement,
resent emails voiding the old token, duplicate/weak-password rejections, gRPC
login of the confirmed account), the identity and OAuth start/callback/complete
routes without providers (error paths), the super-admin signup invite and
waitlist routes, `/me/terms`, and bring-your-own storage servers (register
with probe failures naming the check, write-only secrets, edit/test/delete,
org default for new projects, explicit `storage_server_id`, a project whose
chunks land in a second MinIO bucket and are not billed). An extra invite-only
`nipad-ee` with a published terms version covers the waitlist end to end
(uninvited sign-up joins it, notify emails and allowlists, the address then
signs up) plus direct sign-up of invited addresses and terms acceptance. On
free the same specs assert every one of these routes stays absent (404).

Purge of deleted projects is not driven by the suite: nipad-ee purges hourly
(first run a minute after start) projects deleted more than
`STORAGE_PURGE_RETENTION_DAYS` (whole days) ago, so a black-box run cannot
age a deletion past the cutoff. The suite asserts the restore window it
derives from the retention (`purge_after`); the purge itself is covered by
`ee/e2e/storage_reclaim_test.go`.

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
NIPA_TEST_RATELIMIT=1 tests/run.sh ee     # the same profile against nipad-ee (redis store)
NIPA_TEST_GINKGO_ARGS="--ginkgo.v" tests/run.sh free --filter "nipa ignore"  # extra Ginkgo flags
NIPA_TEST_PURGE_RETENTION_DAYS=3 tests/run.sh ee  # nipad-ee restore window (default 1)
```

Rate-limit profile (both editions): `NIPA_TEST_RATELIMIT=1` also starts a
second server of the edition under test with low per-scope limits
(`auth_ip`, `api_user`, `chunk_ip`, `global_ip`; trusted proxies
`127.0.0.1/32`) on `NIPA_TEST_RATELIMIT_PORT` (default 6749, log in
`tests/.tmp/logs/<edition>-ratelimit.log`), exports its address as
`NIPA_TEST_RATELIMIT_HOST` and focuses the run on `"rate limiting"` unless
`--filter` is given. The per-identity `auth_id` specs run against the regular
server on every run; the low-limit specs skip without
`NIPA_TEST_RATELIMIT_HOST`.

Two-factor-off server (both editions): every run also starts a server of the
edition under test with `MFA_ENABLED: false` on `NIPA_TEST_MFAOFF_PORT`
(default 6751, log in `tests/.tmp/logs/<edition>-mfaoff.log`) and exports it
as `NIPA_TEST_MFAOFF_HOST`. The regular servers run with MFA on; the MFA-off
spec skips without the host.

Invite-only server (ee with `NIPA_TEST_SAAS`): a `nipad-ee` with
`REGISTRATION_ENABLED: false` and `SAAS_TERMS_VERSION` on
`NIPA_TEST_INVITEONLY_PORT` (default 6753, log in
`tests/.tmp/logs/ee-inviteonly.log`), exported as `NIPA_TEST_INVITEONLY_HOST`
with `NIPA_TEST_INVITEONLY_TERMS_VERSION`. The main ee server keeps open
registration and no terms version so the onboarding spec is unaffected.

The extra `nipad-ee` instances (rate-limit, MFA-off, invite-only) share the
postgres server, MinIO and redis of the ee run, but each gets its own
database (`nipa_e2e_<profile>`, recreated every run by
`tests/cmd/pgdatabase`, which also works against an external
`NIPA_TEST_POSTGRES_DSN` given CREATE DATABASE rights), its own
`CHUNK_S3_PREFIX` and its own `RATE_LIMIT_REDIS_PREFIX`. Sharing the main
database would make each instance a cluster member whose background jobs
(email and webhook dispatch, purge, chunk GC, maintenance polling) compete
with the main server and its email receiver; separate databases keep each
instance as isolated as the free profiles' separate SQLite files.
`SNOWFLAKE_NODE_ID: 0` stays static (no node lease), which is safe because no
two instances write the same database; the node lease with two instances on
one database is covered by `ee/e2e`. Their emails go to the main server's
receiver so the invite-only specs can read them.

Bring-your-own storage (ee with `NIPA_TEST_SAAS` and the storage ledger):
`run.sh` sets `BYO_STORAGE_ENCRYPTION_KEY` and `BYO_STORAGE_ALLOW_PRIVATE`
(the loopback MinIO is a private address), creates a second bucket
(`NIPA_TEST_BYO_S3_BUCKET`, default `nipa-byo`) and exports `NIPA_TEST_BYO`
plus the bucket coordinates (`NIPA_TEST_BYO_S3_*`). The specs read the bucket
back with the MinIO client to prove the chunks landed there.

Project restore window: `run.sh` sets `STORAGE_PURGE_RETENTION_DAYS`
(`NIPA_TEST_PURGE_RETENTION_DAYS`, default 1) on nipad-ee and exports it to
the suite, which asserts `purge_after = deleted_at + retention`; on free the
variable is empty and `purge_after` must be absent.

Makefile shortcuts: `make test-client`, `make test-client-ee`,
`make test-client-both`.

## How it works

`tests/run.sh`:

1. builds `bin/nipa`, `bin/nipad` and/or `bin/nipad-ee`;
2. for `ee`, starts `tests/docker-compose.ee.yml` (postgres 17 + redis +
   minio), waits for readiness and creates the S3 buckets (chunks and BYO)
   via `tests/cmd/s3bucket`;
3. writes a per-edition `config.yaml` into `tests/.tmp/server-<edition>/` and
   starts the server there (the server requires `config.yaml` in its working
   directory and applies migrations at startup), then the extra profile
   servers in `tests/.tmp/server-<edition>-<profile>/` (for ee each on a
   database made by `tests/cmd/pgdatabase`);
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
  cmd/s3bucket/           creates the S3 buckets before nipad-ee starts
  cmd/pgdatabase/         recreates the database of an extra nipad-ee instance
  suite/
    suite_test.go         RunSpecs, BeforeSuite (login + org), project factory
    support_*.go          env, CLI exec, REST, auth/identities, JSON DTOs,
                          workspaces, seeds, external diff, daemon, MCP,
                          SSH keys, hook/email receivers, pipe/PTY runners
                          (support_pty_test.go: runNipaPiped, runNipaPTY,
                          colorEnv)
    clone_test.go         nipa clone
    binary_cache_test.go  object cache: text vs. raw binary content, lock pins
                          keeping the old version for offline diff/restore,
                          ext-diff pruning, unreachable server
    blame_test.go         nipa blame (unpushed lines, binary/missing refusals)
    gc_test.go            nipa gc (replaced versions and orphans)
    pristine_test.go      restore and diff without cached objects
    import_git_test.go    nipa import git (history/branches/tags, --update
                          mirror, admin gate, quota pre-check, file:// + LFS)
    branch_test.go        nipa branch
    push_test.go          nipa push (+ add/remove/status glue)
    restore_test.go       nipa restore (discard modifications/deletions)
    restore_direct_test.go nipa restore of raw binaries takes the direct path
                          (no chunk reaches .nipa/objects: fan-out folders a
                          prune leaves behind are the probe) vs. text control
    update_test.go        nipa update (incl. refusing a symlinked folder)
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
                          and nipa ignore import
    sparse_test.go        nipa sparse-checkout
    tag_test.go           nipa tag
    lock_test.go          nipa lock/unlock (branch scope, released on delete)
    mr_test.go            nipa mr (reviews, threads, assignees, checks)
    mr_create_participants_test.go  MR creation with reviewers/assignees over
                          REST, the daemon proxy and the MCP tool
    acl_test.go           nipa acl + permission defaults
    org_owner_test.go     org owners' implicit project access vs. members
    group_test.go         nipa group
    meta_test.go          JSON output modes and error envelopes
    onboarding_test.go    SaaS registration + email confirmation (ee only)
    saas_admin_test.go    signup invites, waitlist, /me/terms, OAuth error
                          paths (ee; 404 on free) and the invite-only server
    byo_storage_test.go   bring-your-own storage servers (ee; 404 on free)
    project_lifecycle_test.go project delete, deleted list, restore, storage
                          release/charge-back (ee ledger)
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
    assets_test.go        nipa provenance (init/show/set/report) and nipa
                          assets check (.nipaassets.yml, dry run, MCP)
    generate_test.go      nipa push --generate against a fake LLM endpoint
    webhook_test.go       webhook deliveries (events incl. mr.ready_for_review,
                          filters, signature)
    email_test.go         email notifications (lifecycle, thread headers,
                          retries/redelivery)
    audit_test.go         admin audit log REST API
    issue_tracker_test.go nipa branch --issue, issue keys in log and mr view
                          against the in-process fake Jira
    admin_rest_test.go    admin REST (auth refresh/logout, users, orgs,
                          projects, branches, browser, signed chunk URLs)
    storage_quota_test.go storage quota (ee) / absent routes incl. billing
    service_token_test.go project service tokens (scopes, checks, webhooks,
                          audit tagging, expiry, creator demotion/deletion)
    maintenance_test.go   maintenance windows across CLI, nipa serve and nipa
                          mcp, frozen server, announcement emails
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
- Issue tracker specs start the `jiratest` fake Jira in-process and point the
  suite org's connection at it over REST; the generated config sets
  `INTEGRATIONS_ENCRYPTION_KEY` and `ISSUE_TRACKER_EGRESS_ALLOWLIST: 127.0.0.1`
  (a server without the key makes the specs skip).
- `admin_rest_test.go` signs expired chunk URLs itself with `chunkSigningKey`,
  a copy of `CHUNK_URL_SIGNING_KEY` from `run.sh`; keep the two in sync.
- Known product bugs are kept as pending specs (`PIt`) naming the cause, so the
  run stays green and the spec flips to `It` with the fix.
- Enterprise-only routes follow one pattern: on free assert the 404 and
  return; on ee exercise them, skipping only when `run.sh` did not enable the
  feature (`saasEnabled`, `storageLedgerEnabled`, `byoStorage`,
  `inviteOnlyServer`).
