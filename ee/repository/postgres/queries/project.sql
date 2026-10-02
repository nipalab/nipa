-- name: CreateProject :one
INSERT INTO projects (id, org_id, slug, name, description)
VALUES (sqlc.arg(id), sqlc.arg(org_id), sqlc.arg(slug), sqlc.arg(name), sqlc.arg(description))
RETURNING *;

-- name: GetProject :one
SELECT * FROM projects WHERE id = sqlc.arg(id) AND deleted = false LIMIT 1;

-- name: GetProjectByOrgIDAndID :one
SELECT * FROM projects WHERE id = sqlc.arg(id) AND org_id = sqlc.arg(org_id) AND deleted = false LIMIT 1;

-- name: GetProjectByOrgIDAndSlug :one
SELECT * FROM projects WHERE org_id = sqlc.arg(org_id) AND slug = sqlc.arg(slug) AND deleted = false LIMIT 1;

-- name: ListProjectsByOrgId :many
SELECT * FROM projects WHERE org_id = sqlc.arg(org_id) AND deleted = false ORDER BY id;

-- name: UpdateProject :one
UPDATE projects SET name = sqlc.arg(name), description = sqlc.arg(description), updated_at = now()
WHERE id = sqlc.arg(id) AND deleted = false
RETURNING *;

-- name: DeleteProject :exec
UPDATE projects SET deleted = true, deleted_at = now() WHERE id = sqlc.arg(id) AND deleted = false;
