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
WHERE (sqlc.narg('actor_user_id') IS NULL OR actor_user_id = sqlc.narg('actor_user_id'))
  AND (sqlc.narg('org_id') IS NULL OR org_id = sqlc.narg('org_id'))
  AND (sqlc.narg('project_id') IS NULL OR project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('action_prefix') IS NULL OR (action >= sqlc.narg('action_prefix') AND action < sqlc.narg('action_prefix') || char(1114111)))
  AND (sqlc.narg('target_id') IS NULL OR target_id = sqlc.narg('target_id'))
  AND (sqlc.narg('since') IS NULL OR occurred_at >= sqlc.narg('since'))
  AND (sqlc.narg('until') IS NULL OR occurred_at < sqlc.narg('until'))
  AND (sqlc.narg('min_id') IS NULL OR id >= sqlc.narg('min_id'))
  AND (sqlc.narg('max_id') IS NULL OR id < sqlc.narg('max_id'))
  AND (sqlc.narg('after') IS NULL OR id < sqlc.narg('after'))
ORDER BY id DESC
LIMIT sqlc.arg('limit');
