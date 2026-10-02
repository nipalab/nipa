-- name: CreateOrganization :one
INSERT INTO organizations (id, name, slug, created_by_user_id)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(slug), sqlc.arg(created_by_user_id))
RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = sqlc.arg(id) AND deleted = false LIMIT 1;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations WHERE slug = sqlc.arg(slug) AND deleted = false LIMIT 1;

-- name: ListOrganizations :many
SELECT * FROM organizations WHERE deleted = false ORDER BY id;

-- name: DeleteOrganization :exec
UPDATE organizations SET deleted = true, deleted_at = now() WHERE id = sqlc.arg(id) AND deleted = false;
