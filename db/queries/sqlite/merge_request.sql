-- name: MergeRequestCreate :one
INSERT INTO merge_requests (
    id, number, project_id, source_branch_id, target_branch_id,
    source_branch_name, target_branch_name, title, description, is_draft,
    merge_base_commit_id, created_by
)
SELECT
    sqlc.arg(id), COALESCE(MAX(number), 0) + 1, sqlc.arg(project_id),
    sqlc.arg(source_branch_id), sqlc.arg(target_branch_id),
    sqlc.arg(source_branch_name), sqlc.arg(target_branch_name),
    sqlc.arg(title), sqlc.arg(description), sqlc.arg(is_draft),
    sqlc.arg(merge_base_commit_id), sqlc.arg(created_by)
FROM merge_requests
WHERE project_id = sqlc.arg(project_id)
RETURNING *;

-- name: MergeRequestGet :one
SELECT * FROM merge_requests WHERE project_id = ? AND number = ?;

-- name: MergeRequestList :many
SELECT * FROM merge_requests
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg('status') IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('author') IS NULL OR created_by = sqlc.narg('author'))
  AND (sqlc.narg('source_branch') IS NULL OR source_branch_name = sqlc.narg('source_branch'))
  AND (sqlc.narg('target_branch') IS NULL OR target_branch_name = sqlc.narg('target_branch'))
  AND (sqlc.narg('draft') IS NULL OR is_draft = sqlc.narg('draft'))
  AND (sqlc.narg('search') IS NULL OR title LIKE '%' || sqlc.narg('search') || '%' OR description LIKE '%' || sqlc.narg('search') || '%')
  AND (sqlc.narg('assignee') IS NULL OR EXISTS (
      SELECT 1 FROM merge_request_assignees ma
      WHERE ma.merge_request_id = merge_requests.id AND ma.user_id = sqlc.narg('assignee')
  ))
  AND (sqlc.narg('after_number') IS NULL OR number < sqlc.narg('after_number'))
ORDER BY number DESC
LIMIT sqlc.arg('limit');

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

-- name: MergeRequestSetDraft :one
UPDATE merge_requests SET is_draft = ?, updated_at = CURRENT_TIMESTAMP
WHERE project_id = ? AND number = ?
RETURNING *;

-- name: MergeRequestAssigneeList :many
SELECT
    mr.number AS merge_request_number,
    u.id AS user_id,
    u.name AS user_name,
    u.photo_url AS user_photo_url
FROM merge_request_assignees a
JOIN merge_requests mr ON mr.id = a.merge_request_id
JOIN users u ON u.id = a.user_id
WHERE mr.project_id = :project_id
ORDER BY mr.number, u.id;

-- name: MergeRequestAssigneeClear :exec
DELETE FROM merge_request_assignees WHERE merge_request_id = :merge_request_id;

-- name: MergeRequestAssigneeAdd :exec
INSERT INTO merge_request_assignees (merge_request_id, user_id)
VALUES (:merge_request_id, :user_id);

-- name: MergeRequestCheckUpsert :one
INSERT INTO merge_request_checks (
    id, merge_request_id, head_commit_id, name, state, details_url, reporter_id
)
VALUES (:id, :merge_request_id, :head_commit_id, :name, :state, :details_url, :reporter_id)
ON CONFLICT (merge_request_id, head_commit_id, name) DO UPDATE SET
    state = excluded.state,
    details_url = excluded.details_url,
    reporter_id = excluded.reporter_id,
    updated_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: MergeRequestCheckList :many
SELECT c.*, u.name AS reporter_name, u.photo_url AS reporter_photo_url
FROM merge_request_checks c
JOIN users u ON u.id = c.reporter_id
WHERE c.merge_request_id = :merge_request_id AND c.head_commit_id = :head_commit_id
ORDER BY c.name;

-- name: MergeRequestAssigneeListByMergeRequest :many
SELECT
    u.id AS user_id,
    u.name AS user_name,
    u.photo_url AS user_photo_url
FROM merge_request_assignees a
JOIN users u ON u.id = a.user_id
WHERE a.merge_request_id = :merge_request_id
ORDER BY u.id;
