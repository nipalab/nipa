# Issue tracker integration (Jira)

Nipa links its commits, branches and merge requests to issues in Jira (Cloud
or Data Center). Issue keys such as `GAME-123` show up as chips with the
issue's title and status in the web UI, the CLI and the desktop app, and Nipa
can add remote links, a merge comment and status transitions on the Jira
issue. Nipa never creates or edits issues, and Jira stays the source of truth.

The feature ships in both editions (`nipad` and `nipad-ee`). The design notes
are in [plans/ISSUE_TRACKER.md](plans/ISSUE_TRACKER.md) and the implementation
notes in [internals/server.md](internals/server.md).

## 1. Enable the feature on the server

| Setting | Purpose |
| --- | --- |
| `INTEGRATIONS_ENCRYPTION_KEY` | Required. Base64 of at least 32 random bytes (`openssl rand -base64 32`). It seals the stored Jira tokens. A value that is not base64 or decodes to fewer than 32 bytes stops the server at startup. Unset, every issue tracker route answers `503` and nothing is linked. |
| `ISSUE_TRACKER_EGRESS_ALLOWLIST` | Optional. Comma-separated hostnames (no port), IPs or CIDR ranges that are allowed in addition to public addresses. Public addresses (Jira Cloud) are always reachable; the list only opens private, loopback or link-local destinations. A malformed entry stops the server at startup. |
| `PUBLIC_BASE_URL` (or `EMAIL_BASE_URL`) | The address of the web UI. Remote links and merge comments point there. Without either, Nipa still records links and shows chips, but it queues no remote links on Jira, and merge comments carry no URL. The server logs a warning at startup. |

Like every setting, these come from the environment, `.env` or `config.yaml`
(see [SERVER_GUIDE.md](SERVER_GUIDE.md)).

### Private Jira Data Center hosts

Outbound requests to Jira go through the same egress guard as webhooks. The
guard checks the address it actually dials, does not follow redirects and
ignores proxy settings. A Data Center site on a private network, on loopback or
reached through a private address must be listed:

```
ISSUE_TRACKER_EGRESS_ALLOWLIST=jira.studio.internal
# or a range: ISSUE_TRACKER_EGRESS_ALLOWLIST=10.20.0.0/16
```

The list only adds destinations: public addresses stay reachable whatever it
holds, so Jira Cloud keeps working next to a listed private Data Center. A
listed hostname is trusted whatever it resolves to; any other private address
stays blocked. `WEBHOOK_EGRESS_ALLOWLIST` works the same way for webhooks.

Nipa verifies TLS certificates against the system trust store. For a Data
Center with an internal CA, add the CA to the trust store of the host (or set
`SSL_CERT_FILE` / `SSL_CERT_DIR` for the `nipad` process).

## 2. Create a Jira service account

Every call is made as the account of the connection, and comments and links
appear under its name, so use a dedicated account (for example
`nipa-bot@studio.com`) instead of a person's.

Grant it, on the Jira projects you will link, and nothing more:

| Jira permission | Used for |
| --- | --- |
| Browse Projects | Resolving titles and statuses, the issue picker search, finding transitions |
| Add Comments | The merge comment (only when "Comment on merge" is on) |
| Link Issues (remote links) | Adding and removing the remote links to merge requests and commits. Jira also needs issue linking turned on in its global settings. Some Data Center versions may also want Edit Issues for remote links (unverified, check against your Jira): grant it only if link jobs fail with a permission error. |
| Transition Issues | Moving issues to the opened and merged statuses (only when you set them) |

Anything the account can browse under the configured project keys becomes
readable by every reader of the linked Nipa project (see
[Visibility](#visibility)), so keep its Browse permission to the projects you
link.

### Jira Cloud: email and API token

1. Sign in to <https://id.atlassian.com/manage-profile/security/api-tokens> as
   the service account and create an API token.
2. In Nipa choose **Jira Cloud: account email and API token**, enter the
   account email and the token. Nipa sends them as HTTP Basic auth.

### Jira Data Center: personal access token

1. Sign in to Jira as the service account, open **Profile → Personal Access
   Tokens** and create a token. Personal access tokens need a Jira Data
   Center version that supports them (8.14 and later per Atlassian; not
   verified by Nipa, check against your Jira).
2. In Nipa choose **Jira Data Center: personal access token**. Nipa sends it
   as `Authorization: Bearer <token>`; no account email is needed.

Nipa talks to `/rest/api/2` on both deployments.

## 3. Connect the organization

An organization owner opens **Organization settings → Issue tracker**
(`/<org>/settings?tab=issues`) and fills in:

- **Name**: a label, `Jira` by default.
- **Base URL**: the site, for example `https://studio.atlassian.net` or
  `https://jira.studio.internal/jira` (a context path is allowed; no
  credentials, query or fragment). Use the final address: redirects are not
  followed, so an `http://` URL that redirects to `https://` fails.
- **Authentication**, **Account email** (Cloud) and **Token**.

**Connect** probes the site first (`GET /rest/api/2/myself`, then
`/serverInfo`) and saves nothing when the probe fails; the error names the
failed check (see [Troubleshooting](#troubleshooting)). **Test** probes the
saved connection again and shows the site title, deployment and the account
it is signed in as.

Rules:

- One connection per organization.
- The token is write-only: it is never returned by the API or shown again.
  Leave the field blank to keep the stored token; it must be entered again
  when the base URL changes.
- **Delete** is refused while a project still uses the connection.
- Org members who are not owners see the kind, name and base URL only.
- Changes are audited as `issue_tracker.created|updated|deleted` (never with
  the token).

The REST equivalents are `GET/PUT/DELETE /api/v1/orgs/{org}/issue-tracker` and
`POST /api/v1/orgs/{org}/issue-tracker/test` (see [WEB_UI.md](WEB_UI.md)).

## 4. Enable it per project

A project admin opens **Project settings → Issue tracker**
(`/<org>/<project>/settings?tab=issues`):

| Setting | Default | Effect |
| --- | --- | --- |
| Jira project keys | (required) | 1 to 20 keys such as `GAME, ART` (uppercase letters, digits and `_`, starting with a letter). Only these keys are recognized, linked and looked up. |
| Link merge requests on the Jira issue | on | Adds a remote link per issue to the merge request (title `#12 New rig`). Off stops the Jira links only; Nipa still records the links and shows the chips. |
| Link commits on the Jira issue | Off | `Off`, `Default branch only` or `All branches`: adds a remote link to each pushed commit that mentions the issue. Imported commits (`nipa import git`) are never linked. |
| Comment on the issue when a merge request merges | on | Posts `<name> merged merge request #12 "New rig" in org/project.` plus the merge request URL. |
| Status when a merge request opens | empty | When set, moves the issues an open, non-draft request closes or whose key leads its source branch name, unless the issue is already in a done category. A draft moves nothing until it is marked ready for review. |
| Status when a merge request merges | empty | When set, moves only the issues the request closes (a closing keyword in its title, description or commits) to this status on merge. Empty = link only. |
| Active | on | While inactive no new links are recorded, no jobs are queued and no chips are shown. |

Statuses are matched by name, case-insensitively, against the transitions the
issue's workflow offers from its current status. Changes are audited as
`project.issue_tracker.set|cleared`. **Remove** stops the project from using
the connection.

## How keys are detected

Only keys with one of the project's configured prefixes count (`GAME-123`
with `GAME` configured; `UTF-8` never matches). In commit messages and merge
request titles and descriptions the key must be written in upper case and the
number cannot start with `0`. At most 20 distinct keys are read from one text.

| Source | Linked to |
| --- | --- |
| Commit message | The pushed commit (one link per key) |
| Branch name | The branch, when the key leads the last path segment: `GAME-123-new-rig`, `feature/game-123-new-rig` (case-insensitive here) |
| Merge request title, description and source branch name | The merge request. Editing the request replaces its links, so removing a key removes its link (and the remote link on Jira). |

A key preceded by a closing keyword is a **closing** reference, any other is
a **mention**: `fix`, `fixes`, `fixed`, `close`, `closes`, `closed`,
`resolve`, `resolves`, `resolved` (any case, optional `:`), for example
`Fixes GAME-123` or `resolved: GAME-123`. Each key needs its own keyword:
in `Fixes GAME-1, GAME-2` only `GAME-1` closes.

Only a merged merge request transitions an issue to the merged status. A
`Fixes GAME-123` in a commit pushed straight to a branch is recorded as a
closing link and nothing more; the same commit inside a merge request makes
the request close `GAME-123` when it merges.

To start work on an issue with the right branch name:

- CLI: `nipa branch -c --issue GAME-123` (named `GAME-123-<slug of the issue
  title>`) or `nipa branch -c fix-rig --issue GAME-123` (`GAME-123-fix-rig`).
  The key is checked with the tracker first; see
  [CLIENT_GUIDE.md](CLIENT_GUIDE.md).
- Desktop: **Branch from issue…** in the command palette.
- Web: **Reference issue** under the merge request description searches the
  tracker and inserts the chosen key at the cursor. Type `Fixes ` before it to
  make it a closing reference.

## Where links show up

- Web UI: chips on commit lists and pages, the branch list, merge request
  lists and headers. A chip opens the issue page
  `/<org>/<project>/issues/<KEY>` (the merge requests, commits and branches
  linked to the issue); the arrow next to it opens the issue in Jira.
- CLI: `nipa log` (`Issues:` line), `nipa mr view`.
- Desktop: chips on history, merge requests and branches.
- `nipa mcp`: the `nipa_issues` tool resolves and searches issues.

Titles and statuses are fetched from Jira on demand and cached for 60 seconds
(10 seconds after a failure). When Jira is down the chips show the key only;
pages never fail because of Jira.

## What is sent to Jira

All writes go through an outbox and are sent by a background dispatcher, so
Jira being slow or down never fails a push or a merge.

| Job | Content |
| --- | --- |
| Link (merge request) | Remote link with the id `nipa:<org>/<project>:mr:<number>`, title `#<number> <title>`, summary `Merge request in <org>/<project> · <state>`, the merge request URL, and a resolved flag once merged or closed |
| Link (commit) | Remote link `nipa:<org>/<project>:commit:<id>`, title = first line of the commit message, summary `<org>/<project> · <branch>`, the commit URL |
| Unlink | Removes a merge request link whose key was edited out |
| Comment | The merge comment above |
| Transition | Moves the issue to the configured status |

Nipa never sends file names, paths or diffs. Re-sending a link updates the
same remote link (keyed by its id), so edits and retries never duplicate links
or comments.

### Visibility

- **Jira to Nipa.** Everyone who can read the Nipa project sees the title,
  status, type and assignee of any issue under the configured keys that the
  service account can browse, and can search them through the picker. Limit
  the keys and the account's Browse permission accordingly.
- **Nipa to Jira.** Merge request titles, commit subject lines, the org,
  project and branch names, the merger's display name and the links become
  visible to every Jira user who can see the issue, even when the Nipa
  repository is private. The links still need a Nipa login to open. Turn off
  "Link merge requests", leave "Link commits" off and "Comment on merge" off
  for a project whose names must not reach Jira.

## Jobs and retries

Project admins see the outbox under **Project settings → Issue tracker →
Jobs** (newest first, filter by Pending, Delivered or Failed): the issue, the
action, the link title or target status, attempts and the last error.

- Rate limits (`429`, honouring `Retry-After`), Jira `5xx` answers and
  timeouts are retried with backoff (30 s, 90 s, 4.5 min, ... at most 6 hours)
  up to 8 attempts.
- An unknown issue, refused credentials, a status the workflow cannot reach,
  a blocked destination and a bad request fail at once.
- Calls to one connection are spaced 200 ms apart.
- **Retry** queues a delivered or failed job again with a fresh attempt
  count, for example after fixing the token, a permission or a workflow.
- Delivered and failed jobs older than 30 days are deleted.
- Nothing is sent while the server is in `frozen` maintenance mode; the jobs
  wait.

The REST equivalents are `GET .../issue-tracker/jobs?state=&after=&limit=`
and `POST .../issue-tracker/jobs/{id}/retry`.

## Metrics

| Metric | Labels |
| --- | --- |
| `nipa_issue_tracker_requests_total` | `kind` (`jira`), `op` (`link`, `unlink`, `comment`, `transition`), `result` (`ok`, `retry`, `failed`) |
| `nipa_issue_tracker_jobs` | `state` (`pending`, `delivered`, `failed`), recounted every minute |

A growing `failed` count or `result="failed"` rate usually means a revoked
token or missing Jira permissions; see the Jobs tab for the error. Metrics are
served on `/metrics` (see [OBSERVABILITY.md](OBSERVABILITY.md)).

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| Issue tracker tabs say the integration is off; the API answers `503` | `INTEGRATIONS_ENCRYPTION_KEY` is not set. Set it and restart. |
| Check failed: `auth` | Jira refused the credentials (`401`/`403` on `/myself`): wrong email for a Cloud API token, an expired or revoked token, or Bearer used against Cloud with an API token. Also shown as "the stored token cannot be decrypted; enter it again" after the key changed (below). |
| Check failed: `tls` | The certificate is not trusted, does not match the host, or the URL speaks `https` to a plain HTTP port. Add the CA to the trust store or fix the URL. |
| Check failed: `reach` | Anything else: DNS, a refused or timed-out connection (15 s), a redirect (use the final URL), a host blocked by the egress guard ("resolves to ..., which is not allowed": add it to `ISSUE_TRACKER_EGRESS_ALLOWLIST`), or a `404` because the base URL lacks the context path. |
| Chips show keys but no titles | Jira is unreachable or refuses the account; titles come back once it answers. Check **Test** in the org settings. |
| No chips at all | The project settings are missing or inactive, the key's prefix is not configured, or the key is lower case in a commit message. Links are recorded when the event happens: earlier commits are not linked retroactively. |
| Link jobs never appear | `PUBLIC_BASE_URL` / `EMAIL_BASE_URL` is unset, or "Link merge requests" / "Link commits" is off. |
| Transition jobs fail with "cannot move from X to Y" | The workflow has no transition from the issue's current status to the configured one. Fix the status name or the workflow, then **Retry**. |
| `Delete` on the connection answers 409 | A project still uses it; remove it from the projects first. |

### Losing or rotating the encryption key

`INTEGRATIONS_ENCRYPTION_KEY` is the only way to read the stored tokens.
Without the old key they cannot be decrypted: **Test** reports "the stored
token cannot be decrypted; enter it again", resolving titles stops (chips
show keys only) and queued jobs fail. Project settings, links and job history
are kept. To recover, start the server with the new key, have an org owner
save each connection again with its token, then **Retry** the failed jobs.
Keep the key with your other server secrets and back it up with the database.
