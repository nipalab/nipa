-- name: CountOwnerSeats :one
WITH owner_orgs AS (
    SELECT b.org_id AS id
    FROM org_billing_owner b
    JOIN organizations o ON o.id = b.org_id AND o.deleted = false
    WHERE b.user_id = sqlc.arg(owner_id)::bigint
    UNION
    SELECT o.id
    FROM organizations o
    WHERE o.created_by_user_id = sqlc.arg(owner_id)::bigint
      AND o.deleted = false
      AND NOT EXISTS (SELECT 1 FROM org_billing_owner b WHERE b.org_id = o.id)
), seats AS (
    SELECT sqlc.arg(owner_id)::bigint AS user_id
    UNION
    SELECT m.user_id FROM org_members m JOIN owner_orgs oo ON oo.id = m.org_id
    UNION
    SELECT gm.user_id
    FROM group_members gm
    JOIN groups g ON g.id = gm.group_id AND g.deleted = false
    JOIN owner_orgs oo ON oo.id = g.org_id
    UNION
    SELECT r.user_id FROM pbac_rules r JOIN owner_orgs oo ON oo.id = r.org_id WHERE r.user_id IS NOT NULL
)
SELECT COUNT(*)::bigint AS seats
FROM seats s
JOIN users u ON u.id = s.user_id
WHERE u.deleted = false;

-- name: IsOwnerSeat :one
WITH owner_orgs AS (
    SELECT b.org_id AS id
    FROM org_billing_owner b
    JOIN organizations o ON o.id = b.org_id AND o.deleted = false
    WHERE b.user_id = sqlc.arg(owner_id)::bigint
    UNION
    SELECT o.id
    FROM organizations o
    WHERE o.created_by_user_id = sqlc.arg(owner_id)::bigint
      AND o.deleted = false
      AND NOT EXISTS (SELECT 1 FROM org_billing_owner b WHERE b.org_id = o.id)
)
SELECT EXISTS (
    SELECT 1 FROM users u
    WHERE u.id = sqlc.arg(user_id)::bigint
      AND u.deleted = false
      AND (
        u.id = sqlc.arg(owner_id)::bigint
        OR EXISTS (SELECT 1 FROM org_members m JOIN owner_orgs oo ON oo.id = m.org_id WHERE m.user_id = u.id)
        OR EXISTS (
            SELECT 1 FROM group_members gm
            JOIN groups g ON g.id = gm.group_id AND g.deleted = false
            JOIN owner_orgs oo ON oo.id = g.org_id
            WHERE gm.user_id = u.id)
        OR EXISTS (SELECT 1 FROM pbac_rules r JOIN owner_orgs oo ON oo.id = r.org_id WHERE r.user_id = u.id)
      )
)::boolean AS is_seat;

-- name: LockSeatOwner :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

-- name: GetLiveSubscription :one
SELECT * FROM billing_subscriptions
WHERE user_id = sqlc.arg(user_id)
  AND status IN ('active', 'trialing', 'past_due')
ORDER BY provider_updated_at DESC, provider_subscription_id DESC
LIMIT 1;

-- name: UpsertSubscription :execrows
INSERT INTO billing_subscriptions (
    provider_subscription_id, user_id, plan, status, interval, seats, storage_packs,
    current_period_end, cancel_at_period_end, provider_updated_at, updated_at
) VALUES (
    sqlc.arg(provider_subscription_id), sqlc.arg(user_id), sqlc.arg(plan), sqlc.arg(status),
    sqlc.arg('interval'), sqlc.arg(seats), sqlc.arg(storage_packs),
    sqlc.narg(current_period_end)::timestamptz, sqlc.arg(cancel_at_period_end),
    sqlc.arg(provider_updated_at), now()
)
ON CONFLICT (provider_subscription_id) DO UPDATE SET
    plan = EXCLUDED.plan,
    status = EXCLUDED.status,
    interval = EXCLUDED.interval,
    seats = EXCLUDED.seats,
    storage_packs = EXCLUDED.storage_packs,
    current_period_end = EXCLUDED.current_period_end,
    cancel_at_period_end = EXCLUDED.cancel_at_period_end,
    provider_updated_at = EXCLUDED.provider_updated_at,
    updated_at = now()
WHERE billing_subscriptions.user_id = EXCLUDED.user_id
  AND billing_subscriptions.provider_updated_at < EXCLUDED.provider_updated_at;

-- name: GetBillingAccount :one
SELECT * FROM billing_accounts WHERE user_id = sqlc.arg(user_id);

-- name: GetBillingAccountByCustomer :one
SELECT * FROM billing_accounts
WHERE provider = sqlc.arg(provider) AND provider_customer_id = sqlc.arg(provider_customer_id);

-- name: InsertBillingAccount :exec
INSERT INTO billing_accounts (user_id, provider, provider_customer_id)
VALUES (sqlc.arg(user_id), sqlc.arg(provider), sqlc.arg(provider_customer_id))
ON CONFLICT DO NOTHING;

-- name: GetBillingUser :one
SELECT id, name, email, deleted FROM users WHERE id = sqlc.arg(id);

-- name: InsertBillingEvent :execrows
INSERT INTO billing_events (event_id, type, occurred_at, payload)
VALUES (sqlc.arg(event_id), sqlc.arg(type), sqlc.arg(occurred_at), sqlc.arg(payload))
ON CONFLICT (event_id) DO NOTHING;

-- name: GetBillingEvent :one
SELECT * FROM billing_events WHERE event_id = sqlc.arg(event_id);

-- name: MarkBillingEventProcessed :exec
UPDATE billing_events SET processed_at = now(), error = NULL WHERE event_id = sqlc.arg(event_id);

-- name: MarkBillingEventFailed :exec
UPDATE billing_events SET processed_at = NULL, error = sqlc.arg(error) WHERE event_id = sqlc.arg(event_id);

-- name: ListQuotaOwners :many
SELECT user_id FROM owner_quota
WHERE user_id > sqlc.arg(after)::bigint
ORDER BY user_id
LIMIT sqlc.arg('limit')::bigint;
