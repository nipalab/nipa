# SaaS, email, storage quota and billing internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

Flow for email notifications (design in `docs/EMAIL.md`): `internal/mail` is
the sender layer (`EMAIL_SENDER` = `smtp|sendgrid|scaleway|http|log|off`), one message
per recipient is persisted in the `email_deliveries` outbox and
`mail.Dispatcher` claims due rows, retries with exponential backoff
(`EMAIL_MAX_ATTEMPTS`/`EMAIL_RETRY_BACKOFF_SECONDS`/`EMAIL_POLL_SECONDS`) and
sweeps expired rows; both mains build the sender + dispatcher from
`internal/config` (an `off` sender leaves the notification seam unwired).
`usecase.EmailNotifier` is attached through `HookEmitter.WithNotifier`: on the
MR events (created/ready/review-requested/review-submitted/comment/
synchronized/merged/closed/reopened and failed `mr.check_reported`, carried by
`EmitMergeRequestCheck`) it resolves the participants (author, requested
reviewers, assignees, commenters), skips the actor, deleted users, invalid
addresses and `users.notify_email = false`, and enqueues one rendered message
per recipient (deep link from `EMAIL_BASE_URL`; the first message to a
recipient uses a per-recipient thread `Message-ID`, later messages reference
it with `In-Reply-To`/`References` via the `thread_key` column). The
dispatcher is kicked after each enqueue. Project admins inspect and redeliver
through `usecase.EmailDelivery`: `GET .../emails/deliveries`
(`state`/`after`/`limit` keyset, `next_cursor`) and
`POST .../emails/deliveries/{id}/redeliver` (delivered/failed only), surfaced
by the SPA project settings "Email deliveries" tab. The harness runs `nipad`
with `EMAIL_SENDER` set to `sendgrid`, `scaleway` or `http` (via
`NIPA_TEST_EMAIL_TRANSPORT`, one transport per edition in CI) pointed at an
in-suite receiver (and 1s retry/poll knobs) and covers opt-out, participant
emails, thread replies, synchronized/check/merge events, failed retries and
redelivery.

Flow for SaaS onboarding (see docs/SAAS_ONBOARDING.md; `nipad-ee` only, gated by
`SAAS_MODE=1`, free edition answers 404): `ee/registration` serves
`POST /api/v1/auth/register` `{name,email}` (creates an inactive, password-less
`email_verified=false` account via `RegistrationRepository.CreateUnverifiedUser`, seeds the free-tier
`owner_quota` from `SAAS_FREE_QUOTA_BYTES`, stores a 72 h one-time token in
`email_verifications` and queues the confirmation email in the existing outbox
using `EMAIL_BASE_URL`), `POST /api/v1/auth/register/resend` (always 200) and
`POST /api/v1/auth/verify` `{token,password}` (the mailbox owner chooses the
password here, so a squatter's registration can never set one; activates the
account, consumes the token and returns the standard login result via the exported `usecase.Auth.LoginResultVia`, which records `auth.login.succeeded`; OAuth `complete` does the same with method `oauth`).
Login gates live in `internal/usecase/auth.go`: password, refresh-token and SSH
logins refuse unverified accounts with a 403 `email not confirmed`. Terms
acceptance is recorded against the version the website publishes
(`SAAS_TERMS_URL`, nipa-web `/legal.json` from `LEGAL.version`, refreshed by
`registration.TermsRefreshJob`; `SAAS_TERMS_VERSION` is the fallback, neither =
off) in
`user_terms_acceptances` (postgres migration `000002`): `register` requires
`accept_terms`, `oauth/{provider}/start?accept_terms=1` carries the version in
the signed state, and `GET/POST /api/v1/me/terms` (`ee/registration/terms.go`)
back the SPA `TermsGate` in `AppShell` that asks accounts without an
acceptance once (`user.terms.accepted` audit). `ee/oauth`
serves the social flow: signed-HMAC state (key = HKDF-SHA256 of the JWT key,
label `nipa oauth state v1`; relative redirects only, anything starting with
`//` or holding empty/`.`/`..` segments falls back to `/`)
through `GET /api/v1/auth/oauth/{provider}/start`, Google userinfo / GitHub
`/user` + verified-email exchange in the callback, account resolution (linked
identity → verified-email match — an unconfirmed account is activated — →
`oauth_error=email_conflict|email_required` → new verified account with free
quota), a one-time `oauth_login_codes` handoff (token never in a URL) and
`POST /api/v1/auth/oauth/complete`; authenticated `GET/DELETE
/api/v1/me/identities` plus `GET /api/v1/me/identities/{provider}/start`
(returns `{url}`) manage links, refusing to unlink the last login method of a
passwordless account (409). Both OAuth flows bind the callback to the starting browser: `Start` sets an
HttpOnly `nipa_oauth_binding` cookie (Lax, path `/api/v1/auth/oauth`) whose hash is
in the signed state, and `Callback` refuses a state without the matching cookie.
A login code stores that hash too (`oauth_login_codes.binding_hash`, postgres
migration `000005`): the callback keeps the cookie only when it issued a code
(link flow and errors clear it), and `Complete` consumes the code, requires
the cookie to match (constant time, no stored hash = invalid, so codes from the
previous color fail once at deploy) and always clears the cookie, so a code
handed to another browser cannot sign it in (login CSRF).
Linking an existing unconfirmed account clears its password, pending tokens and
sessions first. Registering an address that already has an account always looks
like a success (an unconfirmed one is just re-issued a token, a confirmed one
gets a "you already have an account" notice through the outbox, so every path
runs a transaction and an outbox write), `register` and `register/resend` are
throttled per email (`register_email`; a throttled call answers 429 and sends
nothing, counted whether or not an account exists), and password login spends a hash comparison on unknown
emails and on accounts without a password so timing does not reveal
registrations or OAuth-only accounts. With `REGISTRATION_ENABLED=false` only invited addresses (pending org
invitation or a super-admin `signup_invites` row) get an account; the others
join `signup_waitlist` through `registration.Waitlist` (one "on the waitlist"
email per address, social sign-up redirects with `oauth_error=waitlisted`),
and super admins page `GET /admin/waitlist`, delete entries and
`POST /admin/waitlist/notify` the oldest unnotified addresses without an
account (allowlisted while invite-only, then emailed a `/register` link).
Both flows end on the SPA `/onboarding` step
(workspace name + editable URL slug → existing `POST /orgs`); the public SPA pages `/register`,
`/verify` and `/oauth/callback` plus the profile "Connected accounts" card do
the client side, and `GET /api/v1/auth/capabilities` (registered by
`ee/registration`, providers list supplied by `ee/oauth`) lets the SPA hide the
entry points on servers without SaaS mode. Registration/social write through
`postgres.NewRegistrationRepository` (dbtx-aware, joins the caller's
transaction); the public routes attach to the unfiltered `/api/v1/auth`
WebService through `api.WithPublicRoutes`/`serverapp.WithPublicRoutes`, and
`api.Wrap` is the exported AppContext route adapter. Monthly/yearly billing
lives in `ee/billing` inside `nipad-ee` (design in `docs/BILLING.md`; see
the billing flow below).

Flow for storage quota (design in `docs/CLOUD_STORAGE_QUOTA.md`):
`internal/usecase.StorageLedger` is attached with `Chunk.WithStorageLedger`;
nil (the free default) skips all accounting. `PresignUploadURLs` calls
`checkHeadroom` before signing (advisory), `ConfirmUploads` attributes the
present chunks in one transaction, and `MergeForMergeRequest` attributes
server-generated diff3 chunks through `Chunk.AttributeProjectChunks` (the
`chunkUploader` seam) before the commit lands. Quota failures are 402
(`domain.NewErrorQuotaExceeded`), crossing gRPC as `ResourceExhausted`. The
enterprise implementation lives in `ee/storageusage` (postgres,
`STORAGE_LEDGER=cloud`; `SAAS_MODE=1` auto-enables it so the seeded free-tier
quota is enforced; the free `cmd/nipad` ignores the setting and keeps
running with no ledger, no quota and the routes 404): it bills
every org to one owner user (lazily seeded from
`organizations.created_by_user_id`), tracks per-project chunk links with
cached project/org counters, and enforces `owner_quota` (NULL/0 =
unlimited; missing row = `STORAGE_LEDGER_DEFAULT_QUOTA_BYTES`, default
15 GiB, or `SAAS_FREE_QUOTA_BYTES` in SaaS mode via `ledgerDefaultQuotaBytes`). `RecountOrg`
rebuilds links and counters from the stored history and skips deleted
projects (`ListUnbilledProjectIDsByOrg` covers deleted and BYO projects:
links dropped, usage 0). `Project.Delete` calls `ReleaseProject` (attached by
`BuildUsecases` through `Project.WithStorageLedger`) inside its audited
write: under the owner advisory lock it drops the project's `project_chunks`,
zeroes `project_usage`, subtracts the bytes from `org_usage` and returns them
for the `project.deleted` audit entry (`released_bytes`). `attribute`
rejects a deleted project with 404 under the same lock, so an in-flight
confirm or merge cannot re-charge it. `Project.Restore` calls
`RestoreProject` (rebuild links from history, charge the difference, 402 when
it does not fit). The purge of deleted projects past
`STORAGE_PURGE_RETENTION_DAYS` is `ee/projectpurge` (see
`docs/internals/server.md`); chunk GC: `docs/plans/STORAGE_RECLAIM.md`. REST routes
(`/orgs/{org}/storage`, `/billing/me`, admin quota/transfer/recount) are
registered on the shared `/api/v1` WebService via `serverapp.WithRoutes` only
when the ledger is enabled, so they 404 otherwise; the SPA hides the org
storage card on 404, while the `/settings/billing` page is always linked from
the header and the profile settings nav (it shows an empty state when the
ledger is off).
`owner_quota` has two columns: `quota_bytes` is the computed (billing)
quota written by `Ledger.SetComputedQuota`, `override_bytes` the super-admin
override (`PUT /admin/billing/owners/{user}/quota` → `SetQuotaOverride`, a
null body overrides to unlimited = 0; `DELETE` → `ClearQuotaOverride`). The
effective quota is `COALESCE(override_bytes, quota_bytes)`, with the same
NULL/0/missing-row semantics; `Ledger.BillingOwner` is the exported org →
owner resolution.

Flow for bring-your-own storage (`nipad-ee`, SAAS_MODE=1 with
`BYO_STORAGE_ENCRYPTION_KEY` set, `config.ValidateBYOStorage`; guide in
`docs/BYO_STORAGE.md`): postgres migration `000013_byo_storage` adds
`storage_servers` (owner user, name unique per owner, endpoint, region,
bucket, prefix, access key id, `secret_sealed` = `sealbox` label `nipa byo
storage secret v1`, AAD `storage_server:<id>`), `org_storage_defaults`
(org → server, `set_by`) and `project_storage` (project → server, written
once at creation; FKs `ON DELETE RESTRICT` on the server), served by the
dbtx-aware `postgres.NewBYOStorageRepository`. `ee/byostorage.Service` (stub
tested): `Create`/`Update` validate (`NormalizeEndpoint`: https only, no
userinfo/path/query, public literal IPs and names), probe through
`s3.Store.Probe` (a failed check is a `*ProbeFailure`, REST 400 `{error,
check}`), seal the secret and audit `storage_server.created|updated`
(metadata never holds keys); at most 20 servers per owner (advisory lock),
bucket/prefix in a PATCH are a 400, `Delete` is a 409 while a project or a
live org default uses the server, `Test` re-probes (200 `{ok, check,
error}`). Every customer request goes through `NewTransport`: no proxy and a
`net.Dialer.Control` that refuses loopback/private/link-local/CGNAT/metadata
addresses at connect time (`BYO_STORAGE_ALLOW_PRIVATE` lifts it and allows
http, tests only). `SetOrgDefault` needs an org owner who is the org's
billing owner (`Ledger.BillingOwner`) setting a server of their own (audits
`org.storage_default.set|cleared`). `Service.Assign` is the
`ProjectStorageAssigner`: a requested server must be the caller's and the
caller the billing owner, else the org default applies while its server
still belongs to the billing owner, else nothing (Nipa storage); it writes
`project_storage` in the creation transaction and audits
`project.storage_server.set`. `Service.Resolver(defaultStore)` is the
`ChunkStores`: project → server rows cached `ResolverTTL` (60 s), opened S3
stores cached per server and replaced when `updated_at` changes, `External:
true`, `ReadOnly` when the optional `Entitlement.OwnStorageActive` says no
(nil = always active; `nipad-ee` attaches `billing.Service` only when billing
is on, see the billing flow); an unopenable server is 502 `storage server
unavailable`. The ledger excludes these projects: `ListProjectIDsByOrg` and
`SetOrgUsageFromProjects` skip them, `RecountOrg` drops their links, and
`OrgStorage` lists them with `external: true` and 0 bytes. Routes (only when
on, `ee/cmd/nipad/byostorage.go` `buildBYOStorage`): `GET|POST
/me/storage-servers`, `GET|PATCH|DELETE /me/storage-servers/{id}`, `POST
.../{id}/test`, `GET|PUT|DELETE /orgs/{org}/storage-server` (GET for org
members: `{server: {id, name}, can_manage}`), `GET
/orgs/{org}/projects/{project}/storage` (project read: `{external,
server}`); capabilities carry `byo_storage: true`; SPA helpers in
`web/src/api/endpoints.ts`; `ee/e2e/byostorage_test.go` pushes through a
second minio bucket, `ee/e2e/byostorage_billing_test.go` gates it on the
Own storage add-on.

Flow for billing (design in `docs/BILLING.md`): the only free-side change is the nil-able `usecase.SeatGate` seam
(`Admit(ctx, orgID, userID, insert)`), attached through
`UsecaseDeps.SeatGate` to `Org.AddMember` (not role updates or org
creation), `Group.AddMember` and user-subject `Permission.CreateRule` (group
rules add no seat); a nil gate runs `insert` directly. The postgres Org,
Group and PBAC repositories resolve the transaction from the context
(`dbtx.New`) so the insert joins the gate's transaction. `ee/billing.Service`
implements it over small interfaces (repository, ledger, transactor; like
`ee/registration`, unit-tested with stubs), while the SQL lives in
`postgres.BillingRepository` (`ee/repository/postgres/billing_repository.go`).
`Admit` runs one transaction: resolve the org's billing owner, take the
per-owner `pg_advisory_xact_lock` (`LockOwner`, own key namespace), admit
users that already hold a seat (`IsSeat`), otherwise count seats
(`CountSeats`: distinct non-deleted users reached through `org_members`,
`group_members` of non-deleted groups and user `pbac_rules` of the owner's
non-deleted orgs, owner included) against the seat limit (live
subscription's paid seats + `BILLING_FREE_SEATS`: the first free seats come
with every plan; `BILLING_FREE_SEATS` alone without one), and refuse with a
402 `seat limit reached`.
`Service.Apply` takes a provider-neutral `domain.BillingSubscription`,
upserts it by `provider_subscription_id` only when `provider_updated_at` is
newer, and recomputes `quota_bytes` from the live subscription
(`active|trialing|past_due`, newest update) with
`entitlement.go` (`free + seats*seat_bytes + packs*pack_bytes`, 0 free =
unlimited, saturating); it never touches `override_bytes`.
`Service.ReconcileQuotas` (`reconcile.go`) runs at SaaS startup as a
background `serverapp.Dispatcher` (`quotaReconcileJob`, provider-less service
when billing is off, failures only logged, cancelled on shutdown): it keyset-pages `owner_quota` (`QuotaOwners`, 500), recomputes
each owner under `LockOwner`, rewrites only differing `quota_bytes`, skips
deleted users and audits `billing.quotas.reconciled` when anything changed
(also after a partial run, with `complete: false`). `internal/config` carries
`BILLING_PROVIDER` (`off` default), `PADDLE_*` and `BILLING_*` keys with
`ValidateBilling` (every key, `SAAS_MODE=1`, the ledger, free seats ≥ 1,
bytes > 0); `ee/cmd/nipad/billing.go` (`buildBilling`) wires nothing with
`off`, otherwise builds the Paddle client and the service, passes it as
`UsecaseDeps.SeatGate`, registers the routes, the webhook root and the
`/billing/me` hook. The provider seam is `billing.Provider`
(`ee/billing/provider.go`: `EnsureCustomer`, `ResolveDiscount`,
`CreateCheckout` (optional discount id), `UpdateSubscription`, `Get/ListSubscriptions`, `PortalURL`, `ParseWebhook`,
`DecodeEvent`, `EventSubscription`; `Items{seats, storage_packs, own_storage,
interval month|year}`, seats = paid seats, `Validate` needs seats + packs ≥ 1
or `own_storage`, `Config.ValidItems` also refuses `own_storage` when the
add-on is not offered, price ids always come from config). Own storage
add-on: optional `PADDLE_PRICE_OWN_STORAGE_MONTHLY`/`_YEARLY` (both or
neither, all price ids distinct, `Config.OwnStorageOffered`), one item of
quantity 1, `billing_subscriptions.own_storage` (postgres `000014`);
`Service.OwnStorageActive(owner)` (live subscription with the add-on, cached
per owner 60 s in `ownstorage.go`, forgotten by `Apply` and
`ChangeSubscription`, a lookup older than the last forget is not stored) is
the `byostorage` entitlement wired by `byoStorageSetup.entitle` only when
billing is on (billing off = BYO never gated; billing on without add-on prices
warns); dropping the add-on is allowed and turns own-storage projects
read-only; no admin override. `ee/billing/paddle`
implements it over the Paddle Billing REST API (`sandbox-api.paddle.com` /
`api.paddle.com` by `PADDLE_ENVIRONMENT`, bearer key, 15 s timeout):
`POST /customers` (a `customer_already_exists` 409 adopts the `ctm_` id from
the error detail, else `GET /customers?email=`), `GET
/discounts?code=&status=active` (first match that is enabled for checkout, not
expired and under its usage limit, else 400 `discount code is not valid`),
`POST /transactions` with `custom_data.user_id` (base36; seat/storage items
only for a non-zero quantity, the own-storage item when set; `discount_id` only when set), `PATCH /subscriptions/{id}`
(`prorated_immediately`, `on_payment_failure: prevent_change`, full item list),
`GET /subscriptions?customer_id=` (cursor `after`), `POST
/customers/{id}/portal-sessions` (`urls.general.overview`). Mapping: seats and
packs are the quantities of the configured seat/storage prices, the
own-storage price sets `OwnStorage`, the interval
is the matched price's, an unknown price, mixed intervals or no items is an
error (packs-only maps to 0 seats),
`cancel_at_period_end` = `scheduled_change.action == "cancel"`. Paddle 409s and
`subscription_locked*`/`subscription_update_when*` codes become 409, other
4xx codes naming a discount the generic 400, the rest
502 (detail internal, never the key). Webhooks verify `Paddle-Signature`
(`ts=…;h1=…`, HMAC-SHA256 over `ts:rawBody`, any `h1`, 5 min tolerance;
`paddle.Sign` builds one for tests). `Service`: `Checkout` (body
`{seats, storage_packs, own_storage, interval, discount_code?}`; 409 with a live
subscription or when paid seats < seats used − free seats; a trimmed
non-empty code is resolved after those checks and before the customer is
ensured, audited as `discount_code`; customer stored in
`billing_accounts` once), `ChangeSubscription` (404 without a live
subscription, 409 for the same seat rule or below used bytes, a missing
`own_storage` keeps the live one, applies the PATCH answer immediately; audit
metadata carries `own_storage`), `Portal`,
`Summary`, `Sync`, `HandleEvent` (stores `billing_events` first, a
processed/failed duplicate is a no-op, an unfinished one is retried; user from
`billing_accounts.provider_customer_id` (`custom_data` must match it, unknown customers are rejected, upserts never change `user_id`); 4xx outcomes such
as unknown/deleted users or unmappable data are marked failed and answered
200, everything else returns the error), `Replay`; only
`subscription.created|updated|trialing|activated|past_due|paused|resumed|canceled`
apply, other types are stored as processed. HTTP: `POST /api/webhooks/paddle`
is its own WebService (`billing.WebhookService` through
`serverapp.WithWebService` / `api.WithWebService`, no bearer or origin filter,
1 MiB raw body cap, 401 bad signature, 500 only for transient errors);
`/api/v1` gains `POST /billing/checkout` → `{transaction_id}`, `PATCH
/billing/subscription`, `POST /billing/portal` → `{url}`, super-admin `POST
/admin/billing/owners/{user}/sync` and `POST
/admin/billing/events/{id}/replay`; `GET /billing/me` stays in
`ee/storageusage` and merges `storageusage.BillingSummary` (plan, status,
interval, seats (paid quantity), seats_used, seat_limit (paid + free), storage_packs,
own_storage, current_period_end, cancel_at_period_end) through `RoutesConfig.Billing` only when set;
`registration.WithBilling` adds `billing: {provider, environment,
client_token, prices (`own_storage` null when not offered), free_seats, free_quota_bytes, seat_included_bytes,
storage_pack_bytes}` to `GET /api/v1/auth/capabilities`. The `ee/e2e`
smoke (`TestEnterpriseBillingPaddleSmoke`) drives signed fake events against a
fake Paddle. The SPA side is the `/settings/billing` plan card and editor (see
Web UI below).

