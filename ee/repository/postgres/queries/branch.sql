-- name: BranchCreate :exec
INSERT INTO branches (id, project_id, name, key, commit_id, is_default, is_protected)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(key), sqlc.arg(commit_id), sqlc.arg(is_default), sqlc.arg(is_protected));

-- name: BranchUpdate :exec
UPDATE branches SET name = sqlc.arg(name), key = sqlc.arg(key), commit_id = sqlc.arg(commit_id), is_protected = sqlc.arg(is_protected), is_default = sqlc.arg(is_default) WHERE id = sqlc.arg(id);

-- name: BranchRemoveDefault :exec
UPDATE branches SET is_default = FALSE WHERE project_id = sqlc.arg(project_id) AND is_default = TRUE;

-- name: BranchGetDefault :one
SELECT *
FROM branches
WHERE project_id = sqlc.arg(project_id) AND is_default = TRUE AND deleted = FALSE
LIMIT 1;

-- name: BranchList :many
SELECT *
FROM branches
WHERE project_id = sqlc.arg(project_id)
  AND deleted = FALSE
  AND (
      sqlc.narg(last_updated_at)::timestamptz IS NULL
      OR updated_at < sqlc.narg(last_updated_at)::timestamptz
      OR (updated_at = sqlc.narg(last_updated_at)::timestamptz AND id < sqlc.narg(last_id)::bigint)
  )
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg('limit')::bigint;

-- name: BranchGet :one
SELECT *
FROM branches
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND deleted = FALSE
LIMIT 1;

-- name: BranchGetByName :one
SELECT *
FROM branches
WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name) AND deleted = FALSE
LIMIT 1;

-- name: BranchSetName :exec
UPDATE branches SET name = sqlc.arg(name), key = sqlc.arg(key), updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND deleted = FALSE;

-- name: BranchSoftDelete :execrows
UPDATE branches SET deleted = TRUE, deleted_at = now()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND deleted = FALSE;

-- name: BranchSetProtection :exec
UPDATE branches SET is_protected = sqlc.arg(is_protected), required_approvals = sqlc.arg(required_approvals), dismiss_stale_approvals = sqlc.arg(dismiss_stale_approvals), updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND deleted = FALSE;

-- name: BranchMarkDefault :exec
UPDATE branches SET is_default = TRUE, updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND deleted = FALSE;

-- name: BranchUpdateCommitIf :execresult
UPDATE branches
SET commit_id = sqlc.arg(to_commit_id), updated_at = now()
WHERE id = sqlc.arg(id) AND deleted = FALSE AND commit_id = sqlc.arg(from_commit_id);
