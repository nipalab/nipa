# Auth hardening plan

Plan for fixing the findings of the authentication review of the gRPC server
and the REST API (2026-11-11). The review was source-only: nothing was
exploited against a running server. No authentication bypass was found; the
work below closes one medium issue, several low ones and a set of hardening
gaps.

Status: phases 1 to 5 are merged, phase 6 is not started.

| Phase | State | PR |
|-------|-------|----|
| 1 Session revocation | done | #204 |
| 2 Refresh token hardening | done | #205 |
| 3 Login surface | done | #208 |
| 4 OAuth and SaaS | done | #210 |
| 5 Transport and server hardening | done | #211 |
| 6 New features | not started | |

The phase sections below keep the original plan text. Where the
implementation deliberately differs:

- Phase 2 keeps one `refresh_tokens` row per session and rotates it in place
  instead of per-token `used_at` rows with pruning. The token is
  `<id>.<tag>.<secret>`; the row holds the current and the previous secret
  hash (`token_hash`, `previous_hash`, `rotated_at`, `session_started_at`; no
  `family_id` or `used_at`). The previous secret is tolerated for 30 seconds
  after a rotation, after that it revokes the session and is audited; a
  session ends 90 days after it started.
- Phase 4 did not widen the binding cookie path: `/api/v1/auth/oauth` already
  covers `/api/v1/auth/oauth/complete`.
- Phase 5 added no key for extra origins: the only allowed origin is the one
  of `PUBLIC_BASE_URL`. A disallowed origin is not rejected, it gets no CORS
  headers and the browser blocks the response.

Follow-ups done in the same release as phases 2 to 5 (a deliberate choice:
every user signs in again once at that deploy, and the previous binary cannot
serve logins or refreshes during the flip):

- Migrations sqlite `000021` / postgres `000006`
  (`refresh_token_drop_plaintext`) delete the plaintext rows older than phase
  2 and drop `refresh_tokens.token`; the legacy-token fallback is gone.
- `ValidateToken` requires the JWT `iss`/`aud` claims.

Open follow-ups:

- Phase 6.

## Findings

| # | Severity | Finding | Phase |
|---|----------|---------|-------|
| 1 | Medium | Access tokens stay valid for 30 minutes after deactivation, password change or admin demotion; admin flags are read from the token | 1 |
| 2 | Low–Medium | Refresh tokens are stored in plaintext | 2 |
| 3 | Low–Medium | Refresh rotation has no reuse detection and no absolute lifetime | 2 |
| 4 | Low | OAuth login code is not bound to the browser (login CSRF) | 4 |
| 5 | Low | Login timing reveals OAuth-only and unconfirmed accounts | 3 |
| 6 | Low | Per-email login throttle allows targeted lockout; resend has no per-email limit | 3 |
| 7 | Low | Password policy: no maximum (bcrypt 72-byte limit), `ChangePassword` unthrottled and unaudited | 3 |

Hardening gaps without a demonstrated exploit: open CORS, a redirect sanitizer
that relies on `http.Redirect` cleanup, `JWT_KEY` reused for OAuth state, no
HTTP header/idle timeouts, cleartext transport on the free server,
unauthenticated `/metrics` and pprof on the main port, and a rate limiter that
fails open for login scopes.

### Finding details

1. `Auth.ValidateToken` (`internal/usecase/auth.go`) checks only signature and
   expiry. Every admin gate reads `IsAdmin`/`IsSuperAdmin` from the claims
   (`permission.go`, `user.go`, `org.go`, `group.go`, `project.go`,
   `audit.go`, `ee/billing`, `ee/storageusage`). A deactivated or demoted
   global admin can still call `POST /api/v1/users` inside the window and
   create a fresh verified account; a demoted super admin can promote it.
2. `SaveRefreshToken` writes the raw token in both repositories. A database or
   backup read yields a 60-day session for every user.
3. `LoginWithRefreshToken` deletes the old token and issues a new one with a
   fresh 60 days. A stolen token used once gives an independent session that
   renews forever.
4. The binding cookie is cleared in the OAuth `Callback`, and
   `POST /auth/oauth/complete` accepts any valid code. An attacker can hand a
   victim their own login code and sign the victim in to the attacker's
   account.
5. An unknown email burns a dummy bcrypt comparison, but an existing account
   with an empty password hash returns immediately. `Register` for a verified
   email also returns faster than a new signup.
6. `throttleIdentity` counts every attempt for an email from any IP before the
   password check. `register/resend` is limited per IP only.
7. `ValidatePassword` enforces only an 8-character minimum. bcrypt rejects
   more than 72 bytes at hash time, which surfaces as a 500.

## Rules for every phase

- Each phase ships on its own and stays forward compatible for one release,
  so both `nipad-ee` colors can run during a flip.
- Schema changes are new migration files in both dialects, followed by
  `make sqlc`; existing migrations are never edited. The phases added
  sequentially numbered pairs (the next free number per dialect), not the
  timestamp names `make migrate-create` produces.
- New code is covered at least 80% by tests.
- Done means `go vet ./...`, `make lint`, `go test ./...`, `npm test` and
  `make test-client-both` are green.
- `AGENTS.md` and the affected `docs/` pages are updated in the same phase.

## Phase 1: Session revocation

Goal: a deactivated, demoted or password-changed account loses access
immediately on gRPC and REST.

- Schema: `users.token_version BIGINT NOT NULL DEFAULT 0`.
- Claims: add `tv` in `Auth.LoginResult`. A token without it counts as
  version 0, so tokens issued by the old color stay valid during the flip.
- Validation: `ValidateToken` loads the user row, rejects a deleted user or a
  version mismatch, and overwrites `IsAdmin`/`IsSuperAdmin` in the returned
  claims from the row. The gRPC interceptor and the REST auth filter both call
  it, so the existing admin gates need no change.
- Bump points, in the same transaction as the change:
  `UpdatePasswordAndRevokeTokens`, `UpdateEmailAndRevokeTokens`,
  `DeactivateAndRevokeTokens`, `UpdateAdminFlags`, and the `ee` paths that
  reset credentials (`ResetUnverifiedCredentials`).
- Tests: usecase tests per bump point and for version 0 compatibility; e2e
  spec that a deactivated admin's old token gets 401 on `POST /users` and on
  a gRPC call.

Decision: one primary-key lookup per request, no cache. A cache reopens a
window and must be reasoned about across two colors; add one only if
profiling asks for it.

## Phase 2: Refresh token hardening

- Schema: add `token_hash`, `family_id`, `session_started_at`, `used_at` to
  `refresh_tokens`.
- Generation: 32 bytes from `crypto/rand`, stored as SHA-256 only.
- Legacy rows: look up by hash first, fall back to the plaintext column for
  one release. A follow-up migration drops the plaintext column.
- Rotation: mark the old row `used_at` instead of deleting it; the new token
  joins the same family and keeps `session_started_at`.
- Reuse detection: presenting an already-used token revokes the family and
  writes an audit entry.
- Absolute cap: refuse rotation when `session_started_at` is older than
  90 days.
- Pruning: used rows are deleted once they are past the grace window and the
  family has moved on, together with the existing expired-row prune.
- Tests: repository tests in both dialects; usecase tests for reuse, grace
  and cap; REST refresh handler tests.

Decision: the refresh handler tolerates two tabs racing on one cookie. Keep
that with a 10–30 second grace window in which a just-used token answers the
existing "not found" without revoking the family.

## Phase 3: Login surface

- Timing: call `spendPasswordCheck` when the stored hash is empty; make
  `Register` for an existing account cost about the same as a new signup.
- Throttle keying: key `auth_id` on email + client IP and add a higher
  email-only ceiling scope. The gRPC interceptor must attach `RequestMeta`
  for the public login RPCs, which it skips today.
- Resend: per-email throttle on `register` and `register/resend`.
- Passwords: 72-byte maximum in `ValidatePassword`, mirrored in the SPA forms
  (register/verify, profile, admin reset).
- `ChangePassword`: apply the identity throttle and add an audit action.
- Login audit: record successful and failed logins (password, SSH key,
  refresh reuse) with new actions in `internal/domain/audit_actions.go`.
- Limiter failure: for `auth_ip`/`auth_id`, fall back to the in-memory store
  when the shared store errors instead of allowing the request.
- Docs: `docs/RATE_LIMIT.md`, `docs/AUDIT_LOG.md`.

## Phase 4: OAuth and SaaS (`ee/` only)

- Login code binding: add `binding_hash` to `oauth_login_codes` (postgres
  migration). Keep the binding cookie until `Complete`, widen its path to
  cover `/api/v1/auth/oauth/complete`, require a match and clear it there.
- Redirect sanitizer: `safeRedirect` rejects any target starting with `//`;
  add cases for `///host`, `////host` and `/a/..//host`.
- Key separation: derive the OAuth state key from `JWT_KEY` with HKDF and a
  purpose label. In-flight states fail once at deploy; they live at most
  10 minutes.
- JWT `iss`/`aud`: set at issue in this phase, enforce one release later.
- Docs: `docs/SAAS_ONBOARDING.md`.

## Phase 5: Transport and server hardening

- CORS: `AllowedDomains` restricted to the origin of `PUBLIC_BASE_URL`; with
  it unset, no cross-origin access.
- HTTP timeouts: `ReadHeaderTimeout` and `IdleTimeout` on the `http.Server`
  in `internal/serverapp/run.go`. No full `ReadTimeout`: it would break long
  chunk uploads and gRPC streams.
- Metrics and pprof: optional separate listen address (loopback by default
  when set); update `docs/OBSERVABILITY.md`, `config.yaml.sample` and
  `deploy/nipad-ee.env.sample`.
- Client: `nipa login` warns when credentials go to a non-loopback host
  without `https://`.
- Docs: `docs/DEPLOY.md` states that the free `nipad` needs TLS termination
  in front of it.

## Phase 6: New features (optional)

- Sessions: "sign out everywhere" and a session list on the profile page.
  Built on phases 1 and 2: bump `token_version` and delete the families.
- MFA: TOTP first, then WebAuthn, required for global admins. Needs its own
  design document, since it touches the CLI login, SSH key login and the
  OAuth flows.

Decisions taken on 2026-11-13:

- Sessions are not started. When they are: each session shows device (user
  agent), client IP, login method, start and last-active time and marks the
  current one, which needs new `refresh_tokens` columns. Actions: sign out
  everywhere, sign out other sessions, revoke a single session (its access
  token can live up to 30 minutes longer).
- MFA: design in `docs/MFA.md`, no code yet. The first version is opt-in TOTP
  with recovery codes for everyone; requiring it for global admins comes
  later.

## Order and size

| Phase | Depends on | Rough size |
|-------|------------|------------|
| 1 | none | 1 day |
| 2 | none | 1 day |
| 3 | none (audit actions shared with 2) | 1 day |
| 4 | none | half a day |
| 5 | none | half a day |
| 6 | 1 and 2 | open |

## Not covered by the review

Per-handler authorization of every RPC and REST route, organization
invitations, webhook egress, billing and the client daemon were not reviewed
and are not part of this plan.
