# Audit log

Both editions (`nipad` on SQLite, `nipad-ee` on PostgreSQL) record who changed
what in an append-only `audit_log` table, readable by super admins over REST.
Reads are never audited.

## Entry model

`domain.AuditEntry` (`internal/domain/audit.go`):

| Field | Meaning |
| --- | --- |
| `id` | snowflake id; also the keyset cursor (newest first) |
| `occurred_at` | server time (UTC) |
| `actor_type` | `user` (an authenticated caller), `provider` (e.g. a billing provider webhook) or `system` (no caller: SaaS signup, OAuth signup) |
| `actor_user_id` | the caller's user id for `user` entries |
| `actor_label` | free label for non-user actors (e.g. `paddle`) |
| `action` | dotted action name from the catalog below |
| `org_id`, `project_id` | scope; project-scoped entries get their `org_id` resolved from the project |
| `target_type`, `target_id` | what was changed (`user`, `org`, `group`, `access_rule`, `path_permission`, `branch`, `lock`, `project`, `webhook`, `subscription`, `billing_event`) |
| `metadata` | JSON object with the old/new values of the change |
| `request_id`, `client_ip`, `user_agent` | request correlation and caller network identity |

The table has **no foreign keys**: entries outlive the users, organizations
and projects they mention. Indexes: `(id DESC)`, `(actor_user_id, id DESC)`,
`(org_id, id DESC)`, `(project_id, id DESC)`, `(action, id DESC)`.

Metadata never holds secrets: no passwords or password hashes, no tokens, no
webhook secrets. Webhook URLs are recorded without query string, fragment or
credentials, since those can carry tokens.

## Action catalog

Constants live in `internal/domain/audit_actions.go`.

| Action | Recorded by | Metadata |
| --- | --- | --- |
| `org.created` | `Org.Create` | `name`, `slug` |
| `org.member.added` | `Org.AddMember` / `UpdateMemberRole` for a new member | `role` |
| `org.member.role_changed` | `Org.AddMember` / `UpdateMemberRole` | `old_role`, `new_role` (skipped when unchanged) |
| `org.member.removed` | `Org.RemoveMember` | `role` (the role before removal) |
| `group.created` | `Group.Create` | `name` |
| `group.member.added` / `group.member.removed` | `Group.AddMember` / `RemoveMember` | `group_name`, `user_id` |
| `access.rule.created` / `access.rule.deleted` | `Permission.CreateRule` / `DeleteRule` | the full rule: `rule_id`, `user_id` or `group_id`, `path_prefix`, `permission` |
| `access.path_permission.set` | `Permission.SetPathPermission` | `path_prefix`, `old_permission` (absent when new), `new_permission` |
| `access.path_permission.deleted` | `Permission.DeletePathPermission` | `path_prefix`, `old_permission` |
| `branch.protection.changed` | `Branch.SetProtection` (skipped on the no-change return) | `branch`, `old`, `new` (protected, approvals, stale dismissal, status checks, reviewers, checks) |
| `lock.force_released` | `FileLock.Release` when the caller is not the holder | `path`, `holder_id`, `branch` (absent for mainline locks) |
| `user.created` | `User.Create` (admin), `ee/registration`, `ee/oauth` (actor `system`) | `name`, `email`, `source` (`admin`, `registration`, `oauth:<provider>`) |
| `user.admin_flags.changed` | `User.SetAdminFlags` (skipped when unchanged) | `old`, `new` (`is_admin`, `is_superadmin`) |
| `user.deactivated` | `User.Deactivate` | none |
| `user.password.reset_by_admin` | `User.ResetPassword` | none |
| `user.password.changed` | `User.ChangePassword` (own password, after the old one matched) | none |
| `user.email.changed` | `User.UpdateEmail` | `old_email`, `new_email` |
| `user.terms.accepted` | `registration.AcceptTerms` (`nipad-ee`, SaaS) | `version`, `source` |
| `auth.login.succeeded` | `Auth.LoginResultVia`: password and SSH key logins, `ee/registration` verify, `ee/oauth` complete (actor = the user) | target = user; `method` (`password`, `ssh_key`, `email_verification`, `oauth`) |
| `auth.login.failed` | `Auth.LoginWithEmailPassword` / `CompleteSSHKeyLogin` refusals that passed the throttle (actor `system`) | target = user when the account exists, else empty; `method`, `email` (normalized, may be empty for key-only SSH), `reason` (`invalid_credentials`, `no_password`, `email_not_verified`, `invalid_challenge`, `invalid_key`, `unknown_key`, `key_mismatch`, `invalid_signature`) |
| `auth.refresh_token.reuse_detected` | `Auth.LoginWithRefreshToken` when a refresh session is presented with a stale secret (actor `system`; the session is deleted) | target = user; `session_id` (base36 row id), `reason` (`previous_secret_after_grace`, `unknown_secret`); never a secret or hash |
| `project.created` | `Project.Create` | `name`, `slug`, `description` |
| `project.updated` | `Project.Update` (skipped when unchanged) | `old`, `new` (`name`, `description`) |
| `project.deleted` | `Project.Delete` | `name`, `slug` |
| `webhook.created` / `webhook.deleted` | `Webhook.Create` / `Delete` | `name`, `url`, `events`, `path_prefix`, `is_active`, `insecure_tls` |
| `webhook.updated` | `Webhook.Update` (skipped when unchanged) | `old`, `new` with the fields above |
| `webhook.secret_rotated` | `Webhook.RotateSecret` | `url`, `events` (never the secret) |
| `service_token.created` | `ServiceToken.Create` | `name`, `scopes`, `hint`, `expires_at` (never the token) |
| `service_token.revoked` | `ServiceToken.Revoke` | `name`, `scopes`, `hint` |

## Billing (enterprise)

`nipad-ee` records the billing changes in the same table. `ee/billing.Service`
and `ee/storageusage.Ledger` take the core `usecase.Audit` through `WithAudit`
(wired in `ee/cmd/nipad/main.go`; nil = auditing off). Metadata never holds
API keys, webhook secrets, Paddle client tokens or webhook payload bodies;
customer, subscription and transaction ids and item quantities are recorded.

| Action | Recorded by | Actor | Metadata |
| --- | --- | --- | --- |
| `billing.quota_override.set` | `Ledger.SetQuotaOverride` (admin `PUT .../owners/{user}/quota`; skipped when unchanged) | super admin | target = owner; `old_override_bytes` (null = none), `new_override_bytes` (0 = unlimited) |
| `billing.quota_override.cleared` | `Ledger.ClearQuotaOverride` (admin `DELETE`; skipped without an override) | super admin | target = owner; `old_override_bytes` |
| `billing.owner.transferred` | `Ledger.SetBillingOwner` (admin `PUT .../billing-owner`; skipped when unchanged) | super admin | `org_id`; `old_owner_id`, `new_owner_id` |
| `billing.checkout.started` | `Service.Checkout`, after the provider created the transaction | user | `seats`, `storage_packs`, `interval`, `transaction_id`, `customer_id` |
| `billing.subscription.changed` | `Service.ChangeSubscription` | user | target = subscription; `owner_id`, `old`/`new` (`seats`, `storage_packs`, `interval`). `old` is the plan the user changed from, read before the provider call; `new` is the provider's answer. It records who asked for the change even when a webhook applied it first (then `subscription.applied` is attributed to the provider) |
| `billing.subscription.applied` | `Service.Apply`, only when the upsert stored the update | provider, or the caller (see below) | target = subscription; `owner_id`, `old`/`new` (`subscription_id`, `status`, `seats`, `storage_packs`, `interval`, `quota_bytes`), `event_id`/`event_type` when an event carried it |
| `billing.sync` | `Service.Sync` (admin sync route) | super admin | target = owner; `synced` |
| `billing.event.replayed` | `Service.Replay` (admin replay route) | super admin | target = event; `event_id`, `event_type`, `outcome` (`processed`/`failed`), `error` |
| `billing.seat.denied` | `Service.Admit` refusal | the caller adding the user | `org_id`, target = user; `billing_owner_id`, `used`, `limit` |
| `billing.quotas.reconciled` | `Service.ReconcileQuotas` at startup, only when a quota changed (also after a partial run) | system | `updated`, `complete: false` on a partial run |

`billing.subscription.applied` is attributed to the authenticated caller when
there is one (a plan change, an admin sync or a replay; the replay keeps the
event id), and to `actor_type=provider`, `actor_label=<provider>` (`paddle`)
on the unauthenticated webhook. `old` is read from the live subscription and
the stored computed quota (`Ledger.ComputedQuota`) before the upsert, in the
same transaction; `old.quota_bytes` is null when none was stored. Duplicate
deliveries and older (skipped) provider updates write no entry, so the
webhook path stays idempotent.

Atomicity: the ledger writes run through `usecase.AuditedWrite` and `Apply`
records inside its existing transaction, so the entry and the change commit
or roll back together; `subscription.changed` shares the transaction of the
apply that follows the provider call. Checkout, sync and replay record after
the provider call (or the per-subscription applies) succeeded; a failed record
fails the request, as everywhere else. **Exception:** `billing.seat.denied` is
written after `Admit`'s transaction, on a context detached from any caller
transaction (`dbtx.Detach`), because the refusal rolls back everything in it.
A failure to write it is only logged and the 402 is still returned.

## Atomicity

An audit row commits with the change it describes. Each audited usecase holds
an optional `usecase.AuditRecorder` (`WithAudit`, nil = auditing off) and runs
its write through `auditedWrite`: the write and `Record` share one
`dbtx.Transactor.WithinTx` transaction, and a failed record fails the action and
rolls the write back. Repositories therefore join the transaction carried in the
context: every repository an audited write touches is built on
`sqlcX.New(dbtx.New(db))`, and the multi-statement writes
(`OrgRepository.CreateWithOwner`/`RemoveMember`,
`BranchRepository.SetBranchProtection`,
`ProjectRepository.CreateWithDefaultBranch`, `WebhookRepository.Delete`) use
`WithinTx` instead of their own `BeginTx`. The enterprise seat gate joins the
same transaction (its refusal entry is the one exception, see Billing).
`auth.refresh_token.reuse_detected` commits its session deletion and entry
through `auditedWrite` before the refresh call returns its 401, so the
revocation survives the failed request. The login entries are the other
exception: they are best-effort (`Auth.recordLogin`, on a context that ignores
cancellation), a failed record is only logged and never changes the login's
outcome. Throttled attempts are not audited (they show in
`nipa_ratelimit_total`), so failed-login volume is bounded by `auth_id`.
`LoginResult` (session reissue after a password change) records nothing; every
sign-in path goes through `LoginResultVia`, so each login is recorded once.

`usecase.Audit` is the recorder: it fills the actor from the JWT claim (or
`system` without one), the request id (`obs.RequestIDFromContext`), the client
IP and user agent, the snowflake id and the timestamp, then inserts through
`AuditRepository` (`internal/repository/sqlite`, `ee/repository/postgres`).
It is wired in `serverapp.BuildUsecases` for both editions; `ee/cmd/nipad`
passes it to the SaaS registration and OAuth flows.

## Request metadata

`domain.ContextWithRequestMeta` carries `{ClientIP, UserAgent}`. The REST auth
filter (`internal/http/api/auth_filter.go`) and both gRPC JWT interceptors
(`internal/grpc/server/interceptor.go`) set it on authenticated calls; the
`/api/v1/auth` WebService (`RequestMetaFilter`) and the interceptors' public
methods (login RPCs) set it too, for login throttling and login entries. The
client IP comes from `ratelimit.ClientIP`: `X-Forwarded-For` (gRPC
`x-forwarded-for` metadata) is honoured only when the direct peer is in
`RATE_LIMIT_TRUSTED_PROXIES`, the right-most untrusted hop wins; otherwise the
peer address is used. The trusted proxies apply even when rate limiting is
disabled. Other unauthenticated routes carry no request meta.

## API

`GET /api/v1/admin/audit` (both editions, super admins only; others get 403):

| Query | Meaning |
| --- | --- |
| `actor` | acting user id (base36) |
| `org`, `project` | organization / project id (base36) |
| `action` | action prefix, e.g. `org.member` or `webhook` |
| `target` | target id |
| `since`, `until` | RFC 3339 bounds on `occurred_at` (inclusive / exclusive) |
| `after` | keyset cursor: the last id of the previous page |
| `limit` | page size, default 50, max 200 |

Response: `{entries: [...], next_cursor}`; `next_cursor` is present only when
another page exists. The SPA has a typed helper (`listAuditEntries` in
`web/src/api/endpoints.ts`) but no page yet.

## Retention

Entries are kept forever in v1. A future `AUDIT_RETENTION_DAYS` sweeper can
delete rows older than the window (keyed by `occurred_at`).
