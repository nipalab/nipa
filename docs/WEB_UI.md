# Web UI

The SPA in `web/` is built with Vite + React 18 + Primer React and is embedded
into the `nipad` binary (`make web` → `web/server/dist` → `webui.Handler`).
It talks to the REST API under `/api/v1` (same-origin; the Vite dev server
proxies `/api` and `/docs` to `NIPA_SERVER_URL`).

In SaaS mode the marketing site is a separate Astro deployment (the `nipa-web`
repo on Netlify) served on `nipalab.com`; the deploy Caddyfile proxies its
pages and assets (`/`, `/_astro/*`, `/screenshots/*`, `/pricing` and the root
assets) from the Netlify origin (`MARKETING_UPSTREAM`), and marketing links
stay on `nipalab.com`. The same binary also carries an optional embedded
fallback (`ee/web/marketing`, built under base path `/web` with
`make marketing-dist`) that serves `/` and `/web/...` when no Caddy proxy is
configured. Either way the SPA keeps every other non-API path, so organizations
and projects live at `/{org}` and `/{org}/{project}/...` exactly as in the free
edition.

## Auth

- Access token lives in memory only; the refresh token is an `HttpOnly` cookie
  (`web/src/api/client.ts`). `AuthProvider` silently refreshes on load, keeps
  `/me` in context, and reacts to cross-tab login/logout events.
- `RequireAuth` guards every route except `/`, `/login`, `/register`, `/verify`
  and `/oauth/callback` and redirects back after sign-in.
- `/` is an entry redirect: anonymous visitors go to `/login`, signed-in users
  to `/dashboard`. On SaaS the server mirrors the marketing site at `/`, so the
  SPA redirect only applies to the free edition.

## Routes

| Route | Page | Notes |
|---|---|---|
| `/` | entry redirect | anonymous → `/login`, signed in → `/dashboard`; SaaS mirrors the marketing landing from `nipalab.com` instead |
| `/login` | sign in | password form, "Create an account" link and social buttons when the server reports them |
| `/register` | sign up | SaaS only (`/api/v1/auth/capabilities`); emails a confirmation link, then offers a resend |
| `/verify` | email confirmation | consumes `?token=` from the confirmation email and starts the session |
| `/oauth/callback` | social login handoff | exchanges `?code=` for a session, returns to the `?from=` destination, and shows `email_conflict`/`email_required` guidance |
| `/onboarding` | first-run setup | names the first workspace and its URL slug (skipped when the user already has an organization) |
| `/dashboard` | repositories | organizations the caller belongs to; "New organization" dialog |
| `/:org` | repositories | project list filtered by PBAC read; org owners also get a Repositories / Settings tab bar |
| `/:org/new` | new repository | org owners only; name + slug + description |
| `/:org/:project` | tree browser | default branch root; legacy `?rev=&path=` links redirect |
| `/:org/:project/tree/:rev/*` | tree browser | branch/commit + directory; per-file last commit, README, go-to-file |
| `/:org/:project/tree` | tree browser | rev-less form of the tree route (`?path=`) |
| `/:org/:project/blob`, `/blob/:rev/*` | file viewer | line numbers + syntax highlighting, image preview, raw/copy/download; binary files get a Lock/Unlock button (holder, branch scope and covering directory locks are shown) |
| `/:org/:project/commits[/:commit]` | history + diff | commit diff against its parent; `?path=` filters by file/dir; "Load more" pages 50 commits at a time (`start`/`limit`) |
| `/:org/:project/compare` | compare | `?base=&head=` take branches, tags or commit ids; renders the diff of `head` against `base` (`GET …/commits/{commit}/diff?base=`) |
| `/:org/:project/branches` | branches | "Load more" paging (keyset cursor); default/protect/rename/delete for project admins, delete and unprotect ask for confirmation; links to Compare |
| `/:org/:project/branches/new` | new branch | name + optional fork point (branch or commit id) |
| `/:org/:project/tags` | tags | list with "Load more" paging, create (name, target branch/commit, message) and delete (confirmed) for writers |
| `/:org/:project/merges[/new]` | merge requests | list with status/author/source/target/assignee filters plus free-text search (status includes a `draft` pseudo-filter) and "Load more" pagination; dedicated create page with a "create as draft" checkbox |
| `/:org/:project/merges/:id` | merge request detail | GitHub-style conversation (description, timeline, comments, reviewers/participants sidebar, merge box) plus Commits and File changes tabs; author/admin can edit title/description |
| `/:org/:project/locks` | file locks | list binary asset locks, lock/unlock |
| `/:org/:project/settings` | project settings | tabs via `?tab=`: General (rename, description, delete for org owners), Access (ACL rules/defaults), Branches (protection), Webhooks, Service tokens (create with scopes and optional expiry, token shown once, revoke), Email deliveries |
| `/:org/settings` | organization settings | tabs via `?tab=`: Members, Groups and Storage (usage, quota, per-project breakdown; the tab is hidden when the server has no ledger); org owner or global admin |
| `/admin/users` | user administration | list with filter, avatar/role labels and row actions: make/remove admin, edit email, reset password (dialog), deactivate (confirmed) |
| `/admin/users/new` | new user | dedicated create page (name, email, password) |
| `/settings/profile` | account settings | tabs via `?tab=`: Public profile, Password, Connected accounts (link/unlink; hidden when the server has no social login) and SSH keys (list/add/remove); Billing shares the same sidebar |
| `/settings/billing` | billing | account settings tab: the caller's billing-owner aggregate usage, quota and per-org breakdown; super admins can set an owner quota override, set it to unlimited or remove it; when capabilities carries `billing`, also the plan card (plan, status, interval, renewal/cancel date, seats used/limit, storage packs, past-due warning) and the plan editor (seat and storage-pack steppers, monthly/yearly toggle, Paddle.js price preview, Upgrade → checkout overlay on the free plan, Update plan + Manage billing on a paid plan); linked from the header and profile settings, shows an empty state when the server has no ledger |

## Reserved slugs

Organization and project slugs cannot use paths the router already owns.
Organizations reject `admin, api, assets, auth, dashboard, debug, docs,
healthz, login, metrics, oauth, onboarding, pricing, readyz, register,
screenshots, settings, verify, version and web`; projects reject `new` and `settings`. The check lives in
`internal/usecase/reserved.go` and is mirrored in `web/src/slug.ts`, so the
forms reject a reserved slug before the request reaches the server.

## Browse API

`GET …/tree` lists one directory. `?history=1` adds `latest_commit` (the newest
commit that touched the directory) and `last_commit` on every entry;
`?recursive=1` flattens every readable file under `?path=` with repo-relative
paths (used for "go to file" search). History walks at most 50 first-parent
commits, comparing subtree hashes, and stops once every entry is resolved.

`GET …/commits?path=` keeps only the commits that touched a file or directory
(scan capped at 500 commits). `start` (inclusive commit id) and `limit` page the
list, and `GET …/commits/{commit}/diff?base=` diffs against any base commit
(the compare view). Hidden paths return 404, like the tree and blob
endpoints.

## Tags API

Release tags are exposed at `GET/POST …/tags` and `GET/DELETE …/tags/{name}`
(`internal/http/api/tag.go`). The SPA Tags page (`/:org/:project/tags`) uses
`listTags` (keyset cursor `last_id` + `last_created_at`; the page over-fetches
one row to know whether a next page exists), `createTag` and `deleteTag` in
`web/src/api/endpoints.ts`; `getTag` is available for a single tag. The tree,
blob and compare ref pickers have a Tags tab that loads the first 100 tags when
opened. The server resolves tree/blob/diff revisions as branch names or commit
ids, so choosing a tag navigates with the tag's commit id and the picker shows
the tag name while that commit is selected.

## UI conventions

Pages share a small set of building blocks (`web/src/components/` and
`web/src/hooks.ts`) instead of hand-rolled markup:

- `Page` / `RepoPageShell` (Primer `PageHeader`, breadcrumbs), `SectionHeading`,
  `SettingsCard` / `SettingsRow` / `SettingsLayout` (sidebar + `?tab=`),
  `FormCard`, `RowList` / `Row`, `DataTable` (loading/empty states included),
  `EmptyState` (Blankslate), `NoPermission`, `ErrorBanner`, `Loading`,
  `LoadMore`.
- Dialogs: `FormDialog` (errors inside the dialog; return `false` from
  `onSubmit` to stay open) and `useConfirmAction()` for destructive actions;
  nothing uses `window.prompt`/`confirm`.
- Data: `useAsync`, `usePaged` + `sliceCursorPage` (first page + load more),
  `useAction` (busy/error/reload), `useOrgRole`, `useOrgMembers` (shared
  30 s cache cleared on sign-out), `useClipboard`.
- Display: `Timestamp`, `StateBadge`, `ActorAvatar`; `formatBytes` uses binary
  units.
- Design tokens: `main.tsx` imports the base size/typography and functional
  radius/typography stylesheets next to `dark.css`; `src/test/tokens.test.ts`
  fails when a component uses a size/typography token that is not loaded (an
  undefined `var()` invalidates the whole declaration, e.g. margin `auto`).

## SSH keys API

SSH public keys of the authenticated user are exposed at `GET/POST /api/v1/keys`
and `DELETE /api/v1/keys/{id}` (`internal/http/api/ssh_key.go`), with
`listMySSHKeys`/`addMySSHKey`/`removeMySSHKey` helpers in
`web/src/api/endpoints.ts`. A key carries an optional `name` (falls back to the
OpenSSH key comment when omitted) so users can identify it. Keys are the
credential for the SSH-key login flow; the profile page's "SSH keys" card
(`web/src/components/SSHKeysCard.tsx`) lists, adds and removes them.

## Organizations API

`GET /api/v1/orgs` lists the caller's memberships; `POST /api/v1/orgs` creates
an organization (name + slug; the API derives it from the name when omitted) and
makes the caller its owner. The home page offers the "New organization" dialog
(`web/src/components/CreateOrgDialog.tsx`, `createOrg` in
`web/src/api/endpoints.ts`). The onboarding page and the dialog require a valid
slug, auto-derive it from the name until the user edits it and show a taken-slug
409 at the field (same for project creation on the new repository page). Member
and group management lives on the organization settings page (`/:org/settings`).

## Onboarding API

The SaaS registration and social login surface (`GET /auth/capabilities`,
`POST /auth/register`, `POST /auth/register/resend`, `POST /auth/verify`,
`GET /auth/oauth/{provider}/start`, the provider callback,
`POST /auth/oauth/complete`, plus the authenticated `/me/identities` routes)
is documented in `docs/SAAS_ONBOARDING.md`. The SPA helpers
(`registerAccount`, `resendVerification`, `oauthStartURL`,
`getOnboardingCapabilities`, `listIdentities`, `startIdentityLink`,
`disconnectIdentity`) live in `web/src/api/endpoints.ts`; capabilities returns
null on 404, so the login/register pages hide the entry points on servers
without SaaS mode.

## Storage API

The enterprise storage ledger exposes `GET /orgs/{org}/storage`,
`GET /billing/me`, `PUT /admin/billing/owners/{user}/quota`,
`PUT /admin/orgs/{org}/billing-owner` and
`POST /admin/orgs/{org}/storage/recount` (see
`docs/CLOUD_STORAGE_QUOTA.md`). `getOrgStorage` returns null on 404, so the org
settings storage card hides itself when the server runs without a ledger; the
`/settings/billing` page shows the owner aggregate and the super-admin quota
editor, and is always linked (header + profile settings) — on servers without a
ledger it explains that storage billing is not enabled. `clearOwnerQuota`
(`DELETE /admin/billing/owners/{user}/quota`) removes the override.

## Billing API

With `BILLING_PROVIDER=paddle` (`docs/BILLING.md`), `GET /auth/capabilities`
adds `billing: {provider, environment, client_token, prices}` (typed as
`OnboardingCapabilities.billing`) and `GET /billing/me` adds the
`BillingSummary` fields (`plan`, `status`, `interval`, `seats`, `seats_used`,
`seat_limit`, `storage_packs`, `current_period_end`, `cancel_at_period_end`).
The SPA helpers are `createCheckout` (`POST /billing/checkout` →
`{transaction_id}`), `updateSubscription` (`PATCH /billing/subscription` →
summary) and `openBillingPortal` (`POST /billing/portal` → `{url}`). The
super-admin `POST /admin/billing/owners/{user}/sync` and
`POST /admin/billing/events/{id}/replay` routes have no SPA helper or UI.

The billing page shows plan controls only when capabilities carries `billing`
and `/billing/me` carries `plan`. Paddle.js is loaded on demand by
`web/src/billing/paddle.ts` (`loadPaddle`, once per page, sandbox through
`Paddle.Environment.set`; `subscribePaddleEvents`; `previewPrices` over
`Paddle.PricePreview`); tests mock that module. The upgrade flow is
`createCheckout` → `Paddle.Checkout.open({ transactionId })` → on
`checkout.completed`, poll `/billing/me` until the plan leaves `free`. Server
409s are shown inline.

A 402 whose message names the seat limit (adding an org member, a group member
or a user access rule) is rendered by `FormDialog` as `SeatLimitBanner`
("Seat limit reached", link to `/settings/billing`) instead of the generic
error banner.

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
org settings); organization owners receive the full project mask on every
project in their org, while plain members need PBAC rules. Every request is
re-authorized server-side: hidden paths return 404 and denied mutations
return 403.
