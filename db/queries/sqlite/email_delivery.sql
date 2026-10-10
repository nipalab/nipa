-- name: EmailDeliveryCreate :one
INSERT INTO email_deliveries (id, event, project_id, user_id, email, subject, thread_key, body, state, next_attempt_at)
VALUES (:id, :event, :project_id, :user_id, :email, :subject, :thread_key, :body, :state, :next_attempt_at)
RETURNING *;

-- name: EmailDeliveryGet :one
SELECT * FROM email_deliveries WHERE id = :id;

-- name: EmailDeliveryThreadRecipients :many
SELECT DISTINCT user_id FROM email_deliveries
WHERE project_id = :project_id AND thread_key = :thread_key;

-- name: EmailDeliveryReclaimStale :execrows
UPDATE email_deliveries
SET state = 'pending', claimed_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE state = 'sending' AND claimed_at IS NOT NULL AND claimed_at < :stale_before;

-- name: EmailDeliveryClaimDue :many
UPDATE email_deliveries
SET state = 'sending', attempts = attempts + 1, claimed_at = :now, updated_at = :now
WHERE id IN (
    SELECT id FROM email_deliveries
    WHERE state = 'pending' AND next_attempt_at IS NOT NULL AND next_attempt_at <= :now
    ORDER BY next_attempt_at, id
    LIMIT :limit
)
RETURNING *;

-- name: EmailDeliveryMarkDelivered :exec
UPDATE email_deliveries
SET state = 'delivered',
    last_error = '',
    next_attempt_at = NULL,
    claimed_at = NULL,
    delivered_at = :delivered_at,
    updated_at = :delivered_at
WHERE id = :id;

-- name: EmailDeliveryScheduleRetry :exec
UPDATE email_deliveries
SET state = 'pending',
    last_error = :last_error,
    next_attempt_at = :next_attempt_at,
    claimed_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = :id;

-- name: EmailDeliveryMarkFailed :exec
UPDATE email_deliveries
SET state = 'failed',
    last_error = :last_error,
    next_attempt_at = NULL,
    claimed_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE id = :id;

-- name: EmailDeliverySweep :execrows
DELETE FROM email_deliveries
WHERE state IN ('delivered', 'failed') AND updated_at < :before;
