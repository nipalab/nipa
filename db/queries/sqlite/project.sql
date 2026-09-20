-- name: CreateProject :one
INSERT INTO projects (id, org_id, slug, name, description) VALUES (?, ?, ?, ?, ?) RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = ? AND deleted = false LIMIT 1;

-- name: GetProjectByOrgIDAndID :one
SELECT * FROM projects WHERE id = ? AND org_id = ? AND deleted = false LIMIT 1;

-- name: GetProjectByOrgIDAndSlug :one
SELECT * FROM projects WHERE org_id = ? AND slug = ? AND deleted = false LIMIT 1;

-- name: ListProjectsByOrgId :many
SELECT * FROM projects WHERE org_id = ? AND deleted = false ORDER BY id;

-- name: UpdateProject :one
UPDATE projects SET name = ?, description = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND deleted = false
RETURNING *;

-- name: DeleteProject :exec
UPDATE projects SET deleted = true, deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted = false;
