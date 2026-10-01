-- name: TagCreate :one
INSERT INTO tags (id, project_id, name, key, commit_id, message, user_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: TagGetByName :one
SELECT *
FROM tags
WHERE project_id = :project_id AND key = :key
LIMIT 1;

-- name: TagList :many
SELECT *
FROM tags
WHERE project_id = :project_id
  AND (sqlc.arg(last_created_at) is null OR created_at < sqlc.arg(last_created_at) OR (created_at = sqlc.arg(last_created_at) AND id < sqlc.arg(last_id)))
ORDER BY created_at DESC, id DESC
LIMIT :limit;

-- name: TagDelete :execrows
DELETE FROM tags
WHERE project_id = ? AND id = ?;
