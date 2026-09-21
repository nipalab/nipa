-- name: BranchCreate :exec
INSERT INTO branches (id, project_id, name, key, commit_id, is_default, is_protected) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: BranchUpdate :exec
UPDATE branches SET name = ?, key = ?, commit_id = ?, is_protected = ?, is_default = ? WHERE id = :id;

-- name: BranchRemoveDefault :exec
UPDATE branches SET is_default = FALSE WHERE project_id = :project_id AND is_default = TRUE;

-- name: BranchGetDefault :one
SELECT *
FROM branches
WHERE project_id = :project_id AND is_default = TRUE AND deleted = FALSE
LIMIT 1;

-- name: BranchList :many
SELECT *
FROM branches
WHERE project_id = :project_id
  AND deleted = FALSE
  AND (sqlc.arg(last_updated_at) is null or updated_at < sqlc.arg(last_updated_at) OR (updated_at = sqlc.arg(last_updated_at) AND id < sqlc.arg(last_id)))
ORDER BY updated_at DESC, id DESC
LIMIT :limit;

-- name: BranchGet :one
SELECT *
FROM branches
WHERE project_id = :project_id AND id = :id AND deleted = FALSE
LIMIT 1;

-- name: BranchGetByName :one
SELECT *
FROM branches
WHERE project_id = :project_id AND name = :name AND deleted = FALSE
LIMIT 1;

-- name: BranchSetName :exec
UPDATE branches SET name = ?, key = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ? AND deleted = FALSE;

-- name: BranchSoftDelete :execrows
UPDATE branches SET deleted = TRUE, deleted_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ? AND deleted = FALSE;

-- name: BranchSetProtection :exec
UPDATE branches SET is_protected = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ? AND deleted = FALSE;

-- name: BranchMarkDefault :exec
UPDATE branches SET is_default = TRUE, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ? AND deleted = FALSE;

-- name: BranchUpdateCommitIf :execresult
UPDATE branches
SET commit_id = :to_commit_id, updated_at = CURRENT_TIMESTAMP
WHERE id = :id AND deleted = FALSE AND commit_id = :from_commit_id;