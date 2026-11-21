# Web UI internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

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

## Web UI

- Routes/API map lives in `docs/WEB_UI.md`. SPA uses `react-router-dom` v6,
  memory-only access token + `HttpOnly` refresh cookie, and a capability mask
  from `/permissions/me` to hide admin controls (server remains authoritative).
  The SPA root `/` is an entry redirect (`anonymous → /login`, signed in →
  `/dashboard`, the app home; `APP_HOME` in `web/src/routes.ts`). The SaaS
  marketing site is a separate Astro deployment (the `nipa-web` repo on
  Netlify) served at `https://nipalab.com`; `deploy/caddy/Caddyfile`
  mirrors its pages and assets on the app domain with the `@marketing` path
  matcher (`/`, `/pricing`, `/download`, `/support`, `/contact`, `/privacy`,
  `/terms`, `/guides*`, `/_astro/*`, `/screenshots/*`, root assets;
  `MARKETING_UPSTREAM` required, the Netlify origin, sent its own Host).
  `/docs` stays the REST API docs. Marketing links are root-relative
  (`siteUrl`/`appUrl` in nipa-web `src/consts.ts`), so
  marketing changes ship without rebuilding `nipad-ee`; `upgrade.sh` only
  rewrites the `reverse_proxy 127.0.0.1` line. The optional embedded fallback
  (`ee/web/marketing`, base `/web`, placeholder committed,
  `serverapp.WithMarketingUI` only when `SAAS_MODE=1`) serves the same paths
  when the Caddy proxy is not used; `make marketing-dist
  [MARKETING_DIR=../nipa-web]` builds with `MARKETING_BASE=/web/` and syncs the
  result into it. Organization/project slugs reject the paths owned by the
  router, marketing and fixed endpoints (`internal/usecase/reserved.go`,
  mirrored in `web/src/slug.ts`; a test checks every `@marketing` path is
  reserved, and `npm run routes` in nipa-web checks the Caddyfile covers the
  build).
  Pages cover repository/org lists, tree browser, blob viewer, commit history
  + diffs, branch management, merge requests (list/create; detail tabs for
  overview, commits and file changes; merge/reviews with decisions, inline
  floating threads, reviewer requests, activity timeline),
  file locks (list/lock/unlock), project/org settings (protection + ACL,
  members/groups), user administration
  and profile; the browse endpoints are served by `internal/usecase/browser.go`
  (+ `browser_history.go` for per-file/per-dir last-commit info) under
  `internal/http/api/`. Release tags have REST routes (`/tags`,
  `/tags/{name}`) and `endpoints.ts` helpers; the SPA has a Tags page (list, create,
  delete, ref-picker tab). Issue tracker views: chips on commits, branches
  and merge requests link to `pages/IssuePage.tsx` (`/:org/:project/issues/:key`,
  the work linked to one issue via `listIssueLinks`), and
  `components/repo/IssuePicker.tsx` adds "Reference issue" (search through
  `searchIssues`, insert the key into the description) to the merge request
  create page and edit dialog; details in `docs/WEB_UI.md` "Issue tracker API".
- Billing (`docs/BILLING.md`): `BillingPage` + `components/BillingPlan.tsx`
  show the plan card and editor only when `GET /auth/capabilities` carries
  `billing`, the editor showing the storage each seat/pack adds and the plan
  total (`web/src/billing/entitlement.ts`); `web/src/billing/paddle.ts` injects Paddle.js v2 from
  `cdn.paddle.com` on demand (once per page, `Environment.set("sandbox")`
  before `Initialize`, events fanned out through `subscribePaddleEvents`,
  `previewPrices` over `PricePreview`) and is mocked in tests. Checkout opens
  `Paddle.Checkout.open({ transactionId })` from `createCheckout`, never with
  client-side price ids; before the first checkout the editor has an optional
  discount code field (`#billing-discount-code`, sent as `discount_code`, a
  400 shown under the field, previews stay undiscounted). `FormDialog` renders a seat-limit 402 as
  `SeatLimitBanner` (link to `/settings/billing`). The app has no CSP; adding
  one must allow Paddle.js and its checkout frames.
- Maintenance (`docs/WEB_UI.md` "Maintenance"): `web/src/maintenance.ts` is a
  module store (public status polled every 60 s plus the current org/project
  route when signed in; `errorFromResponse` records every maintenance 503 and
  `refresh()` marks the session paused). `AppShell` renders
  `MaintenanceBanner` and `MaintenanceGate` (an active `full` window, or
  `frozen` once the session is paused, shows `pages/MaintenancePage.tsx`
  except on `/admin/maintenance` and the settings Maintenance tabs);
  `RequireAuth` shows it instead of the login redirect when `bootstrap()` was
  refused and restores the session once it ends. Window forms live in
  `components/MaintenanceForm.tsx` (admin page, org and project settings tabs
  for org owners and super admins).
- Keep `web/src/api/endpoints.ts` in sync with the REST handlers under
  `internal/http/`. `npm run lint` (tsc), `npm test` (vitest) and `make web`
  must stay green; the built SPA is embedded via `web/server/webui.go`.

