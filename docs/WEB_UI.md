# Web UI

The SPA in `web/` is built with Vite + React 18 + Primer React and is embedded
into the `nipad` binary (`make web` → `web/server/dist` → `webui.Handler`).
It talks to the REST API under `/api/v1` (same-origin; the Vite dev server
proxies `/api` and `/docs` to `NIPA_SERVER_URL`).

## Auth

- Access token lives in memory only; the refresh token is an `HttpOnly` cookie
  (`web/src/api/client.ts`). `AuthProvider` silently refreshes on load, keeps
  `/me` in context, and reacts to cross-tab login/logout events.
- `RequireAuth` guards every route except `/login` and redirects back after
  sign-in.

## Routes

| Route | Page | Notes |
|---|---|---|
| `/` | repositories | organizations the caller belongs to; "New organization" dialog |
| `/:org` | repositories | project list filtered by PBAC read |
| `/:org/:project` | tree browser | default branch root; legacy `?rev=&path=` links redirect |
| `/:org/:project/tree/:rev/*` | tree browser | branch/commit + directory; per-file last commit, README, go-to-file |
| `/:org/:project/blob/:rev/*` | file viewer | line numbers + syntax highlighting, image preview, raw/copy/download |
| `/:org/:project/commits[/:commit]` | history + diff | commit diff against its parent; `?path=` filters by file/dir |
| `/:org/:project/branches` | branches | create; default/protect/rename/delete for project admins |
| `/:org/:project/merges[/new]` | merge requests | list with status/author/source/target/assignee filters plus free-text search (status includes a `draft` pseudo-filter) and "Load more" pagination; dedicated create page with a "create as draft" checkbox |
| `/:org/:project/merges/:id` | merge request detail | GitHub-style conversation (description, timeline, comments, reviewers/participants sidebar, merge box) plus Commits and File changes tabs; author/admin can edit title/description |
| `/:org/:project/locks` | file locks | list binary asset locks, lock/unlock |
| `/:org/:project/settings` | project settings | branch protection + ACL rules/defaults + webhooks |
| `/:org/settings` | organization settings | members and groups (org owner or global admin) |
| `/admin/users` | user administration | GitHub-style list with filter, avatar/role labels and row actions |
| `/admin/users/new` | new user | dedicated create page (name, email, password) |
| `/settings/profile` | public profile | GitHub-style settings layout: avatar preview + photo URL + name, and password change with confirmation/validation |

## Browse API

`GET …/tree` lists one directory. `?history=1` adds `latest_commit` (the newest
commit that touched the directory) and `last_commit` on every entry;
`?recursive=1` flattens every readable file under `?path=` with repo-relative
paths (used for "go to file" search). History walks at most 50 first-parent
commits, comparing subtree hashes, and stops once every entry is resolved.

`GET …/commits?path=` keeps only the commits that touched a file or directory
(scan capped at 500 commits). Hidden paths return 404, like the tree and blob
endpoints.

## Tags API

Release tags are exposed at `GET/POST …/tags` and `GET/DELETE …/tags/{name}`
(`internal/http/api/tag.go`); the web API client already provides
`listTags`/`createTag`/`deleteTag` in `web/src/api/endpoints.ts`. There is no
Tags page in the SPA yet.

## Organizations API

`GET /api/v1/orgs` lists the caller's memberships; `POST /api/v1/orgs` creates
an organization (name + optional slug) and makes the caller its owner. The home
page offers the "New organization" dialog (`web/src/components/CreateOrgDialog.tsx`,
`createOrg` in `web/src/api/endpoints.ts`). Member and group management lives on
the organization settings page (`/:org/settings`).

## Merge request reviews

The merge request detail page is tabbed like GitHub, driven by `?tab=`:
**Conversation** (GitHub-style header with state and branch info, description
card, interleaved timeline events/reviews/comments — including inline code
threads with a diff snippet of the commented code, their `file:line` context
and a "View on file" link — the opening description labelled as such, comment
composer, and a sidebar with reviewers, participants and the merge box with
merge/close/reopen; the merge box picks the merge strategy (fast-forward, merge
commit, squash, rebase) and whether to delete the source branch, and with a
non-fast-forward strategy a diverged source stays mergeable),
**Commits** (GitHub-style timeline of the source-branch commits, oldest first,
with author, timestamp, copy-hash and browse-at-commit actions), and
**File changes** (GitHub-style: file tree sidebar with
status/counters, filter box, diffstat and Unified/Split toggle, per-file
collapse and Viewed checkboxes, diff with inline comments). The conversation
composer can attach inline comments (file/line drafts) to a review decision,
and the merge box explains review blocks (`blocked_by` = `draft`,
`changes_requested`, `insufficient_approvals`, `required_reviewers` or
`status_checks`) and disables merging until they clear. Draft requests show a
"Draft" marker in the list and header; the
author/admin can mark one ready from the header or the edit dialog, which also
carries the draft toggle. The sidebar shows the
reported status checks (state + details link) and the assignees with a picker.
The project settings branch card exposes
`dismiss_stale_approvals`, `require_status_checks` with required check names,
and a required-reviewers picker next to the approvals input. Posting
inline comments or reviews refreshes the shared thread list, so both tabs and
the overview stay in sync.

The diff renderer (`web/src/components/repo/DiffView.tsx`) renders hunks
**side by side by default** with a Unified/Split toggle, and is shared with
the commit detail page. Review comments float directly under their anchored
line: each thread card (`web/src/components/repo/ThreadCard.tsx`) supports
inline reply, resolve/reopen and edit/delete of your own comments, and a new
comment opens a composer in place. Files can be filtered to the lines that
carry open threads. Comment threads anchored to a line the source head no
longer shows are marked outdated; a new push starts a new review round and
dismisses previous decisions (`dismissed_reason = new_commits`), keeping the
history.

The REST endpoints behind it: `GET/POST .../reviews`,
`GET .../review-state`, `DELETE .../reviews/{reviewId}`,
`POST .../reviews/{reviewId}/dismiss`, `GET/POST .../threads`,
`POST .../threads/{threadId}/comments`,
`PATCH/DELETE .../comments/{commentId}`,
`POST .../threads/{threadId}/resolve`, `DELETE .../threads/{threadId}`,
`GET/POST/DELETE .../review-requests`, `GET .../commits`, and `GET .../timeline`
(see `internal/http/api/merge_request_review.go` and
`internal/http/api/merge_request.go`).

## Permission gating

`GET /api/v1/orgs/{org}/projects/{project}/permissions/me` supplies the
capability mask. The UI hides controls the caller cannot use (`write` for
branch creation, `admin` for settings/ACL, org `owner` or global admin for
org settings), but every request is re-authorized server-side: hidden paths
return 404 and denied mutations return 403.
