# Issue tracker integration

Status: PRs 1–5 implemented (see the table below and
`docs/internals/server.md`); PR 6 is on demand. First tracker: Jira (Cloud
and Data Center). Nipa does not become a task manager; it links its commits, branches
and merge requests to issues that live elsewhere.

## Goals

- Recognize issue keys (`GAME-123`) in commit messages, branch names and merge
  request titles/descriptions and show them as links with the issue's title
  and status.
- Show the Nipa work on the issue: a link to each merge request (and
  optionally each commit) on the Jira issue.
- Move the issue when work lands: `Fixes GAME-123` in a merged request
  transitions the issue to a configured status.
- Keep trackers behind one interface so Linear, Codecks, HacknPlan or GitHub
  Issues can be added without touching the usecases.

Non-goals for v1: creating or editing issues from Nipa, syncing Jira state
into Nipa in real time, the Jira "Development" panel, per-user Jira identities.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Edition | Both (`internal/`, sqlite + postgres) | Adoption feature, like webhooks. Nothing here needs postgres or S3. |
| Connection scope | Exactly one connection per org, enabled per project | A studio has one Jira site; projects differ in Jira project keys and rules. The service enforces the limit; the schema does not, so several per org can come later without a migration. |
| Transitions | Merged requests only | A closing keyword in a commit pushed straight to a branch only links. Fewest accidental transitions. |
| Jira auth (v1) | Cloud: email + API token (Basic). Data Center: personal access token (Bearer) | No app registration, works for self-hosted Nipa. OAuth 3LO later. |
| Jira API | `/rest/api/2` on both Cloud and Data Center | One code path; v2 comment bodies are plain text, v3 needs ADF. |
| Identity in Jira | The connection's account (a service account) | Comments name the Nipa user in the text. |
| Outbound writes | Outbox + dispatcher, like `webhook_deliveries` | Jira being down never fails a push or a merge. |
| Issue details | Fetched on demand, cached in memory (60 s) | No stale copy to maintain; links still work when Jira is down. |
| Key detection | Only the Jira project keys configured for the Nipa project | Avoids `UTF-8`/`BC-7` false positives and limits what readers can look up. |

## Package layout

```
internal/issuetracker/            pure: no usecase/repository imports
  tracker.go                      interfaces, Issue/Link/Transition, errors
  refs.go                         key extraction, closing keywords
  registry.go                     kind -> Factory
  jira/                           Jira client (Cloud + Data Center)
internal/egress/                  dial guard hoisted from internal/webhook/client.go
internal/usecase/issue_tracker.go         connections, project settings, read API
internal/usecase/issue_sync.go            event -> links + outbox jobs
internal/usecase/issue_dispatch.go        outbox dispatcher (serverapp.Dispatcher, Pausable)
internal/repository/sqlite|ee/repository/postgres   IssueTrackerRepository (dbtx-aware)
internal/http/{api,handler,model}/issue_tracker.go
```

## The abstraction

A small required core plus optional capabilities, the same pattern as
`storage.DirectTransferStore`: the usecase type-asserts and skips what a
tracker cannot do.

```go
package issuetracker

type Tracker interface {
    Kind() string
    Probe(ctx context.Context) (*Site, error)
    Issue(ctx context.Context, key string) (*Issue, error)
    IssueURL(key string) string
}

type Linker interface {
    // Link is an upsert keyed by Link.ID.
    Link(ctx context.Context, key string, link Link) error
}

type Commenter interface {
    Comment(ctx context.Context, key, text string) error
}

type Transitioner interface {
    // TransitionTo moves the issue to the named status. ErrNoTransition when
    // the workflow has no such step from the current status.
    TransitionTo(ctx context.Context, key, status string) error
}

type Searcher interface {
    Search(ctx context.Context, query string, limit int) ([]Issue, error)
}

type Issue struct {
    Key, Title, Type, URL string
    Status         string
    StatusCategory string // todo | in_progress | done
    Assignee       string
}

type Link struct {
    ID, URL, Title, Summary string
    Resolved bool
}

type Config struct {
    BaseURL, Account, Secret, AuthKind string
    Transport http.RoundTripper
}

type Factory func(Config) (Tracker, error)
func Register(kind string, f Factory)
func New(kind string, cfg Config) (Tracker, error)
```

Errors are typed so the dispatcher can decide: `ErrNotFound`,
`ErrUnauthorized`, `ErrNoTransition` (permanent), `*RateLimited{RetryAfter}`
and `ErrUnavailable` (retry).

`refs.go`:

- `Extract(text string, keys []string) []Ref` finds `\b(KEY1|KEY2)-\d+\b`
  for the configured project keys, deduplicated, capped at 20 per text.
- A ref is `closes` when preceded by `fix|fixes|fixed|close|closes|closed|
  resolve|resolves|resolved` (case-insensitive, optional `:`), otherwise
  `mentions`.
- `BranchKey(name, keys)` reads a leading key from a branch name
  (`GAME-123-new-rig`, `feature/GAME-123-new-rig`).

Fuzzed, like the other pure parsers.

## Jira implementation

| Operation | Call |
|---|---|
| `Probe` | `GET /rest/api/2/myself`, then `GET /rest/api/2/serverInfo` (`deploymentType`) |
| `Issue` | `GET /rest/api/2/issue/{key}?fields=summary,status,issuetype,assignee` |
| `Link` | `POST /rest/api/2/issue/{key}/remotelink` with `globalId` = `Link.ID` (Jira upserts on it) |
| `Comment` | `POST /rest/api/2/issue/{key}/comment` |
| `TransitionTo` | `GET .../transitions`, pick the one whose `to.name` matches, `POST .../transitions` |
| `Search` | `GET /rest/api/2/issue/picker?query=` |

Client rules: 15 s timeout, 1 MiB response cap, no redirects, the secret
redacted from errors, `429`/`Retry-After` mapped to `*RateLimited`, 5xx to
`ErrUnavailable`. Tested against an `httptest` fake Jira; an env-gated test
(`NIPA_JIRA_TEST_*`) runs against a real site.

To verify during implementation: the exact Cloud behaviour of
`issue/picker` and `remotelink` under API-token auth, and the minimum Data
Center version for personal access tokens.

## Storage

sqlite `000026_issue_tracker`, postgres `000015_issue_tracker`.

```
issue_trackers
  id, org_id, kind ('jira'), name, base_url, auth_kind ('basic'|'bearer'),
  account, secret_sealed, created_by, created_at, updated_at
  UNIQUE (org_id, name)

project_issue_trackers
  project_id PK, tracker_id, keys (comma-joined Jira project keys),
  link_merge_requests BOOL, link_commits ('off'|'default_branch'|'all'),
  comment_on_merge BOOL, merged_status TEXT ('' = no transition),
  opened_status TEXT, is_active BOOL, set_by, updated_at

issue_links
  id, project_id, tracker_id, issue_key,
  subject_kind ('commit'|'merge_request'|'branch'), subject_id,
  relation ('mentions'|'closes'), created_at
  UNIQUE (project_id, issue_key, subject_kind, subject_id)
  INDEX (project_id, subject_kind, subject_id)

issue_tracker_jobs
  id, tracker_id, project_id, issue_key, action ('link'|'comment'|'transition'),
  payload, dedupe_key UNIQUE, state ('pending'|'delivered'|'failed'),
  attempt, last_error, next_retry_at, delivered_at, created_at
```

The token is sealed with `sealbox` (label `nipa issue tracker secret v1`, AAD
`issue_tracker:<id>`) under a new `INTEGRATIONS_ENCRYPTION_KEY`. Without the
key the feature is unavailable (routes answer 503, like MFA). The secret is
write-only in every API.

## Flows

### Event to links (write path)

`HookEmitter` is already the fan-out point for webhooks and email. It gains a
third seam, `WithIssueSync(sink)`, shaped like `emailNotifier`
(`Wants(event)`, `Handle(ctx, IssueEvent)`); a nil sink is off. `emit` must no
longer return early when only the sink wants the event. Failures only log, as
for email.

| Event | Links written | Jobs queued |
|---|---|---|
| `push` | commit ↔ keys in the message; branch ↔ `BranchKey` | `link` per `link_commits` |
| `mr.created`, title/description edit | merge request ↔ keys in title, description and source branch name | `link` (upsert), `transition` to `opened_status` |
| `mr.merged` | none | `link` (resolved), `comment`, `transition` to `merged_status` for `closes` refs only |
| `mr.closed` / `mr.reopened` | none | `link` (title/status refresh) |

Rules:

- Link rows are written in the sink; Jira calls only ever happen in the
  dispatcher.
- Link ids are stable (`nipa:<org>/<project>:mr:<number>`,
  `...:commit:<base36>`), and `dedupe_key` = action + link id + state, so
  re-edits and retries never duplicate links or comments.
- Imported commits (`origin` set) are never linked, so a Git import does not
  flood Jira.
- Commits landed by a merge request are represented by the request's link.
- A plain mention never transitions an issue; only a closing keyword in a
  merge request's title, description or commits does, and only when the
  request merges. A direct push with `Fixes GAME-1` records a `closes` link
  and nothing more.

The dispatcher reuses `internal/dispatch.Runner`: claim due rows atomically,
exponential backoff, 8 attempts, a per-connection in-process rate limit,
`Pausable` so nothing is sent while the server is `frozen`. One client per
connection, cached and rebuilt when `updated_at` changes.

### Reading (web, CLI, desktop)

- Commit, merge request and branch payloads gain `issue_keys` (one indexed
  lookup in `issue_links`; batched for lists). Each key carries its URL, built
  from the connection's base URL without calling Jira.
- `GET .../issues?keys=A-1,A-2` (max 50) resolves title/status/type through
  the cache (60 s TTL, one fetch for concurrent misses, negative entries for
  unknown keys). Tracker down = `{key, url}` only; never an error page.
- `GET .../issues/{key}/links` lists the commits, branches and merge requests
  of an issue.
- `GET .../issues/search?q=` backs issue pickers when the tracker is a
  `Searcher`.

All three need project read and only answer keys with a configured prefix.

### Configuration

| Route | Who |
|---|---|
| `GET/PUT/DELETE /orgs/{org}/issue-tracker`, `POST /orgs/{org}/issue-tracker/test` | org owner (GET for org members: kind, name and base URL only) |
| `GET/PUT/DELETE /orgs/{org}/projects/{project}/issue-tracker` | project admin |
| `GET .../issue-tracker/jobs`, `POST .../jobs/{id}/retry` | project admin |

`PUT` creates or replaces the org's single connection (an omitted token
keeps the stored one); `DELETE` is a 409 while a project uses it. Create and
update probe first; a failed probe is a 400 with the failed check
(`auth`, `reach`, `tls`), the same shape as BYO storage. Audited as
`issue_tracker.created|updated|deleted` and
`project.issue_tracker.set|cleared`; metadata never holds the token.

## Security

- **Egress.** The base URL is user input, so every request goes through the
  dial guard already written for webhooks (public addresses only, checked on
  the dialed IP, no redirects). It moves to `internal/egress` and both use it.
  A Jira Data Center on a private network needs an entry in
  `ISSUE_TRACKER_EGRESS_ALLOWLIST`.
- **Visibility leak, Jira to Nipa.** Every Nipa project reader sees titles
  and statuses the service account can read. The per-project key list bounds
  this; the settings page says so.
- **Visibility leak, Nipa to Jira.** Only a title and a URL are sent, never
  file names or diffs. Private-project MR titles become visible to Jira users
  who can see the issue; this is stated next to the toggle.
- **Token scope.** The docs recommend a dedicated Jira account limited to the
  linked projects, with browse, comment, link and transition permissions.

## Surfaces by phase

| PR | Content |
|---|---|
| 1 | `internal/issuetracker` (interfaces, refs, registry), `jira` client with the fake, `internal/egress` extraction |
| 2 | Migrations, repositories, `usecase.IssueTracker` (connections, project settings, probe), REST, audit, config key |
| 3 | `issue_links`, the `HookEmitter` sink, `issue_keys` on DTOs, resolve/links routes, SPA: issue chips on commits and merge requests, org and project settings tabs |
| 4 | Outbox + dispatcher: remote links, merge comment, transitions; jobs tab; metrics (`nipa_issue_tracker_requests_total{kind,op,result}`, `nipa_issue_tracker_jobs{state}`) |
| 5 | gRPC (`ResolveIssues`, `SearchIssues`, `issue_keys` fields), `nipa branch -c --issue GAME-123`, `nipa log`/`mr view` keys, desktop chips and "Branch from issue" in the palette, MCP read tool |
| 6 | Later, each on demand: inbound Jira webhooks (cache invalidation, `issue.updated` events), Jira Development panel, OAuth 3LO, a second tracker (Linear) to prove the abstraction |

Each PR carries its own tests (80% gate): unit tests with the fake Jira and
gomock, `internal/e2e` for the push → link → job path, `tests/suite` once the
CLI surface exists, with the fake Jira started by the harness.

## Settled (2026-11-20)

- Ships in both editions.
- Only merged requests transition issues.
- One connection per org.
