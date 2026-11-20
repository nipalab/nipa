# Authentication internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

Flow for session revocation: `users.token_version` (both dialects) is bumped
inside the statement that changes the account: `UserUpdatePassword`
(`UpdatePasswordAndRevokeTokens`, `SetPasswordHash`,
`ResetUnverifiedCredentials`), `UserUpdateEmail`, `UserDeleteByID`
(deactivate) and `UserUpdateAdminFlags` (only when a flag changes).
`Auth.LoginResult` signs it as the `tv` claim (absent = 0, so older tokens
stay valid until a bump) next to `iss` `nipad` / `aud` `nipa-api`
(`domain.TokenIssuer`/`TokenAudience`), which `ValidateToken` requires
(`jwt.WithIssuer`/`WithAudience`; a token without them is a 401, so tokens
from a binary older than #210 are refreshed once). `Auth.ValidateToken`, the only access-token check
(REST `AuthFilter`, gRPC `JWTUnary`/`JWTStream`), loads the user by id on
every request (no cache): a missing (deleted) user or a `tv` mismatch is a 401
`invalid token`, and `IsAdmin`/`IsSuperAdmin` are overwritten from the row, so
admin gates never trust the token's flags. A lookup failure that is not "not
found" passes through as a 5xx error (`domain.IsServerError`): REST answers
500 and gRPC `Internal`, so a database blip never triggers a client token
refresh. `POST /me/password` revokes the caller too and answers with a fresh
`LoginResponse` + refresh cookie that the SPA installs. Service tokens keep
their own per-request creator check.

Flow for refresh sessions (`internal/usecase/auth_refresh.go`, both
dialects: sqlite migration `000020`, postgres `000004`): one `refresh_tokens`
row per session, rotated in place. The wire token is
`<id base36>.<tag>.<secret>`: `tag` is a 16-byte HMAC-SHA256 of the id under
the JWT key (so a session id alone, being sequential, is never enough to touch
a session; a bad tag is "not found"), `secret` is 32 random bytes in URL-safe
base64, and the row keeps only `token_hash` (hex SHA-256 of the secret),
`previous_hash`, `rotated_at`, `session_started_at` and `expires_at`.
`Auth.LoginWithRefreshToken` (REST and gRPC share it): the current secret
rotates through a conditional `UPDATE ... WHERE token_hash = current`
(`RotateRefreshSession`; zero rows = another request won = "not found"),
keeping the id and `session_started_at` and renewing 60 days; the previous
secret within `refreshGraceWindow` (30 s) is "not found" (the REST handler
keeps the cookie, a racing tab retries); any other secret, or the previous
one after the grace, deletes the session and records
`auth.refresh_token.reuse_detected` (system actor, `auditedWrite` committed
before the 401 `refresh token revoked`); expiry or `refreshSessionMaxAge`
(90 days from `session_started_at`) deletes it with 400 `refresh token
expired`. `WithClock` drives grace/cap in tests. `Logout` deletes only when
the secret matches the current or previous hash. Only hashes are stored:
migrations sqlite `000021` / postgres `000006` (`refresh_token_drop_plaintext`)
deleted the pre-session plaintext rows and dropped the `token` column
(`token_hash` is `NOT NULL UNIQUE`; the sqlite table rebuild carries
`sqlite_sequence` over so session ids are never reused), so a token without
the session shape or with a bad tag is "not found" and a binary older than
#205 cannot run against this schema. Client: `Session.Refresh`
reloads the shared token store when the server rejects a refresh and uses the
stored pair when another `nipa`/`nipa serve`/`nipa mcp` process rotated it
first, but only for the same session (`<id>.<tag>` prefix) or the same stored
email, since the store is keyed by host (a 404 polls up to 5 x 100 ms for that process to save) before falling
back to `reauth`.

Flow for SSH key login (design in `docs/SSH.md`: `nipa login`, the implicit
login behind every command, the profile page card): an SSH key is an alternate
credential that bootstraps the normal JWT session over the same gRPC transport;
the private key never leaves the client. Public keys live in `ssh_public_keys`
(name, type, blob, globally unique `SHA256:` fingerprint, comment; max 10 per
user) and are managed over REST only (`GET/POST /api/v1/keys`,
`DELETE /api/v1/keys/{id}`, rendered by the SPA profile "SSH keys" card);
`internal/sshkey` parses authorized_keys lines (ed25519/ecdsa/RSA ≥ 2048, no
DSA or certificates) and verifies signatures. Login is a two-RPC
challenge–response: `BeginLoginSSHKey(email?)` stores a 32-byte nonce (60 s TTL,
single-use, expired rows swept on insert) in `ssh_login_challenges`, the client
signs `nipa-login:v2:<origin>:<nonce>` (origin = `sshkey.CanonicalOrigin`: lowercase host
without trailing dot, port only when not 80/443, no scheme or path) and
`CompleteSSHKeyLogin` consumes the challenge,
resolves the account from the key fingerprint (key-only, git-style) or checks
the optional email against the key owner, verifies and returns the usual
access/refresh pair; every failure is the generic 400
`invalid email or SSH key` and neither call reveals whether an account exists.
The server's expected origin comes from config, never from the request:
`PUBLIC_BASE_URL`, else the host of `EMAIL_BASE_URL` (`Config.SSHLoginAudience`,
`Auth.WithSSHAudience`); with neither, both RPCs answer 400 `SSH key login is not
enabled` and startup logs a warning. `BeginLoginSSHKey` returns it as `audience`
and the client refuses to sign unless it equals the canonical origin of the
address it dialed, so a hostile server can neither obtain nor relay a usable
signature; v1 (`nipa-login:v1:`) is gone and RSA must sign `rsa-sha2-256/512`
(`ssh-rsa` SHA-1 is rejected). Both RPCs are in the interceptor's public list. Client-side
`internal/client/sshkey.Resolve` resolves signers in order: `--ssh-key`,
`NIPA_SSH_KEY`, config `sshKeyPath`, ssh-agent keys, then
`~/.ssh/id_ed25519|id_ecdsa|id_rsa` (configured keys fail loudly with a
passphrase prompt, auto-detected keys are skipped); `nipa login` tries SSH
before any prompt and falls back to the password flow (flag/env failures are
hard errors), `MakeSureLoggedIn` does the same for every command, and
`Session` re-authenticates with the key — email-bound when the stored session
has one, key-only otherwise. Covered by unit tests plus the
`tests/suite/ssh_test.go` e2e specs (key-only login, lazy clone login through
`NIPA_SSH_KEY`, unregistered-key rejection, username binding).

Flow for two-factor authentication (design in `docs/MFA.md`): the pure `internal/totp` implements RFC 6238 (`Code`/`Match`
with one step of skew either side, skipping steps at or below `last_step`),
recovery codes (`NewRecoveryCodes` `XXXXX-XXXXX` from an unambiguous
alphabet, `NormalizeRecoveryCode`, SHA-256 `HashRecoveryCode`), the
`otpauth://` `URI` and `Cipher` (over the pure `internal/sealbox`:
AES-256-GCM, key = HKDF-SHA256 of `MFA_ENCRYPTION_KEY`, label `nipa mfa
totp v1`, the owner id as AAD).
`MFA_ENABLED` switches the feature (`Config.MFAEnabledOr`, unset = the
edition default `UsecaseDeps.MFAEnabledByDefault`: false on `nipad`, true on
`nipad-ee`); off means `MFA.WithEnabled(false)` (`available: false`,
enrollment 503 "disabled"), `Auth` gets no `WithMFA` so logins never ask, and
the key is not read. `Config.MFAKey` decodes the key (base64, >= 32 bytes;
both mains call `ValidateMFAKey(mfaEnabledByDefault)`, which fails startup on
a bad key only while MFA is on); `serverapp.mfaCipher` leaves the cipher nil when it is
unset or invalid, which makes MFA unavailable (enrollment 503, enrolled
password/OAuth logins 503, recovery codes still disable). Startup logs
enrolled accounts the server cannot ask (`LogUnusableEnrollments`: a warning
when disabled, an error without a key). Storage (sqlite `000022`,
postgres `000007`): `user_totp` (one row per user, `secret_enc`,
`confirmed_at` NULL until the first code, `last_step`), `mfa_recovery_codes`
(PK user + hash, `used_at`), `mfa_login_challenges` (PK SHA-256 of the
token, `method`, `attempts`, 5 min `expires_at`, swept on insert) and
`refresh_tokens.auth_method`/`mfa`; `sqlite|postgres.NewMFARepository`
(`Repositories.MFA`), multi-statement writes join the caller's transaction.
`usecase.MFA` (`Status`, `StartTOTP` with the current password or, for a
passwordless account, an `auth_time` claim under 10 min, `ConfirmTOTP`
returning the ten codes once, `DisableTOTP`, `RegenerateRecoveryCodes`,
`AdminReset` under `requireCanManageUser`) audits `user.mfa.*` through
`auditedWrite` and notifies the owner through `MFAMailer` (outbox event
`user.mfa`, ignores the email opt-out). `Auth.LoginWithFirstFactor`
(password login and OAuth `complete`) returns `LoginResult.MFAChallenge`
instead of tokens for a confirmed TOTP row; `Auth.CompleteLoginMFA` counts
the attempt (fifth failure deletes the challenge), spends `auth_id`
`mfa:<id>`, consumes the TOTP step (`AdvanceTOTPStep` is conditional, so
replay fails) or the recovery code and the challenge in one transaction,
and audits `auth.login.failed` reason `mfa_code` or `auth.login.succeeded`
with `mfa`. Sessions record `auth_method`/`mfa`; access tokens carry them
as `amr` plus `auth_time` (session start), kept across refreshes and the
password-change reissue. REST: `/auth/login` and `/auth/oauth/complete`
answer `{mfa_required, mfa_challenge, expires_in}` without a cookie
(`handler.WriteLoginResult`), `POST /auth/mfa/verify`, `/me/mfa`
(`GET`, `POST totp`, `POST totp/confirm`, `DELETE totp`, `POST
recovery-codes`) and `DELETE /users/{id}/mfa`. gRPC:
`LoginUsernamePasswordRequest.supports_mfa`, `LoginResponse.mfa_challenge`
(TTL in `expires_in`) and the public `CompleteLoginMFA`; without the flag an
enrolled account gets `FailedPrecondition` with the upgrade hint. 503
crosses gRPC as `FailedPrecondition`. SSH key, refresh, email verification
and service tokens never ask. Client: the transport always sends
`supports_mfa` and returns the challenge in `LoginResult.MFAChallenge`
(never stored); `usecase/login_mfa.go` takes the code from `nipa login
--otp`, then `NIPA_OTP`, then `OTPPrompter.PromptOTP` (`Authentication
code:`, up to 3 prompts on a 400, only on a TTY) for `nipa login`,
`MakeSureLoggedIn` and `Session.reauth`; without a source it fails with
`MFARequiredError` (`LoginHint`, also used by `nipa mcp`). The daemon never
prompts: `daemon.PasswordLogin` over `Auth.StartPasswordLogin`/
`CompleteLoginMFA` answers `LoginResponse.mfa_required` + `mfa_challenge`,
and a second `Login` with `mfa_challenge` + `otp` finishes it; the desktop
`SignIn` returns `SignInResult` and `SignInMFA` drives the clone dialog's
code step. SPA: `startSession` (login, OAuth `complete`) returns the
challenge and `components/MFACodeStep.tsx` posts `/auth/mfa/verify`, counting
wrong codes and the TTL locally (every failure is `invalid code`) before
sending the user back to the password; the Security tab
(`/settings/profile?tab=security`, `TwoFactorCard.tsx`, hidden when
`available` is false) enrolls with a locally rendered `qrcode` QR, shows
recovery codes once behind a required "I saved these codes" checkbox
(`RecoveryCodes.tsx`), disables and regenerates; `/admin/users` has
"Reset two-factor".

