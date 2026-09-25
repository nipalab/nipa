-- name: MergeRequestCreate :one
INSERT INTO merge_requests (
    id, project_id, source_branch_id, target_branch_id,
    source_branch_name, target_branch_name, title, description,
    merge_base_commit_id, created_by
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: MergeRequestGet :one
SELECT * FROM merge_requests WHERE project_id = ? AND id = ?;

-- name: MergeRequestList :many
SELECT * FROM merge_requests
WHERE project_id = ?
ORDER BY id DESC
LIMIT ?;

-- name: MergeRequestListByStatus :many
SELECT * FROM merge_requests
WHERE project_id = ? AND status = ?
ORDER BY id DESC
LIMIT ?;

-- name: MergeRequestCountOpenByBranch :one
SELECT COUNT(*) FROM merge_requests
WHERE project_id = :project_id
  AND status = :status
  AND (source_branch_id = :branch_id OR target_branch_id = :branch_id);

-- name: MergeRequestUpdateStatus :exec
UPDATE merge_requests SET status = ?, merge_commit_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ?;

-- name: MergeRequestUpdate :one
UPDATE merge_requests SET title = ?, description = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND id = ?
RETURNING *;
