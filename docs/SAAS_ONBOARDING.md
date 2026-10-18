# SaaS onboarding (registration + social login)

Design and implementation notes for the enterprise SaaS deployment
(`nipad-ee` with `SAAS_MODE=1`). Two flows are implemented: self-registration
with email confirmation and social login/signup (Google + GitHub). Both end in
the same first-run onboarding step. Billing/subscription is planned but not
built yet; the agreed design is recorded at the end.

The free `nipad` never registers these routes, so every endpoint below answers
404 there — clients feature-detect by calling `GET /api/v1/auth/capabilities`.

## Config

| Env | Meaning |
|---|---|
| `SAAS_MODE` | `1` enables registration + social routes in `nipad-ee`; also turns on the cloud storage ledger (`STORAGE_LEDGER=cloud`) so the seeded free-tier quota is enforced and visible |
| `SAAS_FREE_QUOTA_BYTES` | free-tier quota seeded per new account (default `16106127360` = 15 GiB) |
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

## Registration with email confirmation

1. `POST /api/v1/auth/register` `{name, email, password}` — validates the
   identity and password (≥ 8 chars), rejects duplicate emails (409), creates
   an **inactive** account (`users.email_verified = false`) plus a one-time
   verification token (`email_verifications`, sha256 of a 32-byte secret,
   72 h TTL, one row per user, resending replaces it) and the free-tier
   `owner_quota` seed, then queues the confirmation email in the existing
   mail outbox and kicks the dispatcher. Responds `202
   {status: "pending_verification", email}`; no session is issued.
2. The email contains `<EMAIL_BASE_URL>/verify?token=…`; the SPA `/verify`
   page posts `POST /api/v1/auth/verify {token}` — the account is activated,
   the token is consumed and the response is the standard login result
   (access token + HttpOnly refresh cookie), so the SPA lands signed in.
3. Until confirmation the account cannot log in: password, refresh-token and
   SSH logins all refuse with `403 email not confirmed`. The login page offers
   a resend.
4. `POST /api/v1/auth/register/resend {email}` reissues the token; it always
   answers `200` so it cannot be used to probe for accounts.

## Social login and signup

1. The SPA's social buttons link to
   `GET /api/v1/auth/oauth/{provider}/start?redirect=/oauth/callback?from=…`
   (anonymous flow): the redirect target must be the SPA callback route that
   exchanges the one-time code, and the post-login destination rides along in
   its own `from` parameter. The endpoint sets a signed state (HMAC with
   `JWT_KEY`, 10 min TTL, relative-path-only redirect) and 302s to the
   provider.
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
   `oauth_login_codes` row (2 min TTL, single use) and redirects to
   `<redirect>?code=…` (new accounts only), i.e. to
   `/oauth/callback?from=…&code=…`. The SPA's `/oauth/callback` page posts
   `POST /api/v1/auth/oauth/complete {code}`, receives the standard login
   result and returns to the `from` destination (`/onboarding` for new
   accounts).

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

Edited into the existing consolidated migrations (project is not live yet, no
new migration files):

- `users.email_verified BOOLEAN NOT NULL DEFAULT true` +
  `email_verified_at` — pre-existing and admin/service accounts stay verified;
  only the registration repo writes `false` (`UserCreateUnverified`).
- `email_verifications (user_id PK, token_hash UNIQUE, expires_at)`
- `user_identities (id, user_id, provider, provider_account_id, email,
  UNIQUE(provider, provider_account_id))`
- `oauth_login_codes (code PK, user_id, expires_at)`

All of these are postgres-only apart from the `users` columns, which exist in
both dialects because the login gate lives in shared `internal/usecase/auth.go`.

## Future work (agreed, not built)

**Monthly/yearly billing.** The external billing service keeps charging and
drives entitlement updates; nipad-ee stores and enforces them:

- `billing_subscriptions (user_id PK, plan, quota_bytes, current_period_end,
  status)` — monthly vs yearly is just how far `current_period_end` reaches;
  pricing, invoices, proration, dunning and reminder emails stay external.
- The billing service is a nipad super-admin service account (created by ops
  through the existing admin API); it authenticates with normal nipad access
  tokens (gRPC login keeps the refresh token client-side) and updates the
  entitlement through `PUT /api/v1/billing/owners/{user}/subscription`
  `{plan, quota_bytes, current_period_end, status}` after each successful
  renewal. It identifies the customer from the user's access token validated
  through the existing `GET /api/v1/me`; a reconciler can use `user_id`.
- Enforcement is lazy at the storage-ledger gates: expired/canceled
  subscriptions return 402 (read-only lock) at presign/confirm/merge and at
  secondary writes (project/branch/MR creation), and fall back to the
  free-tier `owner_quota` baseline. No cron in nipad.

**Other**: device-code `nipa login --web`, password reset, explicit
"set password" for social accounts, rate limiting on the public endpoints,
email-change flows.
