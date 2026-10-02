-- name: WebhookCreate :one
INSERT INTO webhooks (id, project_id, name, url, secret, events, path_prefix, is_active, insecure_tls)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(url), sqlc.arg(secret), sqlc.arg(events), sqlc.arg(path_prefix), sqlc.arg(is_active), sqlc.arg(insecure_tls))
RETURNING *;

-- name: WebhookGet :one
SELECT * FROM webhooks
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: WebhookGetByID :one
SELECT * FROM webhooks
WHERE id = sqlc.arg(id);

-- name: WebhookListByProject :many
SELECT * FROM webhooks
WHERE project_id = sqlc.arg(project_id)
ORDER BY id;

-- name: WebhookListActiveByProject :many
SELECT * FROM webhooks
WHERE project_id = sqlc.arg(project_id) AND is_active = TRUE
ORDER BY id;

-- name: WebhookUpdate :one
UPDATE webhooks
SET name = sqlc.arg(name),
    url = sqlc.arg(url),
    events = sqlc.arg(events),
    path_prefix = sqlc.arg(path_prefix),
    is_active = sqlc.arg(is_active),
    insecure_tls = sqlc.arg(insecure_tls),
    updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: WebhookRotateSecret :one
UPDATE webhooks
SET secret = sqlc.arg(secret), updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: WebhookDelete :execrows
DELETE FROM webhooks
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: WebhookDeliveryDeleteByWebhook :execrows
DELETE FROM webhook_deliveries
WHERE webhook_id = sqlc.arg(webhook_id);

-- name: WebhookDeliveryCreate :one
INSERT INTO webhook_deliveries (id, webhook_id, event_type, payload, state, next_retry_at)
VALUES (sqlc.arg(id), sqlc.arg(webhook_id), sqlc.arg(event_type), sqlc.arg(payload), sqlc.arg(state), sqlc.arg(next_retry_at))
RETURNING *;

-- name: WebhookDeliveryGet :one
SELECT * FROM webhook_deliveries
WHERE webhook_id = sqlc.arg(webhook_id) AND id = sqlc.arg(id);

-- name: WebhookDeliveryList :many
SELECT * FROM webhook_deliveries
WHERE webhook_id = sqlc.arg(webhook_id)
ORDER BY id DESC
LIMIT sqlc.arg('limit')::bigint OFFSET sqlc.arg('offset')::bigint;

-- name: WebhookDeliveryListPendingRetry :many
SELECT * FROM webhook_deliveries
WHERE state = 'pending' AND next_retry_at IS NOT NULL AND next_retry_at <= sqlc.arg(now)::timestamptz
ORDER BY next_retry_at
LIMIT sqlc.arg('limit')::bigint;

-- name: WebhookDeliveryMarkDelivered :one
UPDATE webhook_deliveries
SET state = 'delivered',
    attempt = sqlc.arg(attempt),
    response_status = sqlc.arg(response_status),
    last_error = '',
    next_retry_at = NULL,
    delivered_at = sqlc.arg(delivered_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: WebhookDeliveryReschedule :one
UPDATE webhook_deliveries
SET state = 'pending',
    attempt = sqlc.arg(attempt),
    response_status = sqlc.arg(response_status),
    last_error = sqlc.arg(last_error),
    next_retry_at = sqlc.arg(next_retry_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: WebhookDeliveryMarkFailed :one
UPDATE webhook_deliveries
SET state = 'failed',
    attempt = sqlc.arg(attempt),
    response_status = sqlc.arg(response_status),
    last_error = sqlc.arg(last_error),
    next_retry_at = NULL
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: WebhookDeliveryRequeue :one
UPDATE webhook_deliveries
SET state = 'pending', last_error = '', next_retry_at = sqlc.arg(next_retry_at)
WHERE webhook_id = sqlc.arg(webhook_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: WebhookDeliveryDeleteOld :execrows
DELETE FROM webhook_deliveries
WHERE webhook_deliveries.webhook_id = sqlc.arg('webhook_id')::bigint
  AND webhook_deliveries.state <> 'pending'
  AND webhook_deliveries.id NOT IN (
      SELECT d.id FROM webhook_deliveries d
      WHERE d.webhook_id = sqlc.arg('webhook_id')::bigint
      ORDER BY d.id DESC
      LIMIT sqlc.arg('keep')::bigint
  );
