-- name: ServiceTokenCreate :one
INSERT INTO project_service_tokens (id, project_id, name, token_hash, hint, scopes, created_by, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(token_hash), sqlc.arg(hint), sqlc.arg(scopes), sqlc.arg(created_by), sqlc.narg(expires_at))
RETURNING *;

-- name: ServiceTokenGet :one
SELECT *
FROM project_service_tokens
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
LIMIT 1;

-- name: ServiceTokenGetByHash :one
SELECT *
FROM project_service_tokens
WHERE token_hash = sqlc.arg(token_hash)
LIMIT 1;

-- name: ServiceTokenListByProject :many
SELECT *
FROM project_service_tokens
WHERE project_id = sqlc.arg(project_id)
ORDER BY id DESC;

-- name: ServiceTokenRevoke :one
UPDATE project_service_tokens
SET revoked_at = sqlc.arg(revoked_at)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND revoked_at IS NULL
RETURNING *;

-- name: ServiceTokenTouch :exec
UPDATE project_service_tokens
SET last_used_at = sqlc.arg(last_used_at)
WHERE id = sqlc.arg(id);
