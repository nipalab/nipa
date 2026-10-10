# AGENTS.md

Project context for opencode. Read this before working in this repo.

## What this project is

**Nipa** is a centralized version control system (Go/gRPC) targeting
binary-heavy projects (game dev, 3D assets, media) and large monorepos. It
blends Git (lightweight branches, release tags, merge requests), Perforce
(binary streaming), and SVN (path-based access control). Client-server monorepo: `nipa` (client,
CLI at `cmd/nipa`) plus two server binaries: the free `nipad` (`cmd/nipad`;
SQLite + local chunk storage) and the enterprise `nipad-ee` (`ee/cmd/nipad`;
PostgreSQL + S3-only chunk storage, see `docs/POSTGRES.md`). Both multiplex
gRPC (h2c), the REST API and the embedded SPA on one port through
`internal/serverapp` (`isAPIPath` routes `/auth`, `/docs`, `/api`; everything
else falls through to the web UI handler); the mains only compose repositories.
Dual-licensed (Apache 2.0; everything under `ee/` is enterprise).

## Architecture

- **Server** (`internal/`): `domain` (entities/errors), `usecase` (business
  logic, gomock-tested, depends only on repository interfaces; `browser.go` /
  `browser_history.go` power the web UI tree/commit browsing,
  `merge_request.go` the MR lifecycle, `merge_request_review.go` reviews and
  `tag.go` release tags), `repository/sqlite` (free implementation over
  sqlc-generated `internal/repository/sqlc/sqlite`), `serverapp` (shared
  registry + HTTP/gRPC/SPA lifecycle used by both mains), `grpc` (proto + `pb`
  generated + `server` handlers), `http` (REST API).
- **Enterprise** (`ee/`): `db` (postgres opener/migrator + consolidated
  migrations), `repository/postgres` (the same 13 repositories over
  `ee/repository/postgres/sqlc`, queries in `ee/repository/postgres/queries`),
  `storage/s3`, `cmd/nipad`/`cmd/migrate`, `e2e` (testcontainers smoke). All
  postgres and S3 code lives here; details in `docs/POSTGRES.md`.
- **Web UI** (`web/`): Vite/React/TypeScript SPA in `web/`, built with Primer
  React (`@primer/react` + `@primer/primitives` + `@primer/octicons-react`).
  UI code lives in `web/src/` (components use v38 API: `Stack`/`Banner`; the
  old `Box`/`Flex`/`sx` API is gone). Pin `react-is` to the same major as
  React (18) — react-is 19 checks `Symbol.for("react.transitional.element")`
  and silently breaks every Primer visual (`Button` `leadingVisual` etc.) with
  React 18 → black screen. `Banner` REQUIRES a `title` prop (or
  `<Banner.Title>`), children-only usage throws and unmounts the app. Theme is
  dark-only for now: `index.html` sets `data-color-mode="dark"` +
  `data-dark-theme="dark"` and a `html { background-color:#0d1117;
  color-scheme:dark }` inline style (without it the shell is transparent, so the
  bg comes from the browser canvas and text looks dark-on-dark), and
  `main.tsx` passes `colorMode="dark"`; only `dark.css` is imported. Built
  with `make web` into
  `web/server/dist`, embedded into the `nipad` binary via
  `//go:embed all:dist` in `web/server/webui.go` (package `webui`) and served
  for non-API paths in `internal/serverapp` (`isAPIPath`).
- **Client** (`internal/client/`): `cli` (cobra commands: clone, branch
  (`-a` list, `-c` create+switch, `-d` delete), tag (list, `-c` create, `-d`
  delete), add, remove, status, push, update, switch (branch or `--tag`),
  merge, revert, log, diff, acl, group, sparse-checkout, mr, lock, unlock, serve), `usecase`
  (clone/login, push, update/switch (incl. sparse and detached-tag checkout),
  merge, revert, diff, log, tag management,
  permission, merge-request orchestration, file locks over small interfaces; `threeway.go`
  holds the shared materialize/stage/delete core), `grpc` (gRPC transport,
  converts pb→server domain types; `Client.ServiceClient()` exposes the typed
  pb client for the daemon proxy), `merge` (three-way tree decisions + diff3
  text merge), `securestorage` (keyring-backed token store), `config`
  (`~/.config/nipa/config.json`: `diffExternal`, `uploadWorkers`), `difftool`
  (external diff runner), `ignore` (root `.nipaignore` + local `.nipa/ignore`
  pattern matcher for untracked working files), `domain` (client
  config/url/errors/commit/pending state), `localrepo` (`.nipa/` local metadata
  + SQLite),
  `daemon` (the loopback gRPC service behind `nipa serve`, see the flow below).
  `localrepo.FindRepoRoot`
  searches up to 32 parent directories for a `.nipa/config` so every in-repo
  command works from subdirectories.
- **Diff engine** (`internal/diff/`): pure package shared by client and server
  candidates — Myers line diff (`Lines`/`LinesWith` with an equality hook for
  whitespace ignores), tree-state comparison
  (`Entry`/`Change`/`Compare`/`FromTree`/`LoadContent`), rename detection
  (`DetectRenames`), content assembly (`AttachContents`), a server-facing
  tree-vs-tree entry point (`TreeDiff`) and unified/stat/name rendering.
  `internal/client/merge` delegates its line diff to `diff.Lines`. Keep this
  package free of client imports: the server merge-request diff reuses it.

Flow for `nipa clone <url> <target>`: `cli/clone.go` parses the URL →
`usecase.Repo.Clone` → ensure empty target → login → resolve default branch (or
`GetBranchByName` when a branch is given) → fetch tree manifest (gRPC
`GetTreeManifest`, recursive, with the URL path) → `localRepo.Init(target)` →
`SaveConfig` (`.nipa/config` json: url + branch + sparse prefixes) →
`syncWorkingCopy` (shared with update/switch: download missing chunks through
the commit-scoped `ChunkScope`, materialize files, record `stat_cache`
fingerprints) → `SaveTree` → `SaveCommit` (branch head commit id). Clone,
update and switch record the branch head commit id locally so `HEAD`/`@` (and
branch creation via `nipa branch -c`, which forks from the pinned commit)
resolve offline.

Flow for `nipa sparse-checkout <list|set|add|remove|disable>` (and `nipa clone
--sparse a,b`): `Update.SetSparse` normalizes the prefixes
(`domain.NormalizePathPrefix`, dedup, empty = full checkout) into
`.nipa/config`'s `sparse` field, then runs a regular update. The manifest
request and the chunk download scope carry the prefixes, so only the sparse
set is materialized and saved; files outside the set are removed from disk and
swept from the local DB but stay tracked on the server. Push from a sparse (or
permission-filtered) clone sends the pinned head commit as
`base_commit_id` (preferred over `base_tree_hash`) so the server applies the
delta to the full base tree. Merge and revert still require a full checkout;
`Push.Run` refuses to run mid multi-target revert sequence.

Flow for ignore rules (`.nipaignore` at the repo root, versioned; `.nipa/ignore`
per clone, never tracked): `internal/client/ignore.New` compiles both files
(local rules last, so they win) with a pragmatic gitignore subset (`#` comments,
`!` negation, `*`/`?`, `**`, leading/mid `/` root anchoring, trailing `/` =
directory-only; last match wins). `WorkingCopy.Status` and `Diff.scanWorking`
walk the working tree through `walkWorkingFiles`, which drops ignored paths
unless they are tracked or staged (tracking always wins over ignores) and prunes
ignored directories that contain no protected paths. `nipa add` errors on an
explicitly ignored target with a `use -f` hint (`AddOptions{Force}` / `nipa add
-f` stages it anyway); directory and whole-repo adds skip ignored files
silently. The matcher is client-side only: a hostile client can still push such
paths, and a tracked file that later matches a rule stays tracked until
explicitly removed. Sparse manifests always include the root `.nipaignore`
(`loadTreeManifest`'s `keepRootIgnoreFile`, still behind the read filter), so
partial checkouts apply the team rules too.

Flow for release tags (`nipa tag`, `nipa tag -c <name> [-m msg] [--branch <b> |
--commit <id|hash>]`, `nipa tag -d <name>`, `nipa switch --tag <name>`): a tag
is an immutable project-scoped name pointing at one commit, with an optional
annotation message (`tags` table, hard `UNIQUE(project_id, key)`; deleting frees
the name). Server `usecase.Tag` resolves the target
`commit_id > commit_hash > branch head > default branch head`, read-gates
list/get, write-gates create/delete (409 on a live duplicate) and emits
`tag.created` / `tag.deleted` webhooks. `nipa tag` lists (keyset cursor followed
across pages); `-c` defaults to the locally pinned HEAD commit (offline-safe)
and `--commit` accepts a base36 id or hex hash. `nipa switch --tag` is a
Perforce-style detached checkout: resolve the tag, fetch the tagged commit's
manifest with `GetCommitTreeManifest` (`GetTreeManifest` + `commit_id`, sparse
and read-filtered, project-scoped), sync the working copy, pin the tag commit
and record `{"head":{"kind":"tag","name":...}}` in `.nipa/config` — the branch
identity stays configured. While detached, `nipa update` re-syncs the pinned
commit (so sparse-checkout edits keep the checkout), `nipa status` prints
`HEAD detached at tag "x"`, `nipa log` walks from the pinned commit, `nipa diff`
accepts tag names as revisions, and push/merge/revert refuse with a
`nipa switch <branch>` hint; switching to any branch clears the marker (and
`branch -c` forks from the pinned commit and re-attaches). The `/tags` REST
routes (`internal/http/api/tag.go`, handlers/DTOs in
`internal/http/{handler,model}/tag.go`) mirror list/create/get/delete, and the
web API client (`web/src/api/endpoints.ts`) already exposes
`listTags`/`createTag`/`deleteTag` (no SPA page yet).

Flow for email notifications (design in `docs/EMAIL.md`): `internal/mail` is
the sender layer (`EMAIL_SENDER` = `smtp|sendgrid|http|log|off`), one message
per recipient is persisted in the `email_deliveries` outbox and
`mail.Dispatcher` claims due rows, retries with exponential backoff
(`EMAIL_MAX_ATTEMPTS`/`EMAIL_RETRY_BACKOFF_SECONDS`/`EMAIL_POLL_SECONDS`) and
sweeps expired rows; both mains build the sender + dispatcher from
`internal/config` (an `off` sender leaves the notification seam unwired).
`usecase.EmailNotifier` is attached through `HookEmitter.WithNotifier`: on the
MR events (created/ready/review-requested/review-submitted/comment/
synchronized/merged/closed/reopened and failed `mr.check_reported`, carried by
`EmitMergeRequestCheck`) it resolves the participants (author, requested
reviewers, assignees, commenters), skips the actor, deleted users, invalid
addresses and `users.notify_email = false`, and enqueues one rendered message
per recipient (deep link from `EMAIL_BASE_URL`; the first message to a
recipient uses a per-recipient thread `Message-ID`, later messages reference
it with `In-Reply-To`/`References` via the `thread_key` column). The
dispatcher is kicked after each enqueue. Project admins inspect and redeliver
through `usecase.EmailDelivery`: `GET .../emails/deliveries`
(`state`/`after`/`limit` keyset, `next_cursor`) and
`POST .../emails/deliveries/{id}/redeliver` (delivered/failed only), surfaced
by the SPA project settings "Email deliveries" tab. The harness runs `nipad`
with `EMAIL_SENDER` set to `sendgrid` or `http` (via
`NIPA_TEST_EMAIL_TRANSPORT`, one transport per edition in CI) pointed at an
in-suite receiver (and 1s retry/poll knobs) and covers opt-out, participant
emails, thread replies, synchronized/check/merge events, failed retries and
redelivery.

Flow for organization creation (`POST /api/v1/orgs`, "New organization" dialog
on the SPA home page): any authenticated user creates an org; `usecase.Org.Create`
trims the name, derives the slug from it when omitted, rejects invalid or
duplicate slugs (409) and calls `OrgRepository.CreateWithOwner`, which in one
transaction inserts the row with `created_by_user_id` and upserts the creator as
an `owner` member — so project creation (`Project.Create` requires org owner)
works immediately. The free version tracks no storage usage; cloud builds attach
quota to `created_by_user_id` through the `usecase.StorageLedger` seam.

Flow for binary file locks (`nipa lock <path> [--branch]`, `nipa unlock <path>`,
`nipa lock list`): binary changes are mandatory-lock gated. `FileLock.Acquire`
resolves the scope from the branch — the default branch is a project-global lock
(`file_locks.branch_id NULL`), any other branch a lock of that branch — and
rejects a path already covered (exact or directory prefix, `domain.PrefixCovers`)
by another holder; the same holder is idempotent. `Push.Push` classifies the
touched binaries against the head tree (`headBinaryPaths`): tracked binaries
being modified or removed must be covered by the pusher's lock (409 `binary file
%q requires a lock`), while new binary files only conflict with someone else's
covering lock (409 `%q is locked by <holder>`) — adding a brand-new asset needs
no lock unless a directory or pre-emptive lock guards it. `EnsureLocks` enforces
both lists in the target scope; after `ApplyPush`, `ReleaseLanded` drops the
pusher's own exact-path, non-request locks while directory locks span the
editing pass and stay. Merge requests acquire locks for their changed binary
paths at creation (re-checked and topped up at merge — `branchMerger.
BinaryChangesBetween` is the permission-filter-free three-dot enumeration —
accepting locks held by the request owner so an admin can merge on their
behalf), release them on merge/close, and re-acquire on reopen. Creation writes
the request row and its locks in one transaction through
`dbtx.Transactor.WithinTx` (repositories built with `dbtx.New` resolve the
transaction from the context), so a lock failure rolls the row back. Plain
`FastForward` enforces/releases
via `BinaryLockPlan` (required vs checked paths); `Branch.Delete` releases the
branch's scoped locks (soft delete ⇒ no FK cascade). The `/locks` REST routes,
`LockFile`/`UnlockFile`/`ListFileLocks` RPCs and the web Locks page expose
acquire/release/list.

Flow for merge request reviews: a review is one decision per reviewer per source
head round (`approved`/`changes_requested`/`commented`); a review for an older
head is stale and stops counting, and the latest per reviewer+head wins.
Submitting a review can carry inline comments that become anchored threads
(`file_path` empty = top-level conversation; otherwise `old_line`/`new_line`
must be a line the current diff shows). `POST .../reviews` on your own request
is rejected; a project writer can `POST .../reviews/{id}/dismiss` (kept in
history, `dismissed_reason=new_commits|manual`) while a reviewer can `DELETE`
their own; dismissing leaves comment threads intact (`review_id` FK is
`ON DELETE SET NULL`). A thread anchored to a line the new head no longer shows
is outdated (top-level threads never are). Review requests are answered by that
reviewer's next review and re-requesting keeps the original requester. Every
push to the source branch dismisses decisions for the old head and appends a
`pushed` timeline event: `Push` calls `usecase.Push.WithReviews`, which runs
after the apply succeeded and only logs bookkeeping failures. The timeline
events carry a `subject` actor (e.g. the requested reviewer). REST routes live
in `internal/http/api/merge_request_review.go`, the matching RPCs in
`internal/grpc/server/merge_request_review.go`; MR list/detail responses carry
the live `review` summary and the SPA renders a tabbed MR page (Conversation /
Commits / File changes, `?tab=`). The Commits tab is served by
`MergeRequest.Commits`, a first-parent `CommitLogUntil` walk (stop = live merge
base, authors joined) exposed at `GET .../merge-requests/{id}/commits`. The
diff is side-by-side by default with a Unified/Split toggle and floating inline
thread cards
(`web/src/components/repo/DiffView.tsx` + `ThreadCard.tsx`, reused by the
commit page).

Merge is review-gated: `MergeRequest.WithReview` attaches the review usecase
(the nil seam disables the gate, like file locks/hooks), and `check()` fills
`Mergeability.BlockedBy` — live `changes_requested` always blocks, and a target
branch's `required_approvals` (new `branches.required_approvals` column, set
through `SetBranchProtection` / `PUT .../branches/{name}/protection` with an
optional `required_approvals` that is kept when absent) must be met by live,
non-stale approvals. `Merge` refuses blocked requests with a 409. The same
review seam writes the lifecycle timeline events (`opened` inside the Create
transaction; `merged` with the landed commit id+hash, `closed`, `reopened`
log-only after the change landed), so the SPA's timeline renders them. The MR
usecase's `AppendEvent` path requires the review repository to be
transaction-aware (`dbtx.New`), which is why
`NewMergeRequestReviewRepository` wraps its DB handle. gRPC carries the same
surface: `GetMergeRequest` (detail + mergeability + live review summary),
`ReopenMergeRequest`, `CheckMergeRequest`,
`ListMergeRequestCommits` (reuses `CommitLogEntry`),
`GetMergeRequestDiff` (new `DiffFileDetail`/`DiffHunkDetail`/`DiffLineDetail`
messages mirroring the REST diff model), `MergeabilityDetail.blocked_by`,
`MergeRequestDetail.review`/`MergeRequestDetail.draft` (populated by
`ListMergeRequests` via `AttachSummaries`), and
`Branch.required_approvals`/`Branch.dismiss_stale_approvals`.

Draft requests: `merge_requests.is_draft` blocks `Merge` through
`check()`'s `blocked_by: "draft"`; `SetDraft` (author or project admin, open
requests only) toggles it, marking ready emits the `ready_for_review` timeline
event plus the `mr.ready_for_review` webhook, and the list takes a `draft`
filter (the CLI's `--status draft` maps to open+draft). A WIP/Draft title
prefix (`wip:`, `draft:`, `[wip]`, ...) auto-drafts on create and on rename,
and removing the prefix from a title-drafted request marks it ready. The SPA
marks drafts in the list, the page header, the edit dialog and the merge box;
the CLI adds `mr create --draft`, `mr update --draft=false` and
`mr ready <number>`.
`SetBranchProtection` carries `dismiss_stale_approvals` (default on; off
carries live decisions onto the new head through `CarryOverReviews` instead of
dismissing them), `require_status_checks` plus required check names, and named
`required_reviewers` whose live approval the review gate demands
(`blocked_by: "required_reviewers"`). `Branch.Delete` refuses protected
branches (409) until they are unprotected.

Status checks: `merge_request_checks` rows are keyed by (request, head commit,
name) and written by `MergeRequestCheck.Report` (`POST .../checks`, gRPC
`ReportMergeRequestCheck`, `nipa mr check`); a target branch with
`require_status_checks` blocks merging (`blocked_by: "status_checks"`) until
every required name succeeded for the current source head — a push starts a
clean slate. `List`/`nipa mr checks` return the head's checks; reporting emits
`mr.check_reported`.

Assignees: `merge_request_assignees` (base-set through
`SetAssignees` / `POST .../assignees` / gRPC `SetMergeRequestAssignees` /
`nipa mr assign`, project-write gated, ids validated) attach to list and get
payloads and drive the list's `assignee` filter; the SPA has a sidebar picker
and a list filter. The list also takes a free-text `search` (title/description
LIKE; CLI `--search`).

Merge strategies: `Merge` takes a `strategy` (`ff` default, `merge`, `squash`,
`rebase`) and `delete_source`. `Branch.MergeForMergeRequest`
(`internal/usecase/merge_strategies.go`) reuses the hoisted `internal/merge`
three-way engine (the former `internal/client/merge`): it loads the full
unfiltered trees (`mergeTree`), materializes taken files and diff3 text merges
(new content stored through the `Chunk` usecase via `WithChunkUploader`), and
writes the resulting tree and commit through `PushRepository.ApplyPushAll` —
one transaction for the whole rebase chain (`WithMergeCommitter`). Conflicts
return a 409 naming the paths and nothing lands; `behind_target` only blocks
`ff` and the review gate still applies to it (an objection or missing approval
blocks a diverged source too), while an ancestor source is `up_to_date`; rebase
walks the source first-parent history oldest-first, stopping at any commit the
base already contains, so a source that merged the target does not replay the
shared history (`rebaseCommitLimit`). Squash uses the request title and creator
as author;
rebase keeps each original commit's author and message. The target branch stays
protection-aware (admin required) and merged-source deletion is best-effort
(warn on failure). The SPA merge box picks the strategy plus a delete-source
checkbox; the CLI adds `mr merge --strategy <ff|merge|squash|rebase>
[--delete-source]`.

MR lists paginate by number: `GET .../merge-requests` takes
`status/author/source/target/draft/assignee/search/after/limit` and returns
`{merge_requests, next_cursor}` (the cursor is the last number of a full page;
gRPC uses `ListMergeRequestsRequest.after_number`/`ListMergeRequestsResponse.
next_cursor`). The handlers over-fetch one row, so `next_cursor` only appears
when a next page really exists; changing filters resets the cursor. Review
activity is webhook-visible through the same MR-shaped payload as the lifecycle
events: `mr.review_submitted`, `mr.review_dismissed`, `mr.review_requested`,
`mr.review_request_removed`, `mr.comment_created` (new comments only) and
`mr.check_reported`. The
CLI mirrors the surface under `nipa mr`: `create --draft`, `view`, `reopen`,
`ready`, `assign <n> <user-id>...`, `review --approve|--request-changes -m`,
`comments`, `comment [-m]
[--file --new-line/--old-line]`, `reply <number> <thread-id>`, `resolve
<number> <thread-id> [--unresolve]`, `timeline`, `requests`,
`request-review <number> <user-id>`, `unrequest-review`, `diff`, `checks <n>`,
`check <n> --name --state [--url]`,
`merge --strategy/--delete-source`, and `list
--author/--source/--target/--assignee/--search/--after/--status draft`.

Flow for `nipa revert <commit>` / `<from>..<to>`: resolve targets via gRPC
`GetCommit` / `WalkCommits` (range walks newest-first, exclusive stop; ranges are
capped at 16 targets and rejected before any three-way work) → for each
target three-way merge `(base=target tree, ours=current head, theirs=mainline
parent tree; root commit ⇒ empty parent; merge commits need --mainline 1|2)` via
`usecase/threeway.go` → stage and commit one revert per target through
`Push.pushStaged` (base = previous head/tree hash) → on conflict persist
localrepo `revert_state` (remaining targets, current tree hash + commit id,
conflicts) and return; `--continue` commits the resolved step and resumes,
`--skip` drops it, `--abort` restores the original tree/pin, `--no-commit`
applies to the working copy without committing (single commit, `-m` sets the
message). Reverts are forward-only; history is never rewritten.

Flow for `nipa diff [<rev1> [<rev2>]] [--] [<path>...]`: `cli/diff.go` splits
revisions from paths (paths only after `--`; `<a>..<b>`/`<a>...<b>` expand to two
revisions and `...` sets merge-base mode) → `usecase.Diff.Run`. With no revisions
it is fully offline: `Snapshot()` (paths, hashes, modes and per-file chunk
hashes) + `ListStaged()` → `walkWorkingFiles` (shared with `WorkingCopy.Status`).
Tracked files whose `stat_cache` fingerprint is fresh and whose file hash equals
the old side are reused without reading; everything else (staged additions,
stale fingerprints, real changes) is hashed with `chunkFile` → `diff.Compare`
over path-keyed `diff.Entry` maps; untracked-unstaged files are excluded, missing
tracked files are deletions, staged new files are additions. `--no-cache` reads
every working file. With revisions, each token
resolves to a commit ID first (branch name via gRPC `GetBranchByName`, then tag
name via `GetTagByName`; `HEAD`/`@`
via localrepo `LoadCommit`, falling back to the configured branch; otherwise
`snow.ParseBase36` → gRPC `GetCommit`), then `GetCommit(id).Tree` is flattened
with `diff.FromTree`; missing chunks are downloaded with `downloadMissing`
through the commit-scoped `ChunkScope` (binary content only with
`--ext-diff`). `<a>...<b>` calls `GetMergeBase` with
the two commit IDs and uses `MergeBaseTree` as the old side. Renames always run
through `diff.DetectRenames` (exact hash, line similarity for text, chunk
overlap otherwise); `-w`/`-b` live in `diff.Options` and `diff.FilterIgnored`
drops files whose differences are fully ignored. `--ext-diff` runs
`internal/client/difftool` with git's 7-argument contract (command from
`NIPA_EXTERNAL_DIFF`/`diffExternal`) and only when requested. `--staged`/`Paths`
narrow the result; rendering (patch, stat, name-only, name-status), colors and
the pager live in `cli/diff.go` + `internal/diff`.

Flow for `nipa serve` (client daemon, design in `docs/DAEMON.md`):
`cmd/nipa/serve.go` builds `daemon.NewServer` (proto
`internal/client/grpc/proto/daemon.proto`, generated
`internal/client/grpc/daemonpb`) and publishes `~/.config/nipa/daemon.json`
(`{pid, port, token, version}`, 0600, atomic write); every RPC must carry the
`x-nipa-daemon-token` metadata (constant-time compare), and `Shutdown`/SIGTERM
drain in-flight operations before removing the file. `WatchRepo(root)` resolves
any path inside a clone to its root (up to 32 parents), validates
`.nipa/config` and pins a per-root entry: cached `localrepo` handle +
`usecase.WorkingCopy`, watcher/reconciler, coordinator and a `RepoOps` graph
(`serveRepoOps` builds one usecase set and one gRPC client per root, since the
client transport binds one host at a time). `Status`/`Stage` are read-through
`WorkingCopy` calls under the shared coordinator slot (`Status` reports the
configured branch and the detached `head` marker); `Update`/`Switch`/
`Push`/`Merge`/`Revert` take the exclusive slot (FIFO, queued exclusive blocks
later shared waits) and stream `OpEvent`s (queued/started/progress/result/
failure; cancelling the stream always terminates it with a failure event).
`Switch` detaches at a tag when its request carries `tag`
(`UpdateRunner.SwitchTag`), otherwise switches branch.
`Diff` streams rendered patch/stat/name output (64 KiB chunks, file-by-file for
patches) under the shared slot. An fsnotify watcher debounces events into
dirty batches and a background reconciler refreshes `stat_cache` through
`WorkingCopy.RefreshStatEntries`; watcher failure or event loss is harmless
(read-through status still stat-detects). `Proxy*` RPCs forward `greet.*`
messages verbatim over `Client.ServiceClient()`, filling the clone's
org/project context from `.nipa/config`; `UnwatchRepo` waits for in-flight refs
before closing handles.

Server merge-request diffs reuse the same engine: `usecase.MergeRequest.Diff`
resolves the source and target heads, `GetMergeBase`, and calls
`Branch.TreeDiffBetween` (`internal/usecase/browser.go`), which loads both
manifests with `commitTreeManifest` (each side pruned by the caller's read
filter) and calls `diff.TreeDiff(baseTree, sourceTree, chunkStore.Get)` — a
three-dot diff like GitHub/GitLab, exposed at
`GET /api/v1/orgs/{org}/projects/{project}/merge-requests/{id}/diff`. The web
UI commit pages diff a commit against its first parent through the same
`commitTreeManifest` + `diff.TreeDiff` path (`?path=` filters to a file/dir).

## Commands

From `Makefile`:

- `make sqlc` — regenerate all sqlc packages: sqlite +
  `internal/repository/sqlc/localrepo` for the free client/server, and postgres
  in `ee/repository/postgres/sqlc` (inputs: `ee/db/migrations/postgres`,
  `ee/repository/postgres/queries`). Uses
  `go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest`.
- `make mock` — `go generate ./...` (regenerates gomock mocks from `//go:generate`).
- `make migrate-up` / `make migrate-down` — sqlite only, `go run ./cmd/migrate`
  (default `DSN=nipa.db`). `nipad` also applies pending sqlite migrations at
  startup. Postgres equivalents: `make migrate-up-postgres DSN=postgres://...`
  / `make migrate-down-postgres` via `go run ./ee/cmd/migrate`; `nipad-ee`
  applies pending postgres migrations at startup.
- `make migrate-create name=<name>` — new up/down migration pair in both
  dialect dirs: `db/migrations/sqlite` and `ee/db/migrations/postgres` (both
  embedded via `go:embed` and read by `sqlc.yaml`).
- `make build` — web + `bin/nipad` + `bin/nipa`; `make build-server-ee` —
  `bin/nipad-ee`; `make build-all` — web + both servers + client.
- `make test` — `go test ./...` (requires Docker for the testcontainers-backed
  postgres/minio suites); `make test-ee` — `go test ./ee/...`.
- `make test-client` / `test-client-ee` / `test-client-both` — the Ginkgo
  black-box client e2e suite (`tests/run.sh [free|ee|both]`): builds `bin/nipa`,
  starts a real `nipad`/`nipad-ee` (ee starts postgres + minio from
  `tests/docker-compose.ee.yml`) and drives the compiled CLI against it, so both
  editions are verified to behave the same client-side. Covers every command and
  flag (clone/push/update, branch, switch, merge, revert, log, diff,
  sparse-checkout, tag, lock, mr, acl, group) plus `nipa serve` (daemon gRPC),
  `nipa mcp` (stdio JSON-RPC) and webhook deliveries (events, filters,
  signatures, ping/redelivery), with multi-user identities for permission
  and lock scenarios. `NIPA_TOKEN_FILE` makes `securestorage` file-backed so the
  CLI runs without an OS keyring/TTY; the suite skips unless `NIPA_TEST_HOST` is
  set. Details in `tests/README.md`.
- `make web` / `web-dev` / `web-install` — build the SPA into
  `web/server/dist` / run the Vite dev server (proxies `/api`, `/docs` to
  `NIPA_SERVER_URL`) / `npm install`.
- `make proto` — regenerates pb from `internal/grpc/proto/server.proto` into
  `internal/grpc/pb` and from `internal/client/grpc/proto/daemon.proto` into
  `internal/client/grpc/daemonpb` (the daemon proto imports the server one;
  the target `M`-maps that import onto `internal/grpc/pb`).
- `make lint` — golangci-lint via Docker image (`.golangci.yml` enables
  errcheck/govet/ineffassign/staticcheck/unused/misspell/unconvert; formatters
  gofmt/goimports).

Direct: `go build ./...`, `go vet ./...`, `go test ./...`.

- `make bench-upload mb=512` (or `NIPA_BENCH_MB=512 go test ./internal/e2e
  -run TestEndToEnd_UploadThroughput -v`) measures upload throughput against an
  in-process server: it pushes a repetitive text file, a random binary file and
  a packed-asset `.png`, logs MiB/s, encoding, chunk and HTTP PUT counts, then
  re-clones and verifies the bytes. Skipped unless `NIPA_BENCH_MB` is set;
  `NIPA_BENCH_WORKERS` pins transfer concurrency (otherwise the adaptive tuner
  is exercised).
- The server opens SQLite through `db.OpenSQLite`, which appends
  `_pragma=busy_timeout(10000)`, `_pragma=journal_mode(WAL)` and
  `_txlock=immediate` to the DSN. Without WAL + a busy timeout, concurrent
  reads (every RPC resolves the project first) collide with confirm/Push
  writes and surface as `database is locked (5) (SQLITE_BUSY)`; without
  immediate transactions, a deferred transaction that reads before writing
  (e.g. `ApplyPushAll` resolving chunk rows) fails upgrading with
  `SQLITE_BUSY_SNAPSHOT` when another connection commits in between, and the
  busy handler is not invoked for that case.

## Conventions

- **Package layout**: consider packages under `internal/` as the border; use the
  existing import ordering (stdlib, third-party, then `github.com/nipalab/nipa/*`).
- **IDs**: server-side records use `github.com/nipalab/nipa/internal/snow`
  (`snow.ID`, `.Base36()` for wire, `.Int64()`/`snow.ParseBase36` for DB/proto).
  Client localrepo tables do NOT use snow IDs.
- **Errors**: `internal/domain.Error` with HTTP-style `Code` + `Message` +
  `InternalMessage` + `Cause`. Constructions: `NewErrorNotFound(msg)`,
  `NewErrorNoPermission()`, `NewErrorUser(msg)`, `NewErrorDatabase(internalMsg)`,
  `NewErrorInternalServer(...)`. Predicates: `domain.IsErrorNotFound(err)`,
  `domain.IsErrorNoPermission(err)`. Client mirrors this with
  `internal/client/domain.Error` (`NewUserError` 400, `NewTokenError` 401).
- **SQL**: schema + queries are sqlc inputs (`db/migrations/sqlite` +
  `db/queries/sqlite` for the free server, `ee/db/migrations/postgres` +
  `ee/repository/postgres/queries` for enterprise, and
  `internal/client/localrepo/schema|queries` for the client). After changing a
  `.sql` file, run `make sqlc` and check the generated Go compiles. Named params
  (`:name`) are converted to `?N` in `database/sql`.
- **sqlc gotchas**: a multi-statement `:exec` query has its SQL truncated by sqlc
  to one statement — use separate single-statement queries instead. Full-table
  `DELETE` without `WHERE` trips SonarQube (S1035-ish); keep them out. On
  sqlite, mixing unnamed `?` placeholders with `sqlc.narg(...)` makes sqlc emit
  a broken `?N` mixture that misbinds args at runtime — name every parameter
  with `sqlc.arg(...)` once a query uses `narg`. On
  postgres, quote keyword arg names (`sqlc.arg('limit')`), cast nullable filters
  and `LIMIT`/`OFFSET` params (`sqlc.narg('x')::boolean`,
  `sqlc.arg('limit')::bigint`), and keep integer columns `BIGINT` so generated
  types stay `int64` like sqlite.
- **Testing**: testify (`require`/`assert`) + gomock (`go.uber.org/mock`,
  `github.com/golang/mock` legacy mocks exist in `*_mock_test.go`). Server usecase
  tests use `NewMock<h1>...` gomock controllers; client usecases use hand-written
  stubs. SQLite-backed tests use `t.TempDir()` for real DBs. Enterprise postgres
  tests are testify suites over testcontainers (postgres + minio, one schema per
  test); override with `NIPA_TEST_POSTGRES_DSN`/`NIPA_TEST_S3_ENDPOINT`/
  `NIPA_TEST_S3_IMAGE` when using service containers.
- **Mocks**: live next to the code with the name suffix `_mock_test.go`; regenerate
  with `make mock`. In `internal/grpc/server`, `*_deps_mock_test.go` /
  `*_usecase_mock_test.go` are the mocks.
- **gofmt/golangci**: new/edited files must be gofmt-clean. One legacy file was
  left unformatted on purpose — do not reformat it unless the whole file is
  already clean: `internal/client/domain/url_test.go`.

## Client localrepo design (`internal/client/localrepo`)

- `.nipa/` inside the clone target: `config` (json `{url, branch, sparse?,
  head?}`; `head` is the detached marker `{"kind":"tag","name":"v1.0.0"}` and
  is absent while on a branch) written
  atomically via `atomicWrite`, `nipa.db` (SQLite, `modernc.org/sqlite`,
  `_pragma=foreign_keys(ON)` in the DSN — cascades silently don't fire otherwise),
  and `objects/` with Git-style loose chunk objects.
- Schema (in `schema/schema.sql`, embedded via `//go:embed`, also the sqlc source):
  - `tree_nodes(path TEXT PRIMARY KEY, parent_path, hash BLOB, mode, snapshot_id)`
  - `files(path TEXT PRIMARY KEY, tree_path FK->tree_nodes ON DELETE CASCADE, hash,
    size_bytes, mode, is_binary, encoding, chunks BLOB, snapshot_id)` — `chunks` is
    the concatenated 32-byte hashes of the file's chunks (metadata only, no
    content); `encoding` is `raw` or `zstd` and refers to the stored chunk bytes.
  - `meta(key PK, value)` — stores `tree_hash`, the pinned `commit_id` /
    `commit_hash`, and the pending `merge_state` / `revert_state` JSON.
  - `staged_files(path PK, staged_at)`.
  - `stat_cache(path PK, size_bytes, mtime_ns, mode, hash, cached_at)` — working
    file fingerprints whose `hash` was validated when the content was last hashed;
    `cached_at` (unix nano) must be newer than `mtime_ns` for the row to be trusted.
- Content lives in `.nipa/objects/<hash[:2]>/<hash[2:]>` (content-addressed loose
  objects, written temp+rename, never erased so `nipa update` can skip unchanged
  content). `StoreChunks`/`StoreChunk` write objects; `OpenChunk`/`LoadChunk` read
  them; `MissingChunks` stats them. Update/switch cache every downloaded chunk,
  merge/revert cache materialized content, and `Push` caches the chunks it uploads
  before sending them. The DB holds metadata only; file→chunk hashes are stored as
  the `files.chunks` blob (no chunk catalog table), which is what lets `nipa diff`
  rebuild the old side offline.
- `SaveTree` semantics (see `store.go`): one transaction. Token = current tree
  hash (`""` for empty). Upsert tree nodes/files by `path`
  (`ON CONFLICT(path) DO UPDATE`, including the encoded `chunks` blob); then
  **mark-and-sweep**:
  `DELETE FROM files/tree_nodes WHERE snapshot_id <> :token` removes only rows that
  were removed/renamed since the last save — never a full-table clear, so saving a
  10k-file repo with 5 changes touches ~5 rows, not the whole table. The same
  transaction drops `stat_cache` rows whose path left `files`. `meta_tree_hash`
  re-set each save. Empty repo: `SaveTree(nil)` sweeps all rows. Cached objects are
  never touched by `SaveTree`.
- `stat_cache` is what keeps `nipa status`/`nipa diff` cheap on big binaries: a
  tracked file whose `(size, mtime)` match a fresh fingerprint (`mtime < cached_at`)
  is trusted without reading, while misses are rehashed (bounded worker pool in
  `Status`) and written back in one `SaveStatEntries` transaction. Fingerprints are
  recorded only when content is known — files materialized by `syncWorkingCopy` /
  `applyThreeWay` and files scanned by `Push`; update's no-op skip path is
  deliberately left alone because it never verified the file, so a dirty file is
  hash-detected once by `status`. Paths are working-copy relative (no leading
  slash) and the sweep strips `files.path`'s leading slash. `status --no-cache` /
  `diff --no-cache` force a full rehash (and refresh the cache). Accepted blind
  spot, same as SVN/Git: an edit that preserves size and mtime is invisible until
  the mtime changes.
- There is no schema migration: the local DB is recreated from scratch (an old
  clone just needs to be deleted and re-cloned).
- The `localRepo` interface (used by `usecase.Repo.Clone`): `Init(target)` /
  `SaveConfig(domain.Config)` / `LoadConfig` / `SaveTree(*serverDomain.TreeNode)`,
  plus the content cache methods `MissingChunks` / `StoreChunks` / `OpenChunk` /
  `LoadChunk`, the working-copy fingerprints `SaveStatEntries` (`LoadStatCache` on the
  status/diff interfaces), staging (`StageAdd` / `StageRemove` / `ListStaged` /
  `ClearStaged`), the head pin (`SaveCommit` / `LoadCommit`) and the
  pending-operation state (`Save/Load/ClearMergeState`,
  `Save/Load/ClearRevertState`). `Push.Run` reads both pending states: merge state
  wins for the base tree/second parent (plus `TargetCommitID`), otherwise
  `RevertState.CurrentTreeHash`/`CurrentCommitID` is the base, and when neither is
  pending the pinned head commit (`LoadCommit`) is sent as `base_commit_id`; both
  states are cleared only after a successful push, and a multi-target revert
  sequence blocks `Push.Run` until `--continue`/`--abort`.
- `clientDomain.Snapshot` files carry the file-level hash (`files.hash`, derived
  from the server manifest by `client/grpc.toServerFile` as `chunker.FileHash` of the
  chunk hashes) plus the `Chunks` hash list decoded from `files.chunks`; content
  itself stays in the loose object store.

## Proto / gRPC

- Source is `internal/grpc/proto/server.proto`; service `NipaService` with
  `LoginWithUsernamePassword`, `LoginWithRefreshToken`; branch RPCs
  `GetListBranch`, `GetBranch`, `GetBranchByName`, `GetDefaultBranch`,
  `CreateBranch`, `RenameBranch`, `DeleteBranch`, `SetDefaultBranch`,
  `SetBranchProtection`; tag RPCs `ListTags`, `GetTagByName`, `CreateTag`,
  `DeleteTag`; history/tree RPCs `GetTreeManifest`, `GetCommitLog`,
  `GetCommit`, `WalkCommits`, `GetMergeBase`, `MergeFastForward`; merge-request
  RPCs `CreateMergeRequest`, `UpdateMergeRequest`, `ListMergeRequests`,
  `GetMergeRequest`, `MergeMergeRequest`, `CloseMergeRequest`,
  `ReopenMergeRequest`, `CheckMergeRequest`, `ListMergeRequestCommits`,
  `GetMergeRequestDiff`; review RPCs
  `SubmitMergeRequestReview`, `ListMergeRequestReviews`,
  `GetMergeRequestReviewState`, `WithdrawMergeRequestReview`,
  `DismissMergeRequestReview`, `ListMergeRequestThreads`,
  `AddMergeRequestComment`, `ReplyMergeRequestThread`,
  `UpdateMergeRequestComment`, `DeleteMergeRequestComment`,
  `ResolveMergeRequestThread`, `DeleteMergeRequestThread`,
  `ListMergeRequestReviewRequests`, `RequestMergeRequestReview`,
  `RemoveMergeRequestReviewRequest`, `ListMergeRequestTimeline`; transfer RPCs `Push`,
  `GetChunkUploadUrls`, `GetChunkDownloadUrls`, `ConfirmChunkUploads`; file-lock
  RPCs `LockFile`, `UnlockFile`, `ListFileLocks`; PBAC
  RPCs `GetMyPermissions`, `CreatePBACRule`, `ListPBACRules`, `DeletePBACRule`,
  `ListProjectPathPermissions`, `SetProjectPathPermission`,
  `DeleteProjectPathPermission`; group RPCs `CreateGroup`, `ListGroups`,
  `AddGroupMember`, `RemoveGroupMember`.
  `rpc` `GetTreeManifest` takes
  `{context{org,project}, branch, path, tree_hash?, recursive, paths,
  commit_id?}` (`paths` = directory prefixes to include, empty = whole tree;
  when `commit_id` is set the manifest is built from that commit's tree — the
  commit must belong to the project, and `tree_hash` is then ignored) and
  returns
  `{branch, root_tree}`. `TreeManifest` carries `tree_hash`, `path`, `sub_trees`,
  `files` (`path, mode, size_bytes, is_binary, chunk_hashes`). `GetBranch` takes
  `branch_id` (base36) and `GetBranchByName` takes `name`; both return the branch
  with its head `commit_id`. `CreateBranch` takes `from_branch` and/or
  `from_commit_id`/`from_commit_hash` (exact fork point, base36/hex).
  `GetCommitLog` takes `{context, branch, start_commit_id?, limit}`.
  `GetCommit` takes
  `{context, commit_id}` (base36; **ID only**, no hash alias) and returns the
  commit detail plus its recursive tree manifest; `WalkCommits` takes
  `{context, start_commit_id, stop_commit_id?, limit}` and returns
  `CommitWalkEntry`s newest-first, both-parents, stop-exclusive. `GetMergeBase`
  takes branch names and/or optional `target_commit_id`/`source_commit_id`
  (base36, take precedence) so a merge base can be resolved for commit pairs.
  All are project-scoped in the usecase because `CommitGet`/`CommitGetByHash`
  have no project filter, and commit IDs are server-local (snow) identifiers.
- **Chunk transfer is presigned HTTP, not gRPC streaming.** `GetChunkUploadUrls`
  (project write; refs carry `hash` + exact `size_bytes`) and
  `GetChunkDownloadUrls` (`commit_ids` + optional `paths` prefixes + `hashes`;
  the visible set is the union of the referenced commit trees narrowed by the
  prefixes — `Branch.VisibleChunks(projectID, commitIDs, paths)` — and requested
  hashes are filtered against it first) return one
  page of signed paths built by `internal/chunkurl` (`/api/chunks/{org}/{project}/{hash}
  ?op=put|get&size=&exp=&sig=`, HMAC-SHA256 with `CHUNK_URL_SIGNING_KEY`,
  constant-time verify, TTL `CHUNK_PRESIGN_TTL_SECONDS` default 3600s) — or
  absolute backend presigned URLs when the store implements
  `storage.DirectTransferStore` (S3). The
  client then PUTs/GETs bytes directly over HTTP through an adaptive transfer
  limiter (`internal/client/grpc/transfer.go`): concurrency starts at 16, is
  auto-tuned within [4, 64] from observed throughput, and can be pinned with
  `NIPA_UPLOAD_WORKERS` or `uploadWorkers` in `~/.config/nipa/config.json`
  (pinned disables tuning). `ConfirmChunkUploads` measures the stored content
  size server-side (`ChunkStore.Size`, never a client-supplied value) and writes
  the chunk metadata rows, so `Push` can rely on them; confirmations are
  serialized client-side under a mutex so concurrent upload windows never stack
  SQLite metadata writes. Chunks already stored come back with `already_stored` so
  clients skip the transfer. `Push` sends `base_commit_id` (base36; preferred over
  `base_tree_hash`, so sparse/permission-filtered clones push deltas against the
  full server tree) and `parent_2_commit_hash` for merge commits.
  `Push.pushStaged` scans into 16MB windows handed to
  two uploader goroutines (queue depth 2), so hashing overlaps with the network;
  window chunks are cached locally before upload and the scanner only blocks
  when the bounded queue is full. Chunk sizes are content-aware
  (`internal/chunker/profile.go`): text and in-place-edited formats (`.blend`,
  `.psd`, `.mb`, `.sqlite`, ...) keep the 64KB default, packed assets (textures,
  audio, video, models, archives) use 4MB average chunks, unknown binaries 1MB;
  the server accepts refs up to `chunker.MaxChunkSize()` (16MB). Text is
  zstd-compressed per file (`chunker.Encode`): files up to 8MB raw become one
  compressed chunk (≤4MB), larger text falls back to CDC chunks of the plaintext
  (256KB avg) compressed individually. Every file carries an `encoding` column
  (`internal/chunker.EncodingRaw`/`EncodingZstd`, proto `FileNode.encoding`,
  `files.encoding` on both server and localrepo); hashes always refer to the
  stored bytes, and every client hashing path (push, add/status, diff,
  merge/revert materialization, guarded remove) must go through
  `chunker.Encode`/`chunker.ConfigForFile`, passing the tracked encoding for
  stored files (`storedEncoding`), so file hashes stay consistent; decode at
  assembly points with `chunker.Decode` (client materialization,
  `diff.LoadContent`, merge's `loadFileContent`). The signed routes live in a `/api/chunks` webService (go-restful
  `os.Exit`s on duplicate root paths so it cannot share `/api/v1`) and are NOT
  behind the Bearer filter — the signature is the capability. HTTP PUT pins the
  exact size (Content-Length check + `MaxBytesReader` + BLAKE3 verify) and only
  writes content (`Chunk.StoreUploaded`, no DB writes, avoiding SQLite lock
  contention from concurrent PUTs); DB metadata is written at confirm time.
  `internal/client/grpc/chunks.go`'s `UploadChunks`/`DownloadChunks` take the
  `ChunkScope{Org, Project, CommitIDs, Paths}` that scopes download visibility;
  download callbacks are
  serialized under a mutex because transfers run concurrently. The server only
  returns relative paths: the client connects with the bare host as the gRPC
  target (also the token-storage key) and `Client.httpURL` prefixes `http://`
  when the target has no scheme.
- **Chunk content backends**: `internal/storage.ChunkStore` (Put/Get/Size/
  Exists/Close, content-addressed by BLAKE3, idempotent Put) is the single seam.
  `storage.NewLocalStore` (`CHUNK_STORAGE=local`, `CHUNK_STORAGE_DIR`) is the
  free `cmd/nipad`'s only backend — `CHUNK_STORAGE=s3` there fails with a hint
  to use `nipad-ee`. The enterprise `ee/cmd/nipad` is S3-only: empty or `s3`
  selects `ee/storage/s3` (`CHUNK_S3_*`), anything else is a startup error. The
  S3 store keys objects `<prefix>/<hash[:2]>/<hash[2:]>` and probes the bucket
  at startup. `internal/` must never import `ee/` — only `ee/cmd/*` does (the
  free `cmd/nipad` links no ee package). A store implementing
  `storage.DirectTransferStore` (the S3 store does) hands clients absolute
  presigned backend URLs: downloads via `PresignedGetObject`, uploads via a
  POST policy whose `content-length-range` pins the exact declared chunk size
  (S3 rejects oversized bodies with EntityTooLarge); `usecase.Chunk`
  type-switches on the capability, `PresignedChunkUrl` carries
  `method`/`form_data`, and the client POSTs multipart with the file part last
  (`Client.httpURL` passes absolute URLs through). Because direct upload bytes
  never transit the server, `usecase.Chunk` tracks every hash it hands a direct
  upload target to until the target expires: confirm-time verification always
  hash-checks those regardless of metadata (oversize objects are deleted
  without being read, mismatches deleted and reported missing), and download
  presigning re-verifies them before issuing a URL; untouched unrecorded chunks
  are verified too, while untouched recorded chunks only get a store existence
  + metadata check. The marks are in-memory per server process and drop after
  the presign TTL (a restart falls back to the metadata rule).
- **Storage accounting seam (free vs cloud).** `usecase.Chunk.WithStorageLedger`
  accepts an optional `StorageLedger` (`internal/usecase/storage_ledger.go`:
  `AttributedSizes`/`Headroom`/`Attribute`). The free version wires none (nil):
  presign/confirm skip all accounting and no quota applies. Cloud builds inject
  an `ee/` implementation at `ee/cmd/nipad` (same import pattern as `ee/storage/s3`);
  it links chunks to projects, keeps per-project/org/owner counters and enforces
  the owner's quota at both `GetChunkUploadUrls` (projected new bytes) and
  `ConfirmChunkUploads` (authoritative). Quota failures use
  `domain.NewErrorQuotaExceeded` (HTTP 402) and cross gRPC as
  `ResourceExhausted`, which the client maps back to a 402 domain error so
  `nipa push` reports it. The cloud implementation (ee tables, per-user quota,
  purchase, recount) is planned in `docs/CLOUD_STORAGE_QUOTA.md`.
- `Parsec`/`ParseNipaUrl` (`internal/client/domain/url.go`): `/org/project[/path]`.
  `path` is threaded through to the manifest request so a missing subpath returns a
  404, and cloning a repo with an empty (no-commit) branch returns an empty tree,
  not an error (branch root only).

## SonarQube / CI

- CI runs `go build ./...`, `go vet ./...`, `go test -race -coverprofile=...
  -coverpkg=./...` (cross-package coverage, so the REST/e2e integration tests
  count towards the handler/usecase packages they exercise).
- `sonar-project.properties` excludes generated code
  (`internal/repository/sqlc/**`, `ee/repository/postgres/sqlc/**`), swagger,
  and cmd mains from analysis; coverage expects `coverage.out`.
- Avoid introducing SonarQube issues: no DELETE without WHERE, keep coverage in new
  packages high (localrepo aims ~90%+).

## Web UI

- Routes/API map lives in `docs/WEB_UI.md`. SPA uses `react-router-dom` v6,
  memory-only access token + `HttpOnly` refresh cookie, and a capability mask
  from `/permissions/me` to hide admin controls (server remains authoritative).
  Pages cover repository/org lists, tree browser, blob viewer, commit history
  + diffs, branch management, merge requests (list/create; detail tabs for
  overview, commits and file changes; merge/reviews with decisions, inline
  floating threads, reviewer requests, activity timeline),
  file locks (list/lock/unlock), project/org settings (protection + ACL,
  members/groups), user administration
  and profile; the browse endpoints are served by `internal/usecase/browser.go`
  (+ `browser_history.go` for per-file/per-dir last-commit info) under
  `internal/http/api/`. Release tags have REST routes (`/tags`,
  `/tags/{name}`) and `endpoints.ts` helpers, but no SPA page yet.
- Keep `web/src/api/endpoints.ts` in sync with the REST handlers under
  `internal/http/`. `npm run lint` (tsc), `npm test` (vitest) and `make web`
  must stay green; the built SPA is embedded via `web/server/webui.go`.

## Things to remember

- Big (10k+ files) manifests are expected on day one — never plan an O(entire repo)
  DB write for an update path; default to incremental upsert + sweep.
- Keep the free/enterprise boundary: `internal/` never imports `ee/`, the free
  `cmd/nipad` links no ee package (sqlite + local chunks only), and postgres/S3
  are wired exclusively from `ee/cmd/*`; see `docs/POSTGRES.md`.
- Revert is forward-only: one commit per target, history is never rewritten, and
  pending `revert_state` is cleared only on success/abort. It reuses existing
  queries and added no SQL, sqlc, or schema changes.
- Do not commit secrets, config.yaml (gitignored, sample is `config.yaml.sample`).
- Added/edited tests should run under `go test ./...` with `go vet ./...` clean.
- If you change `opencode.json`, agents, or skills, tell the user to restart
  opencode after saving.
- Don't put too much comment on the code. Avoid to add comments as much as possible