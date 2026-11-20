# Auth follow-ups

Open work left from the authentication review of 2026-11-11. Phases 1 to 5
(session revocation, refresh token hardening, login surface, OAuth and SaaS,
transport hardening) shipped in #204, #205, #208, #210 and #211; the result is
described in `AGENTS.md` (session revocation, refresh sessions, SSH key login,
SaaS onboarding, transport hardening) and `docs/MFA.md`.

## Session list

Not started. Each session shows device (user agent), client IP, login method,
start and last-active time and marks the current one, which needs new
`refresh_tokens` columns (`auth_method` and `mfa` already exist). Actions:

- sign out everywhere (bump `token_version` and delete every session),
- sign out other sessions,
- revoke a single session (its access token can live up to 30 minutes
  longer).

`docs/MFA.md` relies on it for "sign out other sessions" after enrollment.

## MFA beyond TOTP

Opt-in TOTP with recovery codes shipped (`docs/MFA.md`). Still open:

- requiring MFA for global admins (the `amr` claim is there to check),
- WebAuthn.

## Rules

- Forward compatible for one release, so both `nipad-ee` colors can run
  during a flip.
- Schema changes are new migration pairs in both dialects, then `make sqlc`.
- New code is covered at least 80% by tests.
- `AGENTS.md` and the affected `docs/` pages are updated in the same change.

## Not covered by the review

Per-handler authorization of every RPC and REST route, organization
invitations, webhook egress, billing and the client daemon were not reviewed.
