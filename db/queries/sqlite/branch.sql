-- name: BranchCreate :exec
INSERT INTO branches (id, project_id, name, commit_id, is_default, is_protected) VALUES (?, ?, ?, ?, ?, ?);

-- name: BranchUpdate :exec
UPDATE branches SET name = ?, key = ?, commit_id = ?, is_protected = ?, is_default = ? WHERE id = :id;

-- name: BranchRemoveDefault :exec
UPDATE branches SET is_default = FALSE WHERE project_id = :project_id AND is_default = TRUE;

-- name: BranchGetDefault :one
SELECT *
FROM branches
WHERE project_id = :project_id AND is_default = TRUE
LIMIT 1;

-- name: BranchList :many
SELECT *
FROM branches
WHERE project_id = :project_id
  AND (sqlc.arg(last_updated_at) is null or updated_at < sqlc.arg(last_updated_at) OR (updated_at = sqlc.arg(last_updated_at) AND id < sqlc.arg(last_id)))
ORDER BY updated_at DESC, id DESC
LIMIT :limit;

-- name: BranchGet :one
SELECT *
FROM branches
WHERE project_id = :project_id AND id = :id
LIMIT 1;

-- name: BranchGetByName :one
SELECT *
FROM branches
WHERE project_id = :project_id AND name = :name
LIMIT 1;