-- name: AuditLogInsert :exec
INSERT INTO audit_log (
    id, occurred_at, actor_type, actor_user_id, actor_label, action, org_id, project_id,
    target_type, target_id, metadata, request_id, client_ip, user_agent
) VALUES (
    sqlc.arg('id'), sqlc.arg('occurred_at'), sqlc.arg('actor_type'), sqlc.narg('actor_user_id'), sqlc.arg('actor_label'),
    sqlc.arg('action'), sqlc.narg('org_id'), sqlc.narg('project_id'), sqlc.arg('target_type'), sqlc.arg('target_id'),
    sqlc.arg('metadata'), sqlc.arg('request_id'), sqlc.arg('client_ip'), sqlc.arg('user_agent')
);

-- name: AuditLogList :many
SELECT * FROM audit_log
WHERE (sqlc.narg('actor_user_id')::bigint IS NULL OR actor_user_id = sqlc.narg('actor_user_id')::bigint)
  AND (sqlc.narg('org_id')::bigint IS NULL OR org_id = sqlc.narg('org_id')::bigint)
  AND (sqlc.narg('project_id')::bigint IS NULL OR project_id = sqlc.narg('project_id')::bigint)
  AND (sqlc.narg('action_prefix')::text IS NULL OR left(action, length(sqlc.narg('action_prefix')::text)) = sqlc.narg('action_prefix')::text)
  AND (sqlc.narg('target_id')::text IS NULL OR target_id = sqlc.narg('target_id')::text)
  AND (sqlc.narg('since')::timestamptz IS NULL OR occurred_at >= sqlc.narg('since')::timestamptz)
  AND (sqlc.narg('until')::timestamptz IS NULL OR occurred_at < sqlc.narg('until')::timestamptz)
  AND (sqlc.narg('after')::bigint IS NULL OR id < sqlc.narg('after')::bigint)
ORDER BY id DESC
LIMIT sqlc.arg('limit')::bigint;
