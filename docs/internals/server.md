# Server feature internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

Push path validation (`checkPushContent`, shared by push and git import):
`domain.ValidateRepoPath` rejects empty/`.`/`..` segments, `\`, NUL and any
segment that resolves to the client metadata directory (`.nipa` in any case,
with trailing dots/spaces or HFS-ignorable code points, or `NIPA~1`;
`domain.IsMetadataSegment`), so a pushed `.nipa/config` cannot overwrite a
clone's metadata. Removed paths use `ValidateRemovedRepoPath`, which still
allows such a segment so a push can delete one stored before the check;
`gitimport` reports metadata paths as `InvalidPathsError` like other invalid
paths. A leading slash is refused too (`/a/f`): it used to be trimmed for the
check only, while `buildNewTree` keys directories by the path as written, so
the file was dropped from the tree and, next to `a/g`, the directory `a` was
built twice.

Tree invariant (`treeBuilder.build` in `usecase/push.go`): a directory never
holds a name both as a file and as a subdirectory. `buildNewTree` is the only
producer of a tree to store (push, the merge/squash/rebase strategies, git
import; fast-forward, tags, branches and revert-as-push store no tree of their
own, there is no REST write path and shelves are client-side), so the check
sits where a rebuilt directory's files (`byName`) meet its subdirectories
(rebuilt `Children` and untouched `KeepDirs`): one map lookup per subdirectory
of a directory that is rebuilt anyway, no query, nothing for untouched
directories. Any clash refuses the whole change before `ApplyPush` with a 400
(`newTreeClashError`, cause `*treeClashError`) that names each path and how it
clashes (file written over a folder that keeps files, files written below a
file that stays, both written at once, or already both in the base commit) and
says to stage the deletions too. It is refused, not repaired: dropping the
folder would delete files nobody asked to delete. The merge strategies reword
it as a 409 conflict (`mergeTreeClash`: "merge conflicts in <paths>: one branch
has the path as a file, the other as a folder"); `merge.ThreeWay` itself still
has no file/folder conflict, so the mergeability check does not predict it.
Sorted `Children` with the same name twice are an internal error. A commit
stored before the check (`files f JOIN tree_nodes t ON t.parent_tree_id =
f.tree_id AND t.name = f.name` finds them) blocks only pushes that rebuild the
damaged directory without deleting the file or the folder's content; the
deletion repairs it. Tests: `usecase/push_tree_clash_test.go`,
`e2e/push_tree_clash_test.go` (raw pushes, merge requests, a stored damaged
commit), `tests/suite/push_test.go`.

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
`listTags`/`createTag`/`deleteTag`/`getTag`; the SPA Tags page is `/:org/:project/tags`.

Flow for project service tokens and commit ancestry (payloads and scopes in
`docs/WEBHOOKS.md`): `project_service_tokens` rows (name, SHA-256 `token_hash`,
`hint`, comma-joined `scopes` `read`/`checks:write`/`webhooks:write`,
`created_by`, optional `expires_at`, `last_used_at`, `revoked_at`) are managed
by `usecase.ServiceToken` (project admin, audited as
`service_token.created`/`revoked`) over `GET/POST
/orgs/{org}/projects/{project}/service-tokens` and `DELETE .../{id}`; the
plaintext `nipa_st_...` is returned once. The REST `AuthFilter` sends
`Bearer nipa_st_...` to `ServiceToken.Authenticate` only for the
`serviceTokenRoutes` allowlist (method + selected route path → scope; every
other route is 403), which checks the token is active, the `{org}/{project}`
path names its project and the scope matches, the creator is active, verified
and still a project admin (else 403), touches `last_used_at` at most once a
minute and returns the creator's claims tagged with `Claims.ServiceTokenID`, so
the usual usecase permission checks still apply; the tag puts the token id in
audit entries (`ActorLabel` `service_token:<id>`), gives the token its own
`api_user` rate-limit bucket (`token:<id>`) and exempts it from the "author
cannot report required checks" rule. Creation counts the 50 active tokens
inside the insert transaction behind `LockProject` (postgres advisory lock,
sqlite immediate tx). `GET .../commits/{commit}/contains?ref=`
(`Branch.Contains`, `WithTags` for tag refs) resolves `ref` as tag, branch,
base36 commit id or hash and walks the ref's parents (no id-order pruning,
capped at 10000 commits, then 400) to answer `{contains}`; read permission
required. Service tokens are REST only.

Flow for the issue tracker integration (plan in `docs/plans/ISSUE_TRACKER.md`;
both editions): the pure
`internal/issuetracker` (`Tracker` + optional `Linker`/`Unlinker`
(`DELETE .../remotelink?globalId=`, a missing link is success)/`Commenter`/
`Transitioner`/`Searcher`, registry, `Extract`/`BranchKey`) with the `jira`
client (`/rest/api/2`, Cloud Basic or Data Center Bearer) and its `jiratest`
fake; outbound requests go through `internal/egress` (shared with webhooks).
Storage (sqlite `000026`, postgres `000015`): `issue_trackers`,
`project_issue_trackers` (`project_keys` comma-joined), `issue_links` and the
`issue_tracker_jobs` outbox; soft/hard deletes of projects and orgs drop
their rows (sqlite triggers, postgres triggers + cascades).
`usecase.IssueTracker` (`issue_tracker.go`, `Repositories.IssueTracker`,
dbtx-aware repos) holds one connection per org, enforced under `LockOrg`
(postgres advisory xact lock, sqlite immediate tx) with a re-read inside the
transaction (409 when it changed since the probe): `Get` (owners: all but the
token; members: kind, name, base URL), `Put` (owner; normalizes the URL, an
omitted token keeps the stored one unless the base URL changes, probes first:
`*IssueTrackerProbeFailure{Check: auth|reach|tls}` = REST 400 `{error,
check}`), `Delete` (409 while a project uses it), `Test` (`{ok, check, error,
site}`), `GetProject`/`PutProject`/`DeleteProject` (project admin; keys via
`issuetracker.NormalizeProjectKeys`, 1..20, `link_commits`
`off|default_branch|all`). Tokens are sealed with `sealbox` (label `nipa
issue tracker secret v1`, AAD `issue_tracker:<id>`) under
`INTEGRATIONS_ENCRYPTION_KEY`; without it every route answers 503. Trackers
are built by `GuardedIssueTrackerFactory(ISSUE_TRACKER_EGRESS_ALLOWLIST)`,
an `egress.Guard` whose list only adds private destinations on top of the
always-allowed public ones, as for webhooks (`WithFactory` in tests); both config keys are validated at startup
(`Config.ValidateIntegrations`). REST: `GET/PUT/DELETE /orgs/{org}/issue-tracker`,
`POST .../issue-tracker/test`, `GET/PUT/DELETE
/orgs/{org}/projects/{project}/issue-tracker`; audited as
`issue_tracker.created|updated|deleted` and
`project.issue_tracker.set|cleared` (never the token).
Links (`issue_links`, written only for active settings and configured keys):
`IssueSync` (`issue_sync.go`) is the `HookEmitter.WithIssueSync` sink (wired
in `BuildUsecases` only when the key is set; `syncIssues` runs before the
webhook fan-out, which returns early without matching hooks; failures only
log). `push` links the one commit a push lands (`PushEvent.AfterID`/`Message`;
`Imported` pushes from `Import.emit` are skipped) and the branch through
`BranchKey`; `branch.created` links the branch; `mr.created`/`mr.updated`
replace the request's link set (title + description + source branch, `closes`
after a closing keyword) inside one transaction, so an edit that drops a key
drops its link; `mr.merged|closed|reopened` change no links. Rows are upserts on the unique key
(relation updated). Reading (`issue_tracker_read.go`): `SubjectIssueKeys`
adds `issue_keys: [{key, url}]` to commit lists (also MR commits), branch
lists and merge request list/detail REST payloads (settings + connection +
one batched `ListIssueLinksBySubjects`; URLs from the tracker's `IssueURL`,
no Jira call; keys outside the configured keys hidden; silent nil when off).
Clients are cached per connection and rebuilt when `updated_at` changes.
`GET .../issues?keys=` (max 50, 400 above; unknown prefixes left out)
resolves through `issueCache` (keyed by tracker id + `updated_at` + key, 60 s
for found and not-found, 10 s for failures, singleflight per key, 5000
entries, 5 s request budget, fan-out 8; states `ok|not_found|unavailable`),
`GET .../issues/search?q=` (`supported: false` without `Searcher`,
`unavailable: true` on failure) and `GET .../issues/{key}/links` (newest
first, `after` cursor, max 100, missing subjects skipped; 400 for a key
outside the project's keys) need project read and answer 404 without active
settings.
gRPC mirrors the reads (`internal/grpc/server/issue.go`, read-class in
`maintenance_classes.go`): `ResolveIssues` (`keys`, max 50 =
InvalidArgument) and `SearchIssues` (`query`, `limit` default 20 max 50;
`supported`/`unavailable`) answer `IssueDetail` (`key`, `url`, `state`,
`title`, `type`, `status`, `status_category`, `assignee`), NotFound without
active settings and FailedPrecondition (503) when the integration is off or
the container has no `IssueTracker`. `CommitLogEntry`, `Branch` and
`MergeRequestDetail` gain `issue_keys` (`IssueKey{key, url}`), filled with
one batched `SubjectIssueKeys` per response on `GetCommitLog`,
`ListMergeRequestCommits`, `GetListBranch`, `ListMergeRequests` and
`GetMergeRequest` (the same payloads as REST; create/update/merge answers and
single-branch lookups leave them empty). The client shows them in `nipa log`
and `nipa mr view`, `nipa branch --issue` resolves through `ResolveIssues`,
the daemon proxies both RPCs and `nipa mcp` exposes them as `nipa_issues`.
Jobs (`issue_jobs.go`, `IssueSync.WithJobs(projects, orgs, users, commits,
baseURL)`; Jira is only called by the dispatcher): the sink queues
`issue_tracker_jobs` rows in the transaction that writes the links. Link ids
are `nipa:<org>/<project>:mr:<number>` and `...:commit:<base36>`, URLs the SPA
pages under `PUBLIC_BASE_URL` (else `EMAIL_BASE_URL`; without either no link
job is queued and startup warns), titles `#N <title>` / the first message
line, never file names or diffs. `push` queues a `link` per key of the commit
when `link_commits` is `all`, or `default_branch` and the branch is the
default one (imported pushes never); `mr.created`/`mr.updated` queue the
request's `link` per key when `link_merge_requests` (off only stops the Jira
links, the rows are still written) plus an `unlink` for each key an edit
dropped, and, with `opened_status` on an open request that is not a draft, a
`transition` (`skip_done`: an issue already in a done category is left alone)
for the keys the event added that are closing refs or the source branch key,
never plain mentions; `mr.ready_for_review` queues the same transition for
every eligible key of the request (a draft moves no issue until it is marked
ready); `mr.merged` queues the resolved `link`, a
`comment` (`comment_on_merge`: "<merger> merged merge request #N "title" in
org/project." + URL) for the linked keys plus the closing keys, and a
`transition` to `merged_status` only for closing refs of the title,
description and the request's commits (`CommitLogUntil` merged source head to
merge base, imported commits skipped); `mr.closed|reopened` re-send the links
(`resolved` = merged or closed). `dedupe_key` is `link:<key>:<link id>` (shared by `link` and `unlink`),
`comment:<key>:<link id>:merged` and
`transition:<key>:<link id>:<opened|merged>:<status>`; comments and
transitions are insert-ignore. A remote link has one row whose action is
`link` or `unlink` (sqlite `000027` / postgres `000016` widen the `action`
CHECK; sqlite rebuilds the table with its indexes and the three cleanup
triggers, no `sqlite_sequence` row since the id is not AUTOINCREMENT): an
enqueue with another action or payload re-arms it (attempt 0, pending,
keeping an in-flight lease), so a pending link of a removed key becomes the
unlink and is never sent, an in-flight link settles as stale and the unlink
runs after it, and adding the key back re-arms the link; edits and retries
never duplicate links or comments.
`usecase.IssueDispatcher` (`issue_dispatch.go`, built by `BuildUsecases` only
when the integration is available, exposed through `Registry.Dispatchers()`,
started by both mains and both e2e harnesses, `Pausable`) claims due rows
under a lease (`IssueJobClaimDue` bumps `attempt` and moves `next_retry_at`
to now + 10 min; postgres `FOR UPDATE SKIP LOCKED` in a CTE, sqlite immediate
transactions), only once the in-memory batch (10) is drained, so two server
colours never send the same job, and settles with `attempt` as the claim
token (`MarkIssueJobDelivered|Failed`, `ScheduleIssueJobRetry`; a job re-armed
in flight is released instead). Clients come from `IssueTracker.JobClient`
(the read path's client cache, rebuilt on `updated_at`; settings removed,
inactive or pointing at another connection, or an unopenable token fail the
job). Calls to one connection are spaced 200 ms apart (a wait over 2 s
postpones the job through `DeferIssueJob` without spending an attempt) and a
`*RateLimited` holds the connection for `Retry-After`. Rate limits,
`ErrUnavailable` and timeouts retry with 30 s x 3^(n-1) backoff (max 6 h, at
least Retry-After) up to 8 attempts; not found, refused credentials, no
transition, blocked egress, bad payloads and missing capabilities (a tracker
without `Unlinker` fails its unlink jobs) fail at once. The sink and job retries kick the dispatcher; frozen maintenance holds
claims, sweeps and calls; delivered and failed rows older than 30 days are
swept hourly. Metrics: `nipa_issue_tracker_requests_total{kind,op=link|unlink|comment|transition,result=ok|retry|failed}`
and the `nipa_issue_tracker_jobs{state}` gauge (recounted every minute).
REST (project admin): `GET .../issue-tracker/jobs` (`state`/`after`/`limit`
keyset, `next_cursor`; `title`/`url`/`status` from the payload, never comment
texts) and `POST .../issue-tracker/jobs/{id}/retry` (delivered or failed
only, resets attempt and error, kicks).

Flow for organization creation (`POST /api/v1/orgs`, "New organization" dialog
on the SPA home page and the `/onboarding` step): any authenticated user creates
an org; `usecase.Org.Create`
trims the name, derives the slug from it when omitted, rejects invalid or
duplicate slugs (409) and calls `OrgRepository.CreateWithOwner`, which in one
transaction inserts the row with `created_by_user_id` and upserts the creator as
an `owner` member — so project creation (`Project.Create` requires org owner)
works immediately, and `usecase.Permission` grants org owners `PermissionAll`
on every project in the org (`load` resolves the project's org through
`pbacRepository.ProjectOrgID` + `orgAuthorizer.IsOrgOwner`), so the creator can
read/write/admin the project without seeding rules; plain org members still
need PBAC rules. `Project.Create` takes an optional storage server id (REST
`storage_server_id`) and runs the nil-able `ProjectStorageAssigner`
(`UsecaseDeps.ProjectStorage`) inside its creation transaction; without one a
requested id is 400 `storage servers are not available`. The free version tracks no storage usage; cloud builds attach
quota to `created_by_user_id` through the `usecase.StorageLedger` seam. The SPA
forms (`OnboardingPage`, `CreateOrgDialog`, `NewProjectPage`) share
`web/src/slug.ts` for `slugify`/`SLUG_PATTERN`, require a valid non-empty slug,
auto-derive it until the user edits it, and surface a taken slug (409,
`ApiError.status`) at the slug field.

Flow for project deletion, restore and purge (plan and findings in
`docs/plans/STORAGE_RECLAIM.md`): `Project.Delete` soft-deletes
(`projects.deleted`/`deleted_at`) and, with a storage ledger, calls
`ReleaseProject` in the same audited write. `UNIQUE(org_id, slug)` has no
`deleted` filter, so a deleted project keeps its slug until it is purged and a
restore never clashes. `GET /api/v1/orgs/{org}/deleted-projects` and `POST
/api/v1/orgs/{org}/deleted-projects/{id}/restore` (org owner or global admin;
by id because the slug is not resolvable while deleted) go through
`Project.ListDeleted`/`Project.Restore`. Both honour `WithRestoreWindow`
(`UsecaseDeps.ProjectRestoreWindow`, set by `nipad-ee` to
`STORAGE_PURGE_RETENTION_DAYS`; zero in the free `nipad`, which never purges):
`ProjectRepository.Restore(id, deletedSince)` is one `UPDATE ... WHERE deleted
AND deleted_at >= deletedSince`, so a restore and the purge agree on the same
cutoff. Restore then calls `StorageLedger.RestoreProject` (402 when the
history no longer fits) and audits `project.restored`. `ee/projectpurge`
(`Job`, hourly, first run a minute after start, paused while frozen) purges up
to 50 projects per run deleted before `now - retention`, each step in its own
transaction that re-locks the project row (`LockPurgeableProject ... FOR
UPDATE`, still deleted and past the cutoff) so a restore wins cleanly and two
instances serialize: file locks, merge requests (cascading checks, reviews,
threads, events), tags and branches; detach commit parents in batches; delete
commits in batches of `STORAGE_PURGE_BATCH_SIZE` with their root trees (each
commit owns its root, whose delete cascades to subtrees, files and
`file_chunks`); `project_chunk_uploads` markers and the project's chunk rows
in batches (rows no `file_chunks` references any more; chunks belong to one
project); its objects with one prefix delete of `p/<project>/` in the
server's own store (`Purger.WithStore`, a `storage.ProjectDeleter`; skipped
for own-storage projects, whose bucket is never touched; a failed delete
stops before the project row and the next run retries); `ReleaseProject`
(for projects deleted before deletes released); finally the
project row (cascading the rest) with the `project.purged` audit entry. Tree
reads resolve a bare (kept) directory node to the oldest content node with the
same hash in any project, but every project also wrote its own content node
for every hash it keeps, so purging one project only moves the lookup to the
next copy (`TestPurgeExpired_RemovesTheProjectForGood`). Postgres `000017_project_purge` adds
the indexes the commit deletes need (every foreign key into `commits(id)`,
`commits(project_id)`).

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
no lock unless a directory or pre-emptive lock guards it. Acquiring needs write on the path itself, and on `nipad-ee` grants run in a
transaction holding a per-project advisory lock (`FileLock.WithSerializer`,
covering both `Acquire` and the merge-request lock path; `dbtx.WithinTx` joins
a caller's transaction) so overlapping server processes cannot grant
overlapping locks. Binary
classification is forced server-side for known asset extensions
(`markLockableBinaries`, the client flag can only add). `EnsureLocks` enforces
both lists in the target scope; after `ApplyPush`, `ReleaseLanded` drops the
pusher's own exact-path, non-request locks while directory locks span the
editing pass and stay. Client side, a lock this client takes is also pinned
locally (`usecase.LockPins`, used by `FileLock.Lock` and the daemon's
`ProxyLockFile` through `RepoOps.Pins`, after the server granted the lock):
the path goes into `pinned_paths` and the raw snapshot files it covers are
captured with `ensureSnapshotContent`, so offline compare and restore keep the
old version while the file is edited; a pin or capture failure is a
`FileLock.Warning` (`nipa lock` prints `warning: ...`, the daemon fills
`ProxyLockFileResponse.warning`) and never undoes the lock. Unlock unpins and
prunes (a local unpin failure is only logged: the server lock is already
gone); a successful push drops the exact-path pins among the pushed and
removed paths (`landedPins`, no DB write without one; directory pins stay,
like `ReleaseLanded`). Pins are a best-effort cache hint keyed by path only,
not by lock scope: the same path locked on two branches (or held by a merge
request) shares one pin, and a push on either branch drops it while the other
lock is still held. Locks taken elsewhere are not pinned either; in both cases
reads use the missing-content fallback (download when online). Merge requests acquire locks for their changed binary
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
Release requests (`file_lock_requests`: lock, requester, message, `created_at` = last
time the holder was notified; sqlite `000023`, postgres `000008`, unique per
(lock, requester); sqlite drops them through the `file_lock_requests_cleanup`
trigger because the server connection runs without foreign keys, postgres
through `ON DELETE CASCADE`): `FileLock.RequestRelease(projectID, lockID,
message)` needs project read and read on the lock's path (otherwise 404),
refuses the holder (400 `you hold this lock`) and messages over 500
characters, creates the row or updates the message, and only emits when the
row is new or the last notification is at least `LockRequestCooldown` (24 h)
old. `HookEmitter.EmitLockReleaseRequested` queues the `lock.release_requested`
webhook (`webhook.LockPayload`) and, through the optional
`lockRequestNotifier` on the email notifier (`NotifyLockRequest`, honouring
`notify_email`), emails the holder; failures only log. `FileLock.List` fills
`Requests` for the visible locks, so every reader sees them. REST `POST
.../locks/{id}/requests` (`api_user` rate limit like every route), gRPC
`RequestFileLockRelease`, `FileLockDetail.requests`, `nipa lock request <path>
[-m] [--branch]` (`FileLock.RequestRelease` picks the clone branch's lock over
the global one, `lock list --json` carries `requests`), daemon
`ProxyLockRequestRelease` and the `lock_requested` event (`WatchEvents` tracks
`id@requested_at` of the caller's own locks), the desktop "Ask to release" and
the SPA Locks page button.

Locks may expire: `file_locks.expires_at` (NULL = permanent) is set from an
optional TTL (`LockFileRequest.ttl_seconds`, REST `ttl_seconds`, `nipa lock
--ttl 2h`, client `LockOptions{Branch, TTL}`, usecase `LockTTL`/
`LockTTLSeconds`, 1m..7d else 400). `Get`/`ListProject` filter expired rows
with a `now` passed from Go (`FileLock.WithClock` seam), and `Acquire` and
`ensureMergeRequestLocks` purge them (`DeleteExpired`) inside the serialized
section first, since expired rows still occupy the unique indexes. A re-lock
with a TTL by the holder renews it (`SetExpiry`; permanent covering directory
locks and MR locks are left alone), without a TTL the expiry is unchanged; MR
locks never expire and conflict messages name the expiry. Purges are not
audited, and `WatchEvents` reports an expired lock as `lock_released`.

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
messages mirroring the REST diff model; clients read it through the
server-streaming `StreamMergeRequestDiff`, ~1 MiB `MergeRequestDiffPart`s,
a file whose patch and hunks exceed one part is sent without them and
`too_large`, shown as "diff too large to show" by `nipa mr diff`, MCP
`truncated` and the AI prompt; an older server answers `Unimplemented` with an
upgrade hint), `MergeabilityDetail.blocked_by`,
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
the CLI adds `mr create --draft [--reviewer <id>] [--assignee <id>]`, `mr update --draft=false` and
`mr ready <number>`.
`SetBranchProtection` carries `dismiss_stale_approvals` (default on; off
carries live decisions onto the new head through `CarryOverReviews` instead of
dismissing them), `require_status_checks` plus required check names, and named
`required_reviewers` whose live approval the review gate demands
(`blocked_by: "required_reviewers"`). `Branch.Delete` refuses protected
branches (409) until they are unprotected.
The protection lists live in child tables (`branches_required_reviewers`,
`branches_required_checks`) and are not part of a branch row: the repository's
single-branch getters (`GetBranchByName`, `GetByProjectIDAndID`,
`GetDefaultBranch`) never read them, so push/manifest/update branch resolution
pays nothing for them. `Branch.ListBranches` attaches them for the whole page
with `RequiredReviewersByBranches` / `RequiredChecksByBranches` (one statement
each per 500 branch ids, keyed by branch id, same per-branch order as the
single-branch `RequiredReviewers` / `RequiredChecks`), so a listing costs three
statements however many branches it returns; never load them per listed
branch. The single-branch loaders stay for `SetProtection` and the merge gates
(`reviewBlockedBy`, `MergeRequestCheck.BlockedBy`), which read one branch.

Status checks: `merge_request_checks` rows are keyed by (request, head commit,
name) and written by `MergeRequestCheck.Report` (`POST .../checks`, gRPC
`ReportMergeRequestCheck`, `nipa mr check [--commit <id>]`). A report must name
the source head it was started for (`head_commit_id`; the CLI defaults to the
commit the working copy is pinned to, never the server's current head); a moved head is a 409, and the request author cannot report
when the target requires status checks; a target branch with
`require_status_checks` blocks merging (`blocked_by: "status_checks"`) until
every required name succeeded for the current source head — a push starts a
clean slate. `List`/`nipa mr checks` return the head's checks; reporting emits
`mr.check_reported`.

Create takes `usecase.CreateMergeRequestInput`; optional `ReviewerIDs`/
`AssigneeIDs` (REST `reviewer_ids`/`assignee_ids`, gRPC fields 7/8, SPA/desktop
create forms, MCP `reviewers`/`assignees`) need project write, are deduped and
validated like `SetAssignees` (self review rejected, nil review seam = 400) and
are written in the create transaction after the `opened` event (review
requests + `review_requested` events, no separate webhook), so only
`mr.created` fires and they get the "opened" email.

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

