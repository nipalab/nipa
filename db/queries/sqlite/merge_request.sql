-- name: MergeRequestCreate :one
INSERT INTO merge_requests (
    id, number, project_id, source_branch_id, target_branch_id,
    source_branch_name, target_branch_name, title, description,
    merge_base_commit_id, created_by
)
SELECT
    sqlc.arg(id), COALESCE(MAX(number), 0) + 1, sqlc.arg(project_id),
    sqlc.arg(source_branch_id), sqlc.arg(target_branch_id),
    sqlc.arg(source_branch_name), sqlc.arg(target_branch_name),
    sqlc.arg(title), sqlc.arg(description),
    sqlc.arg(merge_base_commit_id), sqlc.arg(created_by)
FROM merge_requests
WHERE project_id = sqlc.arg(project_id)
RETURNING *;

-- name: MergeRequestGet :one
SELECT * FROM merge_requests WHERE project_id = ? AND number = ?;

-- name: MergeRequestList :many
SELECT * FROM merge_requests
WHERE project_id = ?
ORDER BY number DESC
LIMIT ?;

-- name: MergeRequestListByStatus :many
SELECT * FROM merge_requests
WHERE project_id = ? AND status = ?
ORDER BY number DESC
LIMIT ?;

-- name: MergeRequestCountOpenByBranch :one
SELECT COUNT(*) FROM merge_requests
WHERE project_id = :project_id
  AND status = :status
  AND (source_branch_id = :branch_id OR target_branch_id = :branch_id);

-- name: MergeRequestUpdateStatus :exec
UPDATE merge_requests SET status = ?, merge_commit_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND number = ?;

-- name: MergeRequestUpdate :one
UPDATE merge_requests SET title = ?, description = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND number = ?
RETURNING *;
