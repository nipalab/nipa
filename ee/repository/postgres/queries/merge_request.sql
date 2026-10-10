-- MergeRequestCreate allocates the per-project number while holding a lock on
-- the project row, so concurrent creates cannot pick the same number.
-- name: MergeRequestCreate :one
WITH project_lock AS MATERIALIZED (
    SELECT id FROM projects WHERE id = sqlc.arg(project_id) FOR UPDATE
)
INSERT INTO merge_requests (
    id, number, project_id, source_branch_id, target_branch_id,
    source_branch_name, target_branch_name, title, description, is_draft,
    merge_base_commit_id, created_by
)
SELECT
    sqlc.arg(id), COALESCE(MAX(merge_requests.number), 0) + 1, sqlc.arg(project_id),
    sqlc.arg(source_branch_id), sqlc.arg(target_branch_id),
    sqlc.arg(source_branch_name), sqlc.arg(target_branch_name),
    sqlc.arg(title), sqlc.arg(description), sqlc.arg(is_draft),
    sqlc.arg(merge_base_commit_id), sqlc.arg(created_by)
FROM merge_requests, project_lock
WHERE merge_requests.project_id = sqlc.arg(project_id)
RETURNING *;

-- name: MergeRequestGet :one
SELECT * FROM merge_requests WHERE project_id = sqlc.arg(project_id) AND number = sqlc.arg(number);

-- name: MergeRequestList :many
SELECT * FROM merge_requests
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
  AND (sqlc.narg('author')::bigint IS NULL OR created_by = sqlc.narg('author')::bigint)
  AND (sqlc.narg('source_branch')::text IS NULL OR source_branch_name = sqlc.narg('source_branch')::text)
  AND (sqlc.narg('target_branch')::text IS NULL OR target_branch_name = sqlc.narg('target_branch')::text)
  AND (sqlc.narg('draft')::boolean IS NULL OR is_draft = sqlc.narg('draft')::boolean)
  AND (sqlc.narg('search')::text IS NULL OR title ILIKE '%' || sqlc.narg('search')::text || '%' OR description ILIKE '%' || sqlc.narg('search')::text || '%')
  AND (sqlc.narg('assignee')::bigint IS NULL OR EXISTS (
      SELECT 1 FROM merge_request_assignees ma
      WHERE ma.merge_request_id = merge_requests.id AND ma.user_id = sqlc.narg('assignee')::bigint
  ))
  AND (sqlc.narg('after_number')::bigint IS NULL OR number < sqlc.narg('after_number')::bigint)
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

-- name: MergeRequestSetDraft :one
UPDATE merge_requests SET is_draft = sqlc.arg(is_draft), updated_at = now()
WHERE project_id = sqlc.arg(project_id) AND number = sqlc.arg(number)
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
WHERE mr.project_id = sqlc.arg(project_id)
ORDER BY mr.number, u.id;

-- name: MergeRequestAssigneeClear :exec
DELETE FROM merge_request_assignees WHERE merge_request_id = sqlc.arg(merge_request_id);

-- name: MergeRequestAssigneeAdd :exec
INSERT INTO merge_request_assignees (merge_request_id, user_id)
VALUES (sqlc.arg(merge_request_id), sqlc.arg(user_id));

-- name: MergeRequestCheckUpsert :one
INSERT INTO merge_request_checks (
    id, merge_request_id, head_commit_id, name, state, details_url, reporter_id
)
VALUES (sqlc.arg(id), sqlc.arg(merge_request_id), sqlc.arg(head_commit_id), sqlc.arg(name), sqlc.arg(state), sqlc.arg(details_url), sqlc.arg(reporter_id))
ON CONFLICT (merge_request_id, head_commit_id, name) DO UPDATE SET
    state = excluded.state,
    details_url = excluded.details_url,
    reporter_id = excluded.reporter_id,
    updated_at = now()
RETURNING *;

-- name: MergeRequestCheckList :many
SELECT c.*, u.name AS reporter_name, u.photo_url AS reporter_photo_url
FROM merge_request_checks c
JOIN users u ON u.id = c.reporter_id
WHERE c.merge_request_id = sqlc.arg(merge_request_id) AND c.head_commit_id = sqlc.arg(head_commit_id)
ORDER BY c.name;

-- name: MergeRequestAssigneeListByMergeRequest :many
SELECT
    u.id AS user_id,
    u.name AS user_name,
    u.photo_url AS user_photo_url
FROM merge_request_assignees a
JOIN users u ON u.id = a.user_id
WHERE a.merge_request_id = sqlc.arg(merge_request_id)
ORDER BY u.id;
