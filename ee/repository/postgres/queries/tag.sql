-- name: TagCreate :one
INSERT INTO tags (id, project_id, name, key, commit_id, message, user_id)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(key), sqlc.arg(commit_id), sqlc.arg(message), sqlc.arg(user_id))
RETURNING *;

-- name: TagGetByName :one
SELECT *
FROM tags
WHERE project_id = sqlc.arg(project_id) AND key = sqlc.arg(key)
LIMIT 1;

-- name: TagList :many
SELECT *
FROM tags
WHERE project_id = sqlc.arg(project_id)
  AND (
      sqlc.narg(last_created_at)::timestamptz IS NULL
      OR created_at < sqlc.narg(last_created_at)::timestamptz
      OR (created_at = sqlc.narg(last_created_at)::timestamptz AND id < sqlc.narg(last_id)::bigint)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit')::bigint;

-- name: TagDelete :execrows
DELETE FROM tags
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);
