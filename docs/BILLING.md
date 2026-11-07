# SaaS billing

How the SaaS (`nipad-ee` with `SAAS_MODE=1`) sells seats and storage through
Paddle Billing and turns subscriptions into enforced entitlements. This page
describes the mechanism only. Prices, plans and commercial strategy live in the
deployment environment and the Paddle dashboard, never in the repository.

The free `nipad` links none of this: it has no seat gate, no ledger and no
billing routes.

## Architecture

Billing lives inside `nipad-ee` as the `ee/billing` module, not in a separate
service.

- **Paddle is the source of truth for money and subscription state.** As the
  Merchant of Record it owns prices, checkout, tax, invoices, proration,
  dunning, receipts and the customer portal.
- **nipad owns the derived entitlements** (seat limit, `owner_quota.quota_bytes`)
  and enforces them inside its own transactions: the quota check is part of
  the storage ledger's `Attribute` transaction and the seat check shares a
  transaction with the membership insert. A separate service would need its
  own database and a sync API back into nipad.
- **Free/EE boundary.** `internal/` only has the nil-able
  `usecase.SeatGate` seam (`internal/usecase/seat_gate.go`), the same pattern as
  `usecase.StorageLedger`. Everything that knows about subscriptions or Paddle
  is under `ee/`.
- **Extraction stays possible.** `billing.Provider` (`ee/billing/provider.go`)
  hides Paddle, and every webhook is stored in `billing_events`.

| Piece | Where |
|---|---|
| Entitlement rules (pure) | `ee/billing/entitlement.go` |
| Apply / entitlement lookup | `ee/billing/service.go` |
| Seat gate (`Admit`) | `ee/billing/seats.go` |
| Checkout, change, portal, summary, sync | `ee/billing/account.go` |
| Webhook event handling, replay | `ee/billing/events.go`, `ee/billing/webhook.go` |
| REST routes | `ee/billing/api.go`; `GET /billing/me` in `ee/storageusage/api.go` |
| Paddle REST client, signatures, mapping | `ee/billing/paddle` |
| SQL | `ee/repository/postgres/queries/billing.sql`, `postgres.BillingRepository` |
| Wiring | `ee/cmd/nipad/billing.go` (`buildBilling`) |
| SPA | `web/src/pages/BillingPage.tsx`, `web/src/components/BillingPlan.tsx`, `web/src/billing/paddle.ts` |

## Billing account

The billing account is the **owner user**, the same one the storage ledger
bills (`org_billing_owner`, lazily seeded from `organizations.created_by_user_id`,
see `docs/CLOUD_STORAGE_QUOTA.md`). One subscription covers every organization
billed to that user. Per-org billing is not supported.

## Seats

A seat is a distinct, non-deleted user with an access path into a non-deleted
organization billed to the owner, the owner included:

- an `org_members` row of the org,
- a `group_members` row of a non-deleted group of the org,
- a `pbac_rules` row of the org (org-wide or project rule) naming the user.

A user reached through several paths or several orgs counts once
(`CountOwnerSeats`, `IsOwnerSeat` in `billing.sql`).

The gate wraps exactly three inserts, through `UsecaseDeps.SeatGate`:
`Org.AddMember`, `Group.AddMember` and `Permission.CreateRule` when the rule
names a user (group rules add nobody). Role changes, org creation (the creator
is their own owner) and the super-admin billing-owner transfer are not gated.
`Admit` runs one transaction: resolve the org's owner, take the per-owner
`pg_advisory_xact_lock`, admit a user that already holds a seat, otherwise
count seats against the seat limit and refuse with **402
`seat limit reached: N of M seats in use; ...`**. The insert runs on the same
transaction, so two concurrent adds cannot both take the last seat.

Lowering the limit never removes access: existing seats keep working and only
new seats are refused.

## Entitlements

`billing.Config.Entitlement` (`ee/billing/entitlement.go`) maps the owner's
**live subscription** to an entitlement. The live subscription is the row in
`billing_subscriptions` with status `active`, `trialing` or `past_due` and the
newest `provider_updated_at`.

| Subscription | Seat limit | Computed quota |
|---|---|---|
| `active`, `trialing`, `past_due` | `max(seats, BILLING_FREE_SEATS)` | `max(free, seats * BILLING_SEAT_INCLUDED_BYTES + packs * BILLING_STORAGE_PACK_BYTES)` |
| `paused`, `canceled`, none | `BILLING_FREE_SEATS` | `free` |

- `free` is `SAAS_FREE_QUOTA_BYTES`, the quota new accounts are seeded with.
  It is the floor of every paid quota. When it is 0 (unlimited), the paid
  quota is unlimited too.
- The products saturate at the int64 maximum instead of overflowing.
- `past_due` keeps paid entitlements while Paddle's dunning retries the
  payment. Dunning ends with `subscription.canceled` (or `paused`, per the
  Paddle dashboard setting), which drops to the free tier. nipad keeps no grace
  timer of its own, so nothing needs a cron.
- A scheduled cancellation (`scheduled_change.action == "cancel"`, shown as
  `cancel_at_period_end`) keeps paid entitlements until Paddle sends
  `subscription.canceled` at the period end.

`Service.Apply` writes the computed quota to `owner_quota.quota_bytes`
(`Ledger.SetComputedQuota`) in the same transaction as the subscription
upsert. The storage ledger enforces it as before: over quota, writes get 402
at presign, confirm and merge; reads keep working.

### Admin override

`owner_quota.override_bytes` is the super-admin override and always wins:
the effective quota is `COALESCE(override_bytes, quota_bytes)`, where 0 means
unlimited. `PUT /api/v1/admin/billing/owners/{user}/quota` sets it (a null
`quota_bytes` overrides to unlimited), `DELETE` on the same path clears it so
the computed quota applies again. `Apply` never touches the override, so
billing and support changes do not undo each other. The SPA billing page has
the editor for super admins.

## Flows

### Upgrade

1. The SPA (`/settings/billing`) calls `POST /api/v1/billing/checkout`
   `{seats, storage_packs, interval}` (`interval` is `month` or `year`;
   `monthly`/`yearly` are accepted too).
2. The server refuses with 409 when the caller already has a live
   subscription or `seats` is below the seats in use, and with 400 for
   `seats < 1` or negative packs.
3. It ensures the Paddle customer (`POST /customers`; a
   `customer_already_exists` conflict adopts the existing `ctm_` id) and stores
   it in `billing_accounts`.
4. It creates a Paddle transaction (`POST /transactions`) with the configured
   price ids for the interval (storage item only when packs > 0) and
   `custom_data.user_id`, and returns `{transaction_id}`.
5. The SPA opens the Paddle.js overlay with `Paddle.Checkout.open({ transactionId })`.
   The browser never chooses prices.
6. Paddle creates the subscription and posts webhooks. After the
   `checkout.completed` event the SPA polls `GET /billing/me` for a short while
   until the plan is no longer `free`.

### Change seats, storage or interval

`PATCH /api/v1/billing/subscription {seats, storage_packs, interval?}`
(missing interval keeps the current one) replaces the whole item list, so an
interval change swaps both prices in one update, with
`proration_billing_mode: prorated_immediately` and
`on_payment_failure: prevent_change`. It answers:

- 404 without a live subscription,
- 409 when seats would drop below the seats in use, when the computed quota
  would drop below the bytes in use, or when Paddle refuses (a 409 or a
  `subscription_locked*` / `subscription_update_when*` error, for example a
  pending scheduled change),
- 502 for other Paddle failures,
- otherwise the billing summary. The subscription Paddle returns is applied
  right away; the webhook that follows is a no-op. The response carries the
  billing fields only, not the storage usage, so the SPA reloads
  `GET /billing/me` afterwards.

### Manage

`POST /api/v1/billing/portal` returns `{url}`, a Paddle customer-portal
session (`POST /customers/{id}/portal-sessions`, scoped to the live
subscription when there is one) for payment methods, invoices and
cancellation. 404 when the user never checked out.

### Read

`GET /api/v1/billing/me` (owned by `ee/storageusage`) returns the storage
summary (`user_id`, `used_bytes`, `quota_bytes`, `orgs`) and, when billing is
on, `plan` (`free` without a live subscription, otherwise `team`), `status`,
`interval`, `seats` (subscribed quantity, 0 on free), `seats_used`,
`seat_limit`, `storage_packs`, `current_period_end` and
`cancel_at_period_end`. It answers 404 when the storage ledger is off.

`GET /api/v1/auth/capabilities` (owned by `ee/registration`) carries
`billing: {provider, environment, client_token, prices: {seat: {monthly,
yearly}, storage: {monthly, yearly}}}` when billing is on, and omits it
otherwise. Price ids and the client token are public by design; the SPA needs
them for Paddle.js price previews.

## Webhooks

Paddle posts to `POST /api/webhooks/paddle`, its own WebService with no bearer
or origin filter (`billing.WebhookService`).

- **Verification.** The raw body (capped at 1 MiB) is checked against
  `Paddle-Signature` (`ts=<unix>;h1=<hex>`, HMAC-SHA256 with
  `PADDLE_WEBHOOK_SECRET` over `<ts>:<body>`, any `h1` may match, 5 minute
  tolerance). A bad or stale signature is a 401.
- **Dedup.** The event is inserted into `billing_events` keyed by Paddle's
  `event_id` before it is applied. A duplicate that was already processed or
  failed is answered 200 without work; a duplicate whose first delivery never
  finished is processed again.
- **Event types.** `subscription.created`, `updated`, `trialing`,
  `activated`, `past_due`, `paused`, `resumed` and `canceled` are applied.
  Every other type is stored and marked processed.
- **User resolution.** The user comes from `custom_data.user_id` (base36),
  falling back to the account holding the event's `customer_id`.
- **Ordering.** `Apply` upserts by `provider_subscription_id` only when the
  event's `updated_at` is newer than the stored one, so late or reordered
  deliveries are dropped. Entitlements always come from the live
  subscription, so a late event for an old subscription cannot clobber a newer
  one. `Apply` serializes on the same per-owner advisory lock as `Admit`.
- **Permanent vs transient failures.** Domain 4xx outcomes (unknown or deleted
  user, unknown price, mixed intervals, no seat item, malformed data) are
  recorded in `billing_events.error` and answered 200 so Paddle stops
  retrying. Anything else (database errors) is answered 500 and Paddle
  retries.
- **Blue/green.** Both colors may receive events: dedup is the primary key,
  `Apply` is serialized and ordered, so double delivery is harmless.

### Recovery

- `POST /api/v1/admin/billing/events/{id}/replay` (super admin) re-runs a
  stored event, for example after fixing the user it names. It returns the
  event's state (`processed_at`, `error`).
- `POST /api/v1/admin/billing/owners/{user}/sync` (super admin) lists every
  Paddle subscription of the user's customer and applies them, returning
  `{synced}`.

Paddle retries failed deliveries for days, so there is no periodic
reconciler. Neither admin action has a UI yet.

## Config

Read only by `ee/cmd/nipad` (`internal/config/billing.go`, `ValidateBilling`).
Startup fails with a message naming the missing or invalid key; nothing falls
back to a built-in value.

| Key | Meaning |
|---|---|
| `BILLING_PROVIDER` | `off` (default) or `paddle` |
| `PADDLE_ENVIRONMENT` | `sandbox` or `production`; selects the API host and the Paddle.js environment |
| `PADDLE_API_KEY` | server API key (secret) |
| `PADDLE_WEBHOOK_SECRET` | notification destination secret (secret) |
| `PADDLE_CLIENT_TOKEN` | client-side token for Paddle.js (public) |
| `PADDLE_PRICE_SEAT_MONTHLY`, `PADDLE_PRICE_SEAT_YEARLY` | seat price ids |
| `PADDLE_PRICE_STORAGE_MONTHLY`, `PADDLE_PRICE_STORAGE_YEARLY` | storage pack price ids |
| `BILLING_FREE_SEATS` | free-tier seats, at least 1 (the owner is a seat) |
| `BILLING_SEAT_INCLUDED_BYTES` | storage each paid seat adds, > 0 |
| `BILLING_STORAGE_PACK_BYTES` | storage each pack adds, > 0 |

`BILLING_PROVIDER=paddle` also requires `SAAS_MODE=1` and the storage ledger.
Byte values must be positive because 0 means unlimited in the ledger. The
free-tier quota is the existing `SAAS_FREE_QUOTA_BYTES`.
`deploy/nipad-ee.env.sample` lists the keys with placeholders only. Both
blue/green colors must use the same values.

## Web UI

`/settings/billing` (`BillingPage`) always shows the storage card. When
capabilities carries `billing` and `/billing/me` carries `plan`, it adds:

- a plan card (plan, status, interval, "Renews on" or "Cancels on", seats used
  of the limit, storage packs, a "Payment past due" warning),
- an editor with seat and storage-pack steppers (seats never below the seats
  in use), a monthly/yearly toggle and a price preview from
  `Paddle.PricePreview` with the configured price ids (a fallback note when the
  preview fails),
- on the free plan, Upgrade (checkout, then the Paddle overlay, then a pending
  state while polling for the webhook); on a paid plan, "Update plan"
  (disabled while unchanged) and "Manage billing" (portal in a new tab).

Paddle.js is loaded from `https://cdn.paddle.com/paddle/v2/paddle.js` on
demand by `web/src/billing/paddle.ts`, only on this page and only when billing
is on; it calls `Paddle.Environment.set("sandbox")` before
`Paddle.Initialize({ token, eventCallback })` once per page load. Server
409/402 messages are shown inline. The org members dialog, the group members
dialog and the access rule dialog (all `FormDialog`) render a seat-limit 402
as a "Seat limit reached" banner linking to `/settings/billing`.

The app sets no Content-Security-Policy (not in `index.html`, the Go server
or the Caddyfile), so nothing blocks `cdn.paddle.com` or the Paddle checkout
frames. If a CSP is added later it must allow the Paddle.js script, its
checkout frames and its API calls (see Paddle's documentation for the current
host list).

## Sandbox testing runbook

1. In a Paddle sandbox account, create one product for seats and one for
   storage packs, each with a monthly and a yearly recurring price. Create a
   client-side token and an API key.
2. Create a notification destination for the `subscription.*` events pointing
   at `https://<public-host>/api/webhooks/paddle` (use a tunnel such as
   `cloudflared` or `ngrok` for a local `nipad-ee`) and copy its secret.
3. Start `nipad-ee` with `SAAS_MODE=1`, `BILLING_PROVIDER=paddle`,
   `PADDLE_ENVIRONMENT=sandbox` and every key above (test byte values can be
   small to make quota changes visible).
4. Register a user, create an org, open `/settings/billing`: the free plan,
   the steppers and sandbox price previews appear.
5. Upgrade and pay with a Paddle test card (`4242 4242 4242 4242`, any future
   expiry, any CVC). The overlay completes, the page shows the pending state
   and switches to the paid plan once `subscription.created`/`activated`
   arrives. Check the new seat limit and quota.
6. Change seats, packs and interval with "Update plan"; try lowering seats
   below the seats in use (409 inline).
7. Open "Manage billing" and schedule a cancellation: the page shows
   "Cancels on". Cancel immediately (or let the simulated period end) and
   check that the plan returns to free and adding a member over the free seats
   shows the seat-limit banner.
8. Dunning: subscribe with `4000 0027 6000 3184` (first payment succeeds,
   renewals decline) or use the webhook simulator to drive `past_due`
   (warning banner, paid entitlements kept) and then `canceled`.
9. Recovery: break a user mapping or replay an event with
   `POST /api/v1/admin/billing/events/{id}/replay`, and resync an owner with
   `POST /api/v1/admin/billing/owners/{user}/sync`.

Automated coverage: unit tests in `ee/billing` and `ee/billing/paddle`, the
postgres suite in `ee/repository/postgres`, the `ee/e2e` smoke
(`TestEnterpriseBillingPaddleSmoke`, signed fake events against a fake
Paddle) and the SPA tests in `web/src/pages/billing.test.tsx`,
`web/src/billing/paddle.test.ts` and `web/src/test/settingsPages.test.tsx`.
