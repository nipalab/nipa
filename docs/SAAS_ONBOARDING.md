# SaaS onboarding (registration + social login)

Design and implementation notes for the enterprise SaaS deployment
(`nipad-ee` with `SAAS_MODE=1`). Two flows are implemented: self-registration
with email confirmation and social login/signup (Google + GitHub). Both end in
the same first-run onboarding step. Paid seats and storage (Paddle) are
described in `docs/BILLING.md`.

The free `nipad` never registers these routes, so every endpoint below answers
404 there — clients feature-detect by calling `GET /api/v1/auth/capabilities`.

## Config

| Env | Meaning |
|---|---|
| `SAAS_MODE` | `1` enables registration + social routes in `nipad-ee`; also turns on the cloud storage ledger (`STORAGE_LEDGER=cloud`) so the seeded free-tier quota is enforced and visible |
| `SAAS_FREE_QUOTA_BYTES` | free-tier quota seeded per new account (default `16106127360` = 15 GiB) |
| `SAAS_TERMS_URL` | JSON document the website publishes with the current terms version (`https://nipalab.com/legal.json` → `{"version":"2026-11-10"}`); fetched at startup and every 15 minutes, a failed fetch keeps the last good version |
| `SAAS_TERMS_VERSION` | fallback terms version used until the first successful fetch, or on its own without `SAAS_TERMS_URL`. With neither, the acceptance step is off and startup logs a warning |
| `EMAIL_BASE_URL` | public web UI base URL; confirmation links are `<base>/verify?token=…` (required in SaaS mode) |
| `EMAIL_SENDER` | must be `smtp`/`sendgrid`/`scaleway`/`http` in SaaS mode to deliver confirmation emails; `off` logs a warning and creates accounts without sending |
| `OAUTH_BASE_URL` | public base URL used to build OAuth callback URLs |
| `OAUTH_GOOGLE_CLIENT_ID` / `OAUTH_GOOGLE_CLIENT_SECRET` | enable Google sign-in |
| `OAUTH_GITHUB_CLIENT_ID` / `OAUTH_GITHUB_CLIENT_SECRET` | enable GitHub sign-in |

Providers are enabled per credential pair; a provider appears in
`/capabilities` and the SPA only when both values are set. The authenticated
identity routes (list/unlink/start-link) exist whenever `SAAS_MODE` is on —
unlike the provider entry points they keep working with no credentials
configured, and linking an unconfigured provider answers 404. Register the
callback URLs with the providers:
`<OAUTH_BASE_URL>/api/v1/auth/oauth/google/callback` and
`<OAUTH_BASE_URL>/api/v1/auth/oauth/github/callback`.

## Terms acceptance

The version lives in the nipa-web repository (`LEGAL.version` in
`src/consts.ts`), which renders the pages' "Last updated" date and builds
`/legal.json` from it (Caddy mirrors the path with the marketing site). To
publish new terms, change the text and `LEGAL.version` there and deploy the
website: within one refresh interval the server records new acceptances
against the new version, with no server change or restart. Keep
`SAAS_TERMS_VERSION` close to the current version as the fallback.

With a terms version available, every account records which terms version it
accepted, when and how (`user_terms_acceptances`: `user_id`, `version`,
`source` `registration|oauth:<provider>|app`, `accepted_at`; the first
acceptance of a version is kept).

- `GET /api/v1/auth/capabilities` adds `terms: {version}`.
- `POST /api/v1/auth/register` requires `accept_terms: true` (400 otherwise)
  and records the acceptance in the account-creation transaction; the
  `user.created` audit entry carries `terms_version`.
- `GET /api/v1/auth/oauth/{provider}/start?accept_terms=1` puts the current
  version in the signed state. The callback records it for the new or
  signed-in account, unless the version changed during the round-trip.
  Linking an identity never accepts terms.
- `GET /api/v1/me/terms` returns `{version, accepted_version?, accepted_at?,
  required}`; `required` is true while the account has no acceptance (social
  sign-ups started from the login page, accounts created before the setting).
  `POST /api/v1/me/terms {version}` accepts the current version (409 for an
  outdated one) and is audited as `user.terms.accepted`.

Like GitHub, the SPA sign-up page states "By creating an account, you agree to
the Terms of Service ... see the Privacy Policy" (links to `/terms` and
`/privacy`) under the form; creating the account or continuing with a social
provider from that page sends the acceptance. The login and sign-up pages show
a Terms · Privacy · Docs · Contact footer in SaaS mode. `TermsGate` in the app
shell holds every signed-in page until a user with `required: true` ticks the
checkbox and accepts (social sign-ups started from the login page, accounts
created before terms acceptance existed). A new version does
not ask existing users again; the terms announce material changes by email.

## Registration with email confirmation

1. `POST /api/v1/auth/register` `{name, email}` — validates the identity and
   creates an **inactive, password-less** account (`users.email_verified =
   false`) plus a one-time verification token (`email_verifications`, sha256
   of a 32-byte secret, 72 h TTL, one row per user, resending replaces it) and
   the free-tier `owner_quota` seed, then queues the confirmation email in the
   existing mail outbox and kicks the dispatcher. Responds `202
   {status: "pending_verification", email}`; no session is issued. An address
   that already has an account answers the same `202` (a confirmed account's
   owner gets a "you already have an account" notice through the same outbox,
   an unconfirmed one gets a fresh token), so sign-up cannot be used to probe
   for accounts by response or by timing.
2. The email contains `<EMAIL_BASE_URL>/verify?token=…`; the SPA `/verify`
   page asks the link holder to choose a password (≥ 8 chars, ≤ 72 bytes) and posts
   `POST /api/v1/auth/verify {token, password}` — the password is stored, the
   account is activated, the token is consumed and the response is the
   standard login result (access token + HttpOnly refresh cookie), so the SPA
   lands signed in. The password is chosen only by whoever controls the
   mailbox, so registering (or re-registering) someone else's address can
   never plant a password the attacker knows. A weak password is rejected
   without using up the token.
3. Until confirmation the account cannot log in: password, refresh-token and
   SSH logins all refuse with `403 email not confirmed`. The login page offers
   a resend.
4. `POST /api/v1/auth/register/resend {email}` reissues the token; apart from the
   throttle it always answers `200` so it cannot be used to probe for accounts. `register` and
   `resend` share a per-email budget (`register_email`, see
   `docs/RATE_LIMIT.md`); a throttled call gets a `429` and sends nothing,
   whether or not the address has an account.

## Social login and signup

1. The SPA's social buttons link to
   `GET /api/v1/auth/oauth/{provider}/start?redirect=/oauth/callback?from=…`
   (anonymous flow): the redirect target must be the SPA callback route that
   exchanges the one-time code, and the post-login destination rides along in
   its own `from` parameter. The endpoint sets a signed state (HMAC-SHA256
   with a key derived from `JWT_KEY` by HKDF-SHA256, label
   `nipa oauth state v1`, 10 min TTL) plus the HttpOnly `nipa_oauth_binding`
   cookie (Lax, path `/api/v1/auth/oauth`) whose hash is in the state, and
   302s to the provider. The redirect must be a relative path: absolute URLs,
   anything starting with `//` (`///host` too), backslashes, control
   characters and empty, `.` or `..` path segments (`/a/..//host`) fall back
   to `/` instead of being cleaned. States signed by an older binary (raw
   `JWT_KEY`) fail once after a deploy; the user starts the sign-in again.
2. `GET /api/v1/auth/oauth/{provider}/callback?code&state` exchanges the code,
   fetches the profile (Google userinfo; GitHub `/user` + verified primary
   email) and resolves the account:
   - a linked identity logs that account in;
   - a provider whose email is missing **or unverified** redirects with
     `?oauth_error=email_required`: an untrusted address must never create
     (squat) an account or activate one through auto-linking;
   - a **verified** provider email matching an existing account links the
     identity (an unconfirmed password account is activated — the provider
     proved the email) and logs in;
   - otherwise a new verified account is created with the provider avatar and
     the free-tier quota seed, and the identity is linked.
   The `email_conflict`/`identity_taken` error codes remain in the SPA error
   map for compatibility with older servers; current servers answer
   `email_required` for untrusted provider emails and `identity_taken` when a
   link attempt targets a provider account linked elsewhere.
3. The callback never puts a token in a URL: it stores a one-time
   `oauth_login_codes` row (2 min TTL, single use, `binding_hash` = hash of
   the browser's binding cookie) and redirects to
   `<redirect>?code=…` (`&new=1` for new accounts only), i.e. to
   `/oauth/callback?from=…&code=…`. The SPA's `/oauth/callback` page posts
   `POST /api/v1/auth/oauth/complete {code}` (same-origin, so the binding
   cookie goes along), receives the standard login result and returns to the
   `from` destination (`/onboarding` for new accounts).
4. Login CSRF guard: the callback keeps the binding cookie only when it issued
   a login code (the link flow and every error clear it). `complete` consumes
   the code, requires the cookie to match the stored hash (constant time) and
   always clears the cookie, so a code handed to another browser is refused
   with the same 404 as an unknown or expired code (and is burned). A code
   row without a `binding_hash` (written by the previous binary during a
   blue/green flip) is rejected too: in-flight logins fail once at deploy and
   the user retries.

## Unified onboarding

Both paths end on the same SPA step: `/onboarding` asks for a workspace name
and its URL slug (auto-derived from the name, editable) and calls
`POST /api/v1/orgs` (the caller becomes owner; a taken slug comes back as 409
and is shown at the slug field). Users who already belong to an organization
skip the step. Additional organizations can be created later through the normal
dialog — registration does not restrict that.

## Account management

- `GET /api/v1/me/identities` lists linked providers; `DELETE
  /api/v1/me/identities/{id}` unlinks one — refused (409) while it is the
  last login method and the account has no password.
- `GET /api/v1/me/identities/{provider}/start?redirect=…` returns
  `{url}` for the authenticated account-linking flow (the SPA navigates to
  it); the callback then redirects with `?oauth=linked`.
- Social-only accounts have an empty password: password login fails with the
  generic invalid-credentials error, and the profile page's change-password
  form requires the old password, so they set one after connecting any other
  login method (planned: an explicit "set password" flow).

## CLI for social accounts

`nipa login` supports email/password and SSH keys. A social-only account
registers an SSH key on the profile page and uses the existing key-only login
— no client changes required. A device-code style `nipa login --web` is
future work.

## Endpoints

Public (no bearer; on the `/api/v1/auth` WebService):

| Route | Purpose |
|---|---|
| `GET /api/v1/auth/capabilities` | `{registration, providers[]}` — 404 outside SaaS mode |
| `POST /api/v1/auth/register` | create an inactive account + send confirmation |
| `POST /api/v1/auth/register/resend` | reissue the confirmation email (always 200) |
| `POST /api/v1/auth/verify` | consume the confirmation token → login result |
| `GET /api/v1/auth/oauth/{provider}/start` | 302 to the provider |
| `GET /api/v1/auth/oauth/{provider}/callback` | provider callback → SPA redirect |
| `POST /api/v1/auth/oauth/complete` | exchange the one-time code → login result |

Authenticated (on `/api/v1`):

| Route | Purpose |
|---|---|
| `GET /api/v1/me/identities` | list linked social accounts |
| `GET /api/v1/me/identities/{provider}/start` | begin linking (returns `{url}`) |
| `DELETE /api/v1/me/identities/{id}` | unlink a social account |

Mini-RPCs since both flows reuse the shared login machinery:

- `usecase.Auth.LoginResult` (was `generateLoginResult`) issues the token pair
  for any newly authenticated user.
- `api.WithPublicRoutes` / `serverapp.WithPublicRoutes` attach extra routes to
  the unfiltered `/api/v1/auth` WebService (go-restful rejects a second
  WebService with the same root path).
- `internal/usecase.ValidateUserIdentity` / `ValidatePassword` and the
  admin-gate-free `User.CreateAccount` are shared between the admin user
  creation path and onboarding.

## Schema

The onboarding tables came with the initial migrations
(`ee/db/migrations/postgres/000001_init_schema`, and
`db/migrations/sqlite/000002_user` for the `users` columns); later changes are
new migration files:

- `users.email_verified BOOLEAN NOT NULL DEFAULT true` +
  `email_verified_at` (init) — pre-existing and admin/service accounts stay
  verified; only the registration repo writes `false`
  (`UserCreateUnverified`).
- `email_verifications (user_id PK, token_hash UNIQUE, expires_at)` (init)
- `user_identities (id, user_id, provider, provider_account_id, email,
  UNIQUE(provider, provider_account_id))` (init)
- `oauth_login_codes (code PK, user_id, expires_at, binding_hash)` (init);
  the nullable `binding_hash` was added by
  `000005_oauth_login_code_binding`.
- `user_terms_acceptances (user_id, version, source, accepted_at,
  PK(user_id, version))` was added by `000002_user_terms_acceptances`.

All of these are postgres-only apart from the `users` columns, which exist in
both dialects because the login gate lives in shared `internal/usecase/auth.go`.

## Future work (agreed, not built)

**Billing** is built: seats and storage are sold through Paddle inside
`nipad-ee` (`ee/billing`), see `docs/BILLING.md`. The external billing service
sketched here earlier was dropped.

**Other**: device-code `nipa login --web`, password reset, explicit
"set password" for social accounts, email-change flows. The public endpoints
are rate limited per IP and per account (`docs/RATE_LIMIT.md`).
