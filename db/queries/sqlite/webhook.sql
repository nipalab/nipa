-- name: WebhookCreate :one
INSERT INTO webhooks (id, project_id, name, url, secret, events, path_prefix, is_active, insecure_tls)
VALUES (:id, :project_id, :name, :url, :secret, :events, :path_prefix, :is_active, :insecure_tls)
RETURNING *;

-- name: WebhookGet :one
SELECT * FROM webhooks
WHERE project_id = :project_id AND id = :id;

-- name: WebhookListByProject :many
SELECT * FROM webhooks
WHERE project_id = :project_id
ORDER BY id;

-- name: WebhookListActiveByProject :many
SELECT * FROM webhooks
WHERE project_id = :project_id AND is_active = TRUE
ORDER BY id;

-- name: WebhookUpdate :one
UPDATE webhooks
SET name = :name,
    url = :url,
    events = :events,
    path_prefix = :path_prefix,
    is_active = :is_active,
    insecure_tls = :insecure_tls,
    updated_at = CURRENT_TIMESTAMP
WHERE project_id = :project_id AND id = :id
RETURNING *;

-- name: WebhookRotateSecret :one
UPDATE webhooks
SET secret = :secret, updated_at = CURRENT_TIMESTAMP
WHERE project_id = :project_id AND id = :id
RETURNING *;

-- name: WebhookDelete :execrows
DELETE FROM webhooks
WHERE project_id = :project_id AND id = :id;

-- name: WebhookDeliveryDeleteByWebhook :execrows
DELETE FROM webhook_deliveries
WHERE webhook_id = :webhook_id;

-- name: WebhookDeliveryCreate :one
INSERT INTO webhook_deliveries (id, webhook_id, event_type, payload, state, next_retry_at)
VALUES (:id, :webhook_id, :event_type, :payload, :state, :next_retry_at)
RETURNING *;

-- name: WebhookDeliveryGet :one
SELECT * FROM webhook_deliveries
WHERE webhook_id = :webhook_id AND id = :id;

-- name: WebhookDeliveryList :many
SELECT * FROM webhook_deliveries
WHERE webhook_id = :webhook_id
ORDER BY id DESC
LIMIT :limit OFFSET :offset;

-- name: WebhookDeliveryListPendingRetry :many
SELECT * FROM webhook_deliveries
WHERE state = 'pending' AND next_retry_at IS NOT NULL AND next_retry_at <= :now
ORDER BY next_retry_at
LIMIT :limit;

-- name: WebhookDeliveryMarkDelivered :one
UPDATE webhook_deliveries
SET state = 'delivered',
    attempt = :attempt,
    response_status = :response_status,
    last_error = '',
    next_retry_at = NULL,
    delivered_at = :delivered_at
WHERE id = :id
RETURNING *;

-- name: WebhookDeliveryReschedule :one
UPDATE webhook_deliveries
SET state = 'pending',
    attempt = :attempt,
    response_status = :response_status,
    last_error = :last_error,
    next_retry_at = :next_retry_at
WHERE id = :id
RETURNING *;

-- name: WebhookDeliveryMarkFailed :one
UPDATE webhook_deliveries
SET state = 'failed',
    attempt = :attempt,
    response_status = :response_status,
    last_error = :last_error,
    next_retry_at = NULL
WHERE id = :id
RETURNING *;

-- name: WebhookDeliveryRequeue :one
UPDATE webhook_deliveries
SET state = 'pending', last_error = '', next_retry_at = :next_retry_at
WHERE webhook_id = :webhook_id AND id = :id
RETURNING *;

-- name: WebhookDeliveryDeleteOld :execrows
DELETE FROM webhook_deliveries
WHERE webhook_deliveries.webhook_id = sqlc.arg('webhook_id')
  AND webhook_deliveries.state <> 'pending'
  AND webhook_deliveries.id NOT IN (
      SELECT d.id FROM webhook_deliveries d
      WHERE d.webhook_id = sqlc.arg('webhook_id')
      ORDER BY d.id DESC
      LIMIT sqlc.arg('keep')
  );
