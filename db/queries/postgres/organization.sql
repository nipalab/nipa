-- name: CreateOrganization :one
INSERT INTO organizations (name, slug, created_by_user_id) VALUES ($1, $2, $3) RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = $1 AND deleted = false LIMIT 1;

-- name: ListOrganizations :many
SELECT * FROM organizations WHERE deleted = false ORDER BY id;

-- name: DeleteOrganization :exec
UPDATE organizations SET deleted = true, deleted_at = now() WHERE id = $1 AND deleted = false;
