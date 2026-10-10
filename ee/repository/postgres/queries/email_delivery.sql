-- name: EmailDeliveryCreate :one
INSERT INTO email_deliveries (id, event, project_id, user_id, email, subject, thread_key, body, state, next_attempt_at)
VALUES (sqlc.arg('id'), sqlc.arg('event'), sqlc.arg('project_id'), sqlc.arg('user_id'), sqlc.arg('email'), sqlc.arg('subject'), sqlc.arg('thread_key'), sqlc.arg('body'), sqlc.arg('state'), sqlc.arg('next_attempt_at'))
RETURNING *;

-- name: EmailDeliveryGet :one
SELECT * FROM email_deliveries WHERE id = sqlc.arg('id');

-- name: EmailDeliveryThreadRecipients :many
SELECT DISTINCT user_id FROM email_deliveries
WHERE project_id = sqlc.arg('project_id') AND thread_key = sqlc.arg('thread_key');

-- name: EmailDeliveryReclaimStale :execrows
UPDATE email_deliveries
SET state = 'pending', claimed_at = NULL, updated_at = now()
WHERE state = 'sending' AND claimed_at IS NOT NULL AND claimed_at < sqlc.arg('stale_before');

-- name: EmailDeliveryClaimDue :many
UPDATE email_deliveries
SET state = 'sending', attempts = attempts + 1, claimed_at = sqlc.arg('now'), updated_at = sqlc.arg('now')
WHERE id IN (
    SELECT id FROM email_deliveries
    WHERE state = 'pending' AND next_attempt_at IS NOT NULL AND next_attempt_at <= sqlc.arg('now')
    ORDER BY next_attempt_at, id
    LIMIT sqlc.arg('limit')::bigint
)
RETURNING *;

-- name: EmailDeliveryMarkDelivered :exec
UPDATE email_deliveries
SET state = 'delivered',
    last_error = '',
    next_attempt_at = NULL,
    claimed_at = NULL,
    delivered_at = sqlc.arg('delivered_at'),
    updated_at = sqlc.arg('delivered_at')
WHERE id = sqlc.arg('id');

-- name: EmailDeliveryScheduleRetry :exec
UPDATE email_deliveries
SET state = 'pending',
    last_error = sqlc.arg('last_error'),
    next_attempt_at = sqlc.arg('next_attempt_at'),
    claimed_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg('id');

-- name: EmailDeliveryMarkFailed :exec
UPDATE email_deliveries
SET state = 'failed',
    delivered_at = NULL,
    last_error = sqlc.arg('last_error'),
    next_attempt_at = NULL,
    claimed_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg('id');

-- name: EmailDeliverySweep :execrows
DELETE FROM email_deliveries
WHERE state IN ('delivered', 'failed') AND updated_at < sqlc.arg('before');

-- name: EmailDeliveryList :many
SELECT * FROM email_deliveries
WHERE project_id = sqlc.arg('project_id')
  AND (sqlc.narg('state')::text IS NULL OR state = sqlc.narg('state')::text)
  AND (sqlc.narg('after')::bigint IS NULL OR id < sqlc.narg('after')::bigint)
ORDER BY id DESC
LIMIT sqlc.arg('limit')::bigint;

-- name: EmailDeliveryRedeliver :execrows
UPDATE email_deliveries
SET state = 'pending',
    attempts = 0,
    last_error = '',
    delivered_at = NULL,
    next_attempt_at = sqlc.arg('now'),
    claimed_at = NULL,
    updated_at = sqlc.arg('now')
WHERE id = sqlc.arg('id') AND project_id = sqlc.arg('project_id');
