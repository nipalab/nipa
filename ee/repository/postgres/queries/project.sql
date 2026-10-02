-- name: CreateProject :one
INSERT INTO projects (org_id, slug, name, description) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1 AND deleted = false LIMIT 1;

-- name: ListProjectsByOrgId :many
SELECT * FROM projects WHERE org_id = $1 AND deleted = false ORDER BY id;

-- name: DeleteProject :exec
UPDATE projects SET deleted = true, deleted_at = now() WHERE id = $1 AND deleted = false;
