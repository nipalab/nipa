# SSH key login

Nipa supports logging in with an SSH key instead of a password. A registered
public key bootstraps the normal JWT session over the existing gRPC transport:
the private key never leaves the client, and the resulting session is
identical to a password login (same access/refresh tokens, same interceptor,
same REST filter).

Status: complete. The server key registry (REST), the challenge-response login
RPCs, the client signer resolution, `nipa login`, the profile page "SSH keys"
card and the client e2e harness are implemented.

## Model

An SSH key is an alternate credential for the same account, not a second
account. The server stores only public keys; login proves possession of the
matching private key with a single-use, short-lived challenge (a nonce) that
the client signs.

Two modes exist at login time:

- **key-only** — no identity is supplied: the key fingerprint identifies the
  account, like `git`. This is what `nipa login --ssh-key` and the implicit
  login behind every other command use.
- **email-bound** — the client supplies an email that must match the account
  that owns the key. `nipa login --username <email> --ssh-key …` uses it as an
  assertion so a flag-typed account is never silently swapped.

## Storage

`ssh_public_keys` (sqlite + postgres):

| Column | Notes |
| --- | --- |
| `id` | snow id (base36 on the wire) |
| `user_id` | owner, `REFERENCES users(id) ON DELETE CASCADE` |
| `name` | user label; defaults to the OpenSSH key comment, capped at 100 runes |
| `key_type` | `ssh-ed25519`, `ecdsa-sha2-nistp256/384/521`, `ssh-rsa` |
| `key_blob` | base64 SSH wire-format public key |
| `fingerprint` | `SHA256:…`, **globally unique** (one key, one account) |
| `comment` | the raw OpenSSH comment, stored verbatim |
| `created_at` | |

`internal/sshkey` parses `authorized_keys` lines and enforces the policy:
ed25519, ECDSA (nistp256/384/521) and RSA of at least 2048 bits; DSA,
certificates (`*-cert-v01@openssh.com`) and unknown types are rejected. A user
may register at most 10 keys.

`ssh_login_challenges` stores the login nonces: `email` (empty in key-only
mode), `nonce` (unique, 32 random bytes base64url), `expires_at` (60 s TTL)
and `created_at`. Rows are single-use — completed challenges are deleted, so a
replay fails — and expired rows are swept whenever a challenge is created.
Storing challenges in the database keeps multi-pod `nipad-ee` instances
consistent.

## Key management (REST)

Managed over the REST API only (`internal/http/api/ssh_key.go`), self-scoped
from the caller's claims:

| Route | Operation |
| --- | --- |
| `GET /api/v1/keys` | list the caller's keys |
| `POST /api/v1/keys` | `{name?, public_key}` — one `authorized_keys` line |
| `DELETE /api/v1/keys/{id}` | remove one key |

Errors: invalid or unsupported key material and the 10-key limit are `400`;
registering a fingerprint that already exists (including another account's) is
`409`. The SPA profile page renders the same API as an "SSH keys" card
(`web/src/components/SSHKeysCard.tsx` with add/remove dialogs).

## Login protocol

Two public gRPC RPCs (`internal/grpc/proto/server.proto`, handlers in
`internal/grpc/server/login.go`, both in the interceptor's public list):

1. `BeginLoginSSHKey(email?)` → `{nonce, expires_in: 60}`. The email is
   optional and is **not** resolved here, so the call cannot be used to
   enumerate accounts; an unknown email fails only at completion.
2. The client signs `sshkey.LoginPayload(nonce)` = `nipa-login:v1:<nonce>` with
   its private key and calls
   `CompleteLoginSSHKey(email?, nonce, public_key, signature)` →
   the standard `LoginResponse` (access token, refresh token, `expires_in`).

The server (`usecase.Auth.CompleteSSHKeyLogin`) consumes the challenge,
checks the TTL and that any supplied email matches the one the challenge was
created with, parses the presented public key, looks it up by fingerprint, and
resolves the account: from the key owner in key-only mode (`GetByID`) or by
the supplied email in bound mode (the key must belong to that user). It then
verifies the signature and issues tokens through the same
`generateLoginResult` path as password login. Every verification failure
(bad nonce, expired, unregistered key, foreign key, bad signature) returns the
same generic `400 invalid email or SSH key`.

## Client

`nipa login [--host] [--username] [--ssh-key]` authenticates and stores the
session token for the host. The SSH attempt runs before any prompt; if it
succeeds, no username or password is asked. Without `--ssh-key`, keys are
resolved by `internal/client/sshkey.Resolve` in this order:

1. `--ssh-key <path>`
2. `NIPA_SSH_KEY`
3. `sshKeyPath` in `~/.config/nipa/config.json`
4. every key of a running ssh-agent (`SSH_AUTH_SOCK`)
5. `~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`

A configured key (1–3) that cannot be loaded is a hard error — including
encrypted keys, which prompt for the passphrase. Auto-detected keys (4–5)
that cannot be loaded are skipped silently. Multiple candidates are tried in
order: a rejected key moves on to the next, transport failures stop the
attempt.

When no SSH credential resolves or all are rejected, `nipa login` falls back
to the interactive password flow. If `--ssh-key` or `NIPA_SSH_KEY` was given,
the SSH failure is returned instead (no silent password prompt), which keeps
CI deterministic. `--username` passed together with a key binds the login to
that account and also skips the username prompt in the password fallback.

Every other command logs in implicitly (`MakeSureLoggedIn`): it tries the
resolved SSH keys first with no username and only then prompts for
email/password, so a clone with a registered key needs no input at all. The
session layer (`usecase.Session`) re-authenticates with the key when token
refresh is rejected, reusing the stored email when the session has one and
key-only mode otherwise. `NIPA_TOKEN_FILE` makes the token store file-backed
for scripting and CI.

## Security notes

- Private keys never leave the client; only public blobs and annotations are
  stored server-side.
- Challenges are single-use and expire after 60 seconds; a captured signature
  is useless after consumption or expiry.
- `BeginLoginSSHKey` accepts any email without revealing whether the account
  exists; all completion failures share one generic error.
- The fingerprint is globally unique, so a key cannot be registered to two
  accounts and a login always resolves exactly one owner.
- The signed payload carries the `nipa-login:v1:` domain prefix to prevent
  cross-protocol signature reuse.
- Weak or unsupported key material (DSA, RSA < 2048 bits, certificates) is
  rejected at registration and again at login.

## Testing

- Unit coverage: `internal/sshkey` (parse/fingerprint/verify/policy),
  `usecase.Auth` (challenge TTL, single use, key-only and bound modes, all
  failure branches), `internal/repository/{sqlite,postgres}` challenge
  lifecycle, gRPC handlers, client signer resolution (`internal/client/sshkey`,
  including an in-process ssh-agent), client auth/session flows and the
  `nipa login` command.
- Client e2e (`tests/suite/ssh_test.go`, both editions): registering a key over
  REST, key-only `nipa login` without a username, lazy sign-in for `nipa clone`
  through `NIPA_SSH_KEY`, rejection of an unregistered key, and username
  binding.
