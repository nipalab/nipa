-- name: MergeRequestReviewUpsert :one
INSERT INTO merge_request_reviews (
    id, merge_request_id, reviewer_id, state, body, head_commit_id
)
VALUES (sqlc.arg(id), sqlc.arg(merge_request_id), sqlc.arg(reviewer_id), sqlc.arg(state), sqlc.arg(body), sqlc.arg(head_commit_id))
ON CONFLICT (merge_request_id, reviewer_id, head_commit_id) DO UPDATE SET
    state = excluded.state,
    body = excluded.body,
    updated_at = now(),
    dismissed_at = NULL,
    dismissed_by = NULL,
    dismissed_reason = NULL
RETURNING *;

-- name: MergeRequestReviewList :many
SELECT
    r.*,
    u.name AS reviewer_name,
    u.photo_url AS reviewer_photo_url,
    d.name AS dismissed_by_name
FROM merge_request_reviews r
JOIN users u ON u.id = r.reviewer_id
LEFT JOIN users d ON d.id = r.dismissed_by
WHERE r.merge_request_id = sqlc.arg(merge_request_id)
ORDER BY r.id;

-- name: MergeRequestReviewGet :one
SELECT * FROM merge_request_reviews
WHERE merge_request_id = sqlc.arg(merge_request_id) AND id = sqlc.arg(id);

-- name: MergeRequestReviewDismiss :exec
UPDATE merge_request_reviews
SET dismissed_at = sqlc.arg(dismissed_at),
    dismissed_by = sqlc.arg(dismissed_by),
    dismissed_reason = sqlc.arg(dismissed_reason),
    updated_at = now()
WHERE merge_request_id = sqlc.arg(merge_request_id) AND id = sqlc.arg(id);

-- name: MergeRequestReviewDelete :execrows
DELETE FROM merge_request_reviews
WHERE merge_request_id = sqlc.arg(merge_request_id) AND id = sqlc.arg(id);

-- name: MergeRequestReviewDismissStale :exec
UPDATE merge_request_reviews
SET dismissed_at = sqlc.arg(dismissed_at),
    dismissed_by = sqlc.arg(dismissed_by),
    dismissed_reason = sqlc.arg(dismissed_reason),
    updated_at = now()
WHERE merge_request_id = sqlc.arg(merge_request_id)
  AND dismissed_at IS NULL
  AND head_commit_id != sqlc.arg(head_commit_id)
  AND state != 'commented';

-- MergeRequestReviewCarryOver moves live decisions onto a new source head when
-- the target branch keeps decisions across pushes.
-- name: MergeRequestReviewCarryOver :exec
UPDATE merge_request_reviews
SET head_commit_id = sqlc.arg(head_commit_id),
    updated_at = now()
WHERE merge_request_id = sqlc.arg(merge_request_id)
  AND dismissed_at IS NULL
  AND head_commit_id != sqlc.arg(head_commit_id);

-- Aggregates the live review decisions per merge request for the list view.
-- name: MergeRequestReviewSummary :many
SELECT
    mr.number AS merge_request_number,
    b.commit_id AS head_commit_id,
    r.state   AS state,
    COUNT(*)  AS count
FROM merge_request_reviews r
JOIN merge_requests mr ON mr.id = r.merge_request_id
LEFT JOIN branches b ON b.id = mr.source_branch_id AND b.deleted = FALSE
WHERE mr.project_id = sqlc.arg(project_id)
  AND r.dismissed_at IS NULL
  AND r.state != 'commented'
  AND r.head_commit_id = COALESCE(b.commit_id, r.head_commit_id)
GROUP BY mr.number, b.commit_id, r.state;

-- name: MergeRequestThreadCreate :one
INSERT INTO merge_request_threads (
    id, merge_request_id, file_path, old_line, new_line,
    base_commit_id, head_commit_id, created_by
)
VALUES (sqlc.arg(id), sqlc.arg(merge_request_id), sqlc.arg(file_path), sqlc.arg(old_line), sqlc.arg(new_line),
        sqlc.arg(base_commit_id), sqlc.arg(head_commit_id), sqlc.arg(created_by))
RETURNING *;

-- name: MergeRequestThreadGet :one
SELECT * FROM merge_request_threads
WHERE merge_request_id = sqlc.arg(merge_request_id) AND id = sqlc.arg(id);

-- name: MergeRequestThreadList :many
SELECT
    t.*,
    u.name AS created_by_name,
    u.photo_url AS created_by_photo_url,
    r.name AS resolved_by_name
FROM merge_request_threads t
JOIN users u ON u.id = t.created_by
LEFT JOIN users r ON r.id = t.resolved_by
WHERE t.merge_request_id = sqlc.arg(merge_request_id)
  AND (sqlc.narg('resolved')::boolean IS NULL OR t.resolved = sqlc.narg('resolved')::boolean)
ORDER BY t.id;

-- name: MergeRequestThreadSetResolved :one
UPDATE merge_request_threads
SET resolved = sqlc.arg(resolved),
    resolved_by = sqlc.arg(resolved_by),
    resolved_at = sqlc.arg(resolved_at),
    updated_at = now()
WHERE merge_request_id = sqlc.arg(merge_request_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: MergeRequestThreadSetReview :exec
UPDATE merge_request_threads SET review_id = sqlc.arg(review_id)
WHERE merge_request_id = sqlc.arg(merge_request_id) AND id = sqlc.arg(id);

-- name: MergeRequestThreadDelete :execrows
DELETE FROM merge_request_threads
WHERE merge_request_id = sqlc.arg(merge_request_id) AND id = sqlc.arg(id);

-- name: MergeRequestCommentCreate :one
INSERT INTO merge_request_comments (id, thread_id, user_id, body)
VALUES (sqlc.arg(id), sqlc.arg(thread_id), sqlc.arg(user_id), sqlc.arg(body))
RETURNING *;

-- name: MergeRequestCommentGet :one
SELECT * FROM merge_request_comments
WHERE thread_id = sqlc.arg(thread_id) AND id = sqlc.arg(id);

-- name: MergeRequestCommentUpdate :one
UPDATE merge_request_comments
SET body = sqlc.arg(body), updated_at = now()
WHERE thread_id = sqlc.arg(thread_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: MergeRequestCommentDelete :execrows
DELETE FROM merge_request_comments
WHERE thread_id = sqlc.arg(thread_id) AND id = sqlc.arg(id);

-- name: MergeRequestCommentListByThread :many
SELECT
    c.*,
    u.name AS user_name,
    u.photo_url AS user_photo_url
FROM merge_request_comments c
JOIN users u ON u.id = c.user_id
WHERE c.thread_id IN (
    SELECT id FROM merge_request_threads WHERE merge_request_id = sqlc.arg(merge_request_id)
)
ORDER BY c.thread_id, c.id;

-- Re-requesting the same reviewer keeps the original request: the self-update
-- makes the conflict branch return the existing row.
-- name: MergeRequestReviewRequestCreate :one
INSERT INTO merge_request_review_requests (
    id, merge_request_id, reviewer_id, requested_by
)
VALUES (sqlc.arg(id), sqlc.arg(merge_request_id), sqlc.arg(reviewer_id), sqlc.arg(requested_by))
ON CONFLICT (merge_request_id, reviewer_id) DO UPDATE SET
    requested_by = merge_request_review_requests.requested_by
RETURNING *;

-- name: MergeRequestReviewRequestList :many
SELECT
    rr.*,
    u.name AS reviewer_name,
    u.photo_url AS reviewer_photo_url,
    q.name AS requested_by_name
FROM merge_request_review_requests rr
JOIN users u ON u.id = rr.reviewer_id
JOIN users q ON q.id = rr.requested_by
WHERE rr.merge_request_id = sqlc.arg(merge_request_id)
ORDER BY rr.id;

-- name: MergeRequestReviewRequestDelete :execrows
DELETE FROM merge_request_review_requests
WHERE merge_request_id = sqlc.arg(merge_request_id) AND reviewer_id = sqlc.arg(reviewer_id);

-- name: MergeRequestEventCreate :one
INSERT INTO merge_request_events (
    id, merge_request_id, actor_id, subject_user_id, kind, body, commit_id, commit_hash
)
VALUES (sqlc.arg(id), sqlc.arg(merge_request_id), sqlc.arg(actor_id), sqlc.arg(subject_user_id), sqlc.arg(kind), sqlc.arg(body), sqlc.arg(commit_id), sqlc.arg(commit_hash))
RETURNING *;

-- name: MergeRequestEventList :many
SELECT
    e.*,
    u.name AS actor_name,
    u.photo_url AS actor_photo_url,
    s.name AS subject_name,
    s.photo_url AS subject_photo_url
FROM merge_request_events e
JOIN users u ON u.id = e.actor_id
LEFT JOIN users s ON s.id = e.subject_user_id
WHERE e.merge_request_id = sqlc.arg(merge_request_id)
ORDER BY e.id;

-- name: MergeRequestListOpenBySourceBranch :many
SELECT * FROM merge_requests
WHERE project_id = sqlc.arg(project_id)
  AND status = sqlc.arg(status)
  AND source_branch_id = sqlc.arg(branch_id);

-- name: MergeRequestReviewStaleList :many
SELECT * FROM merge_request_reviews
WHERE merge_request_id = sqlc.arg(merge_request_id)
  AND dismissed_at IS NULL
  AND state != 'commented'
  AND head_commit_id != sqlc.arg(head_commit_id);
