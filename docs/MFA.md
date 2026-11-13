# Multi-factor authentication

Design for a second login factor. An account that enrolled a TOTP
authenticator must present a 6-digit code (or a recovery code) after its
password before a session is issued.

Status: fully implemented: the server side (storage, login step, REST and
gRPC routes, emails), the CLI, the daemon, the desktop sign-in and the SPA.
The decisions taken are listed at the end.

## Scope

In the first version:

- TOTP (RFC 6238: SHA-1, 6 digits, 30 s step), compatible with the common
  authenticator apps.
- Ten single-use recovery codes.
- Opt-in for every user. Nothing is enforced.
- Switched by `MFA_ENABLED`: off by default on the free `nipad`, on by
  default on `nipad-ee`. Either edition can set it explicitly.

Later, each with its own change: WebAuthn/passkeys, required MFA for global
admins, an organization-level requirement.

## Which logins ask for the code

| Login | Second factor | Why |
| --- | --- | --- |
| Password (REST `/auth/login`, gRPC `LoginWithUsernamePassword`) | required | the factor MFA exists to protect |
| OAuth (`/auth/oauth/complete`) | required | otherwise a taken-over provider account skips it |
| SSH key (`BeginLoginSSHKey`/`CompleteLoginSSHKey`) | not asked | the key is a possession factor; it is the path for automation |
| Email verification (`/auth/verify`) | not asked | first login, nothing enrolled yet |
| Refresh (`/auth/refresh`, `LoginWithRefreshToken`) | not asked | continues a session that already passed |
| Project service tokens | not asked | not a user login; bound to scopes and routes |

## Storage

New migration pair per dialect (sqlite and postgres).

`user_totp`: one row per user.

| Column | Notes |
| --- | --- |
| `user_id` | primary key, `REFERENCES users(id) ON DELETE CASCADE` |
| `secret_enc` | 20-byte secret, AES-256-GCM, nonce prepended |
| `confirmed_at` | NULL until the first code was accepted; an unconfirmed row never gates a login |
| `last_step` | last accepted time step; a code for a step at or below it is rejected (replay) |
| `created_at` | |

`mfa_recovery_codes`: `user_id`, `code_hash` (SHA-256, primary key with the
user), `used_at`. Codes are 10 characters from an unambiguous alphabet,
shown once as `XXXXX-XXXXX`.

`mfa_login_challenges`: `token_hash` (primary key, SHA-256 of a 32-byte
random token), `user_id`, `method` (`password` or `oauth`), `attempts`,
`expires_at` (5 minutes). Expired rows are swept on insert, like
`ssh_login_challenges`.

The encryption key is derived with HKDF-SHA256 (label `nipa mfa totp v1`)
from `MFA_ENCRYPTION_KEY`; the owner's id is the GCM additional data, so a
secret copied to another row does not decrypt. There is no fallback to `JWT_KEY`, so rotating the
JWT key never touches enrolled users.

`MFA_ENCRYPTION_KEY` is base64 (standard or URL-safe) and must decode to at
least 32 bytes (`openssl rand -base64 32`); anything else is a startup error
while MFA is on for that edition (with it off the key is not checked).
Unset means MFA is unavailable on that server:

- Enrollment (`POST /me/mfa/totp`) answers `503 two-factor authentication is
  not configured on this server`. `GET /me/mfa` answers `available: false`,
  which the SPA uses to hide the card.
- If confirmed `user_totp` rows exist, startup logs an error naming their
  count. The server still starts, so admins can reset users and log in
  themselves.
- An enrolled user whose first factor succeeds gets the same 503 instead of a
  challenge. The password alone never issues a session.
- A key that no longer decrypts a secret (it was changed) fails GCM
  authentication: the verify step answers 500, logs the user id, and the only
  way back is an admin reset. Changing the key is not supported in this
  version.

With `MFA_ENABLED` off the key is not read at all and the feature is off:

- `GET /me/mfa` answers `available: false` and enrollment answers `503
  two-factor authentication is disabled on this server`.
- Logins never ask for a code, including accounts that enrolled while it was
  on. Startup logs a warning with their count. Their rows stay, so turning it
  back on asks them again; an account can still remove its own enrollment
  with a recovery code, and an admin reset works as usual.

`refresh_tokens` gains `auth_method` (`password`, `ssh_key`, `oauth`,
`email_verification`) and `mfa` (`totp`, `recovery_code` or NULL). The session
list planned in `docs/plans/AUTH_HARDENING.md` shows them, and the access
token carries them as the `amr` claim so a later "admins must use MFA" rule
has something to check.

## Login protocol

1. The first factor succeeds (password compared, or OAuth code consumed).
2. No confirmed `user_totp` row: the session is issued as today.
3. Otherwise no session is issued. The server stores a challenge and answers
   with its token:
   - REST: `200 {"mfa_required": true, "mfa_challenge": "...", "expires_in": 300}`
     and no refresh cookie.
   - gRPC: see compatibility below; the challenge lifetime travels in
     `expires_in`.
4. The client sends the challenge with a code:
   `POST /api/v1/auth/mfa/verify {challenge, code}` or the new RPC
   `CompleteLoginMFA`. `code` is a TOTP code or a recovery code.
5. The server loads the challenge, counts the attempt, and checks the code
   for the current step and one step either side. On success it deletes the
   challenge, records `last_step` (or marks the recovery code used) in the
   same transaction, and returns the usual login result through
   `Auth.LoginResultVia`.

Failures answer the generic `400 invalid code`. The fifth failed attempt
deletes the challenge, so the password has to be entered again. Both RPCs are
public methods and the REST route sits on the `/api/v1/auth` WebService, so
the `auth_ip` limit applies; the verify step also spends `auth_id` keyed on
`mfa:<user id>`.

Throttling and the dummy password comparison stay in front of step 1, so the
response still does not reveal whether an email exists. Whether an account has
MFA is only revealed to someone who already knows its password.

### Compatibility with older clients

The SPA ships with the server, so REST has no version problem. The CLI,
daemon and desktop app do.

- `LoginUsernamePasswordRequest` gets `supports_mfa`. New clients set it.
- With the flag, an enrolled account gets `LoginResponse` with empty tokens
  and the new `mfa_challenge` field.
- Without the flag, an enrolled account gets `FailedPrecondition` with
  "this account uses two-factor authentication; upgrade nipa or log in with
  an SSH key". An old client never stores an empty session.
- Accounts without MFA see no change.

## Enrollment and management (REST)

All under `/api/v1/me/mfa`, authenticated.

| Route | Behaviour |
| --- | --- |
| `GET /me/mfa` | `{available, totp: {enabled, confirmed_at}, recovery_codes_left}` |
| `POST /me/mfa/totp` | creates an unconfirmed row, returns the secret and an `otpauth://` URI; replaces an earlier unconfirmed row; 409 when already enabled |
| `POST /me/mfa/totp/confirm {code}` | first valid code enables MFA and returns the ten recovery codes, once |
| `DELETE /me/mfa/totp {code}` | needs a valid TOTP or recovery code; deletes the secret and the recovery codes |
| `POST /me/mfa/recovery-codes {code}` | replaces all recovery codes, returns the new set once |

Starting enrollment asks for the current password; a passwordless (OAuth-only)
account must have signed in within the last 10 minutes instead.

The SPA account settings have a Security tab (`/settings/profile?tab=security`)
with the "Two-factor authentication" card, hidden when `available` is false.
Enrollment asks the current password, then shows a QR code rendered in the
browser from the URI (the `qrcode` package; the secret is never sent to a
third party) plus the secret for manual entry, then takes the first code and
shows the ten recovery codes once with Copy, Download `.txt` and a required
"I saved these codes" checkbox. The card also disables (with a code),
regenerates recovery codes (with a code, shown the same way) and shows how
many recovery codes are left. The login page gains an "Authentication code"
step after the password, and the OAuth callback page does the same when
`complete` answers `mfa_required`. Every failure is `invalid code`, so the
SPA tracks the five attempts and the challenge lifetime itself and sends the
user back to the password after either runs out. Admins get "Reset
two-factor" on the user administration page.

## Recovery

- A recovery code works wherever a TOTP code does and is consumed.
- A global admin can reset MFA for a user they can manage
  (`DELETE /api/v1/users/{id}/mfa`, same rule as `requireCanManage`). On SaaS
  that is a super admin, reached through support.
- An admin password reset does not remove MFA.
- No email-based reset in the first version: it would make the mailbox a
  bypass for the second factor.

## Clients

- `nipa login`: after the password, prompts `Authentication code:`. Also
  `--otp <code>` and `NIPA_OTP` for scripts that can supply one.
- The implicit login behind other commands and `Session.reauth` use the same
  prompt. Without a TTY they fail with a hint to run `nipa login` or use an
  SSH key.
- Daemon: `LoginRequest` gets `otp` and `mfa_challenge`; `LoginResponse`
  gets `mfa_required`, `mfa_challenge` and `expires_in` so the desktop app can
  show a code step and call again with the challenge and the code. The daemon
  never prompts.
- Desktop: the sign-in dialog adds the code step.
- `nipa mcp` never prompts; it reports the same hint.
- Automation should use SSH keys or service tokens. Neither changes.

## Audit and notifications

New actions: `user.mfa.enabled`, `user.mfa.disabled`,
`user.mfa.recovery_codes_regenerated`, `user.mfa.reset_by_admin`.
`auth.login.succeeded` gains `mfa` in its metadata; a wrong code records
`auth.login.failed` with reason `mfa_code`. Secrets and codes are never
written.

An email goes to the account when MFA is enabled, disabled or reset by an
admin and when a recovery code is used, through the existing outbox (event
`user.mfa`). These are security notices and ignore the notification
opt-out. They link to `/settings/profile?tab=security`.

## Deploy notes

`MFA_ENCRYPTION_KEY` goes into `deploy/nipad-ee.env.sample` and the server
guide, next to `JWT_KEY`, with a note that losing or changing it disables
every enrolled user's second factor. Both colors must use the same value.

The migrations only add tables and nullable columns. During the flip that
first ships MFA the old color does not know about it: someone who enrolls in
that window can still be logged in by the old color with the password alone
until it drains. The window is minutes and applies to that one release.

## Security notes

- Secrets are encrypted at rest; recovery codes and challenge tokens are
  stored as hashes.
- `last_step` makes a code single-use, so a code seen over a shoulder or in a
  proxy log cannot be replayed inside its 90 second acceptance window.
- Five attempts per challenge and the `auth_id` limit bound guessing at a few
  codes per password entry out of one million.
- Server clock drift beyond one step breaks every login for enrolled users;
  the server guide should say NTP is required.
- A session that existed before enrollment stays valid. Enabling MFA offers
  "sign out other sessions" once the session work exists.
- The enable, disable and recovery-code emails are the owner's signal that
  someone else changed the second factor.

## Testing

Unit tests for the TOTP check (RFC 6238 vectors, the step window, replay),
recovery codes, the challenge life cycle and attempt cap, enrollment and
disable rules, the old-client refusal, and every missing or wrong
`MFA_ENCRYPTION_KEY` case. Repository tests in both dialects.
REST and gRPC handler tests. Client tests for the prompt, `--otp`, the
non-interactive failure and the daemon flow. SPA tests for the card and the
login step. One e2e spec per edition: enroll, log in with a code, reuse the
code (refused), log in with a recovery code, disable.

## Suggested order

1. Server core: storage, TOTP, challenges, REST and gRPC login step,
   management routes, audit.
2. SPA: profile card, login and OAuth callback steps.
3. CLI, daemon and desktop.
4. Emails, docs, e2e.

## Decisions

1. **OAuth logins ask for the code.** Trusting the provider would let a
   taken-over Google or GitHub account skip the second factor.
2. **`MFA_ENCRYPTION_KEY` is required.** There is no fallback to `JWT_KEY`.
   Without the key, MFA is unavailable (see Storage). This costs one config
   step but keeps `JWT_KEY` rotation safe.
3. **SSH key logins skip the code.** The key is the possession factor, and
   unattended use keeps working.
4. **Other sessions survive enrollment.** "Sign out other sessions" waits for
   the session list in `docs/plans/AUTH_HARDENING.md`.
5. **Notification emails ship in the first version.**
6. **`MFA_ENABLED` switches the feature.** Off by default on the free
   edition, on by default on the enterprise one. Turning it off stops asking
   enrolled accounts for their code; it does not delete their enrollment.
