-- MergeRequestCreate allocates the per-project number while holding a lock on
-- the project row, so concurrent creates cannot pick the same number.
-- name: MergeRequestCreate :one
WITH project_lock AS MATERIALIZED (
    SELECT id FROM projects WHERE id = sqlc.arg(project_id) FOR UPDATE
)
INSERT INTO merge_requests (
    id, number, project_id, source_branch_id, target_branch_id,
    source_branch_name, target_branch_name, title, description,
    merge_base_commit_id, created_by
)
SELECT
    sqlc.arg(id), COALESCE(MAX(merge_requests.number), 0) + 1, sqlc.arg(project_id),
    sqlc.arg(source_branch_id), sqlc.arg(target_branch_id),
    sqlc.arg(source_branch_name), sqlc.arg(target_branch_name),
    sqlc.arg(title), sqlc.arg(description),
    sqlc.arg(merge_base_commit_id), sqlc.arg(created_by)
FROM merge_requests, project_lock
WHERE merge_requests.project_id = sqlc.arg(project_id)
RETURNING *;

-- name: MergeRequestGet :one
SELECT * FROM merge_requests WHERE project_id = sqlc.arg(project_id) AND number = sqlc.arg(number);

-- name: MergeRequestList :many
SELECT * FROM merge_requests
WHERE project_id = sqlc.arg(project_id)
ORDER BY number DESC
LIMIT sqlc.arg('limit')::bigint;

-- name: MergeRequestListByStatus :many
SELECT * FROM merge_requests
WHERE project_id = sqlc.arg(project_id) AND status = sqlc.arg(status)
ORDER BY number DESC
LIMIT sqlc.arg('limit')::bigint;

-- name: MergeRequestCountOpenByBranch :one
SELECT COUNT(*) FROM merge_requests
WHERE project_id = sqlc.arg(project_id)
  AND status = sqlc.arg(status)
  AND (source_branch_id = sqlc.arg(branch_id) OR target_branch_id = sqlc.arg(branch_id));

-- name: MergeRequestUpdateStatus :exec
UPDATE merge_requests SET status = sqlc.arg(status), merge_commit_id = sqlc.arg(merge_commit_id), updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND number = sqlc.arg(number);

-- name: MergeRequestUpdate :one
UPDATE merge_requests SET title = sqlc.arg(title), description = sqlc.arg(description), updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND number = sqlc.arg(number)
RETURNING *;

-- name: MergeRequestDelete :exec
DELETE FROM merge_requests WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id);
