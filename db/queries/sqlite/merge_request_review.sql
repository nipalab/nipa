-- name: MergeRequestReviewUpsert :one
-- sqlc only numbers the parameters of the VALUES clause, so the conflict branch
-- reads the pending row through excluded instead of repeating named parameters.
INSERT INTO merge_request_reviews (
    id, merge_request_id, reviewer_id, state, body, head_commit_id
)
VALUES (:id, :merge_request_id, :reviewer_id, :state, :body, :head_commit_id)
ON CONFLICT (merge_request_id, reviewer_id, head_commit_id) DO UPDATE SET
    state = excluded.state,
    body = excluded.body,
    updated_at = CURRENT_TIMESTAMP,
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
WHERE r.merge_request_id = ?
ORDER BY r.id;

-- name: MergeRequestReviewGet :one
SELECT * FROM merge_request_reviews
WHERE merge_request_id = :merge_request_id AND id = :id;

-- name: MergeRequestReviewDismiss :exec
UPDATE merge_request_reviews
SET dismissed_at = :dismissed_at,
    dismissed_by = :dismissed_by,
    dismissed_reason = :dismissed_reason,
    updated_at = CURRENT_TIMESTAMP
WHERE merge_request_id = :merge_request_id AND id = :id;

-- name: MergeRequestReviewDelete :execrows
DELETE FROM merge_request_reviews
WHERE merge_request_id = :merge_request_id AND id = :id;

-- name: MergeRequestReviewDismissStale :exec
UPDATE merge_request_reviews
SET dismissed_at = :dismissed_at,
    dismissed_by = :dismissed_by,
    dismissed_reason = :dismissed_reason,
    updated_at = CURRENT_TIMESTAMP
WHERE merge_request_id = :merge_request_id
  AND dismissed_at IS NULL
  AND head_commit_id != :head_commit_id
  AND state != 'commented';

-- Aggregates the live review decisions per merge request for the list view.
-- The head comparison mirrors the read-time staleness rule the usecase applies
-- to individual reviews: a decision given for a head other than the current
-- source branch head no longer counts.
-- name: MergeRequestReviewSummary :many
SELECT
    mr.number AS merge_request_number,
    b.commit_id AS head_commit_id,
    r.state   AS state,
    COUNT(*)  AS count
FROM merge_request_reviews r
JOIN merge_requests mr ON mr.id = r.merge_request_id
LEFT JOIN branches b ON b.id = mr.source_branch_id AND b.deleted = FALSE
WHERE mr.project_id = :project_id
  AND r.dismissed_at IS NULL
  AND r.state != 'commented'
  AND r.head_commit_id = COALESCE(b.commit_id, r.head_commit_id)
GROUP BY mr.number, b.commit_id, r.state;

-- name: MergeRequestThreadCreate :one
INSERT INTO merge_request_threads (
    id, merge_request_id, file_path, old_line, new_line,
    base_commit_id, head_commit_id, created_by
)
VALUES (:id, :merge_request_id, :file_path, :old_line, :new_line,
        :base_commit_id, :head_commit_id, :created_by)
RETURNING *;

-- name: MergeRequestThreadGet :one
SELECT * FROM merge_request_threads
WHERE merge_request_id = :merge_request_id AND id = :id;

-- name: MergeRequestThreadList :many
SELECT
    t.*,
    u.name AS created_by_name,
    u.photo_url AS created_by_photo_url,
    r.name AS resolved_by_name
FROM merge_request_threads t
JOIN users u ON u.id = t.created_by
LEFT JOIN users r ON r.id = t.resolved_by
WHERE t.merge_request_id = :merge_request_id
  AND (sqlc.arg('resolved') IS NULL OR t.resolved = sqlc.arg('resolved'))
ORDER BY t.id;

-- name: MergeRequestThreadSetResolved :one
UPDATE merge_request_threads
SET resolved = :resolved,
    resolved_by = :resolved_by,
    resolved_at = :resolved_at,
    updated_at = CURRENT_TIMESTAMP
WHERE merge_request_id = :merge_request_id AND id = :id
RETURNING *;

-- name: MergeRequestThreadSetReview :exec
UPDATE merge_request_threads SET review_id = :review_id
WHERE merge_request_id = :merge_request_id AND id = :id;

-- name: MergeRequestThreadDelete :execrows
DELETE FROM merge_request_threads
WHERE merge_request_id = :merge_request_id AND id = :id;

-- name: MergeRequestCommentCreate :one
INSERT INTO merge_request_comments (id, thread_id, user_id, body)
VALUES (:id, :thread_id, :user_id, :body)
RETURNING *;

-- name: MergeRequestCommentGet :one
SELECT * FROM merge_request_comments
WHERE thread_id = :thread_id AND id = :id;

-- name: MergeRequestCommentUpdate :one
UPDATE merge_request_comments
SET body = :body, updated_at = CURRENT_TIMESTAMP
WHERE thread_id = :thread_id AND id = :id
RETURNING *;

-- name: MergeRequestCommentDelete :execrows
DELETE FROM merge_request_comments
WHERE thread_id = :thread_id AND id = :id;

-- name: MergeRequestCommentListByThread :many
SELECT
    c.*,
    u.name AS user_name,
    u.photo_url AS user_photo_url
FROM merge_request_comments c
JOIN users u ON u.id = c.user_id
WHERE c.thread_id IN (
    SELECT id FROM merge_request_threads WHERE merge_request_id = :merge_request_id
)
ORDER BY c.thread_id, c.id;

-- Re-requesting the same reviewer keeps the original request: the self-update
-- makes the conflict branch return the existing row, which DO NOTHING cannot do
-- together with RETURNING.
-- name: MergeRequestReviewRequestCreate :one
INSERT INTO merge_request_review_requests (
    id, merge_request_id, reviewer_id, requested_by
)
VALUES (:id, :merge_request_id, :reviewer_id, :requested_by)
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
WHERE rr.merge_request_id = ?
ORDER BY rr.id;

-- name: MergeRequestReviewRequestDelete :execrows
DELETE FROM merge_request_review_requests
WHERE merge_request_id = :merge_request_id AND reviewer_id = :reviewer_id;

-- name: MergeRequestEventCreate :one
INSERT INTO merge_request_events (
    id, merge_request_id, actor_id, subject_user_id, kind, body, commit_id, commit_hash
)
VALUES (:id, :merge_request_id, :actor_id, :subject_user_id, :kind, :body, :commit_id, :commit_hash)
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
WHERE e.merge_request_id = ?
ORDER BY e.id;

-- name: MergeRequestListOpenBySourceBranch :many
SELECT * FROM merge_requests
WHERE project_id = :project_id
  AND status = :status
  AND source_branch_id = :branch_id;

-- name: MergeRequestReviewStaleList :many
SELECT * FROM merge_request_reviews
WHERE merge_request_id = :merge_request_id
  AND dismissed_at IS NULL
  AND state != 'commented'
  AND head_commit_id != :head_commit_id;
