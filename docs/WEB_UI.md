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
| `/` | repositories | organizations the caller belongs to |
| `/:org` | repositories | project list filtered by PBAC read |
| `/:org/:project` | tree browser | default branch root; legacy `?rev=&path=` links redirect |
| `/:org/:project/tree/:rev/*` | tree browser | branch/commit + directory; per-file last commit, README, go-to-file |
| `/:org/:project/blob/:rev/*` | file viewer | line numbers + syntax highlighting, image preview, raw/copy/download |
| `/:org/:project/commits[/:commit]` | history + diff | commit diff against its parent; `?path=` filters by file/dir |
| `/:org/:project/branches` | branches | create; default/protect/rename/delete for project admins |
| `/:org/:project/pulls[/:id]` | merge requests | list/create, detail + diff, merge/close/reopen |
| `/:org/:project/locks` | file locks | list binary asset locks, lock/unlock |
| `/:org/:project/settings` | project settings | branch protection + ACL rules/defaults |
| `/:org/settings` | organization settings | members and groups (org owner or global admin) |
| `/admin/users` | user administration | global admin CRUD, superadmin flags |
| `/settings/profile` | profile | name/photo and password |

## Browse API

`GET …/tree` lists one directory. `?history=1` adds `latest_commit` (the newest
commit that touched the directory) and `last_commit` on every entry;
`?recursive=1` flattens every readable file under `?path=` with repo-relative
paths (used for "go to file" search). History walks at most 50 first-parent
commits, comparing subtree hashes, and stops once every entry is resolved.

`GET …/commits?path=` keeps only the commits that touched a file or directory
(scan capped at 500 commits). Hidden paths return 404, like the tree and blob
endpoints.

## Permission gating

`GET /api/v1/orgs/{org}/projects/{project}/permissions/me` supplies the
capability mask. The UI hides controls the caller cannot use (`write` for
branch creation, `admin` for settings/ACL, org `owner` or global admin for
org settings), but every request is re-authorized server-side: hidden paths
return 404 and denied mutations return 403.
