-- name: FileLockCreate :one
INSERT INTO file_locks (id, project_id, branch_id, path, held_by, merge_request_id, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: FileLockGet :one
SELECT * FROM file_locks
WHERE project_id = sqlc.arg(project_id) AND path = sqlc.arg(path) AND branch_id IS sqlc.narg('branch_id')
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: FileLockListProject :many
SELECT
    fl.id, fl.project_id, fl.branch_id, fl.path, fl.held_by, fl.merge_request_id, fl.acquired_at, fl.expires_at,
    u.name AS held_by_name,
    b.name AS branch_name,
    mr.number AS merge_request_number
FROM file_locks fl
JOIN users u ON u.id = fl.held_by
LEFT JOIN branches b ON b.id = fl.branch_id
LEFT JOIN merge_requests mr ON mr.id = fl.merge_request_id
WHERE fl.project_id = sqlc.arg(project_id)
  AND (fl.expires_at IS NULL OR fl.expires_at > sqlc.arg(now))
ORDER BY fl.path;

-- name: FileLockDelete :execrows
DELETE FROM file_locks WHERE id = ?;

-- name: FileLockDeleteExpired :execrows
DELETE FROM file_locks
WHERE project_id = sqlc.arg(project_id) AND expires_at IS NOT NULL AND expires_at <= sqlc.arg(now);

-- name: FileLockSetExpiry :execrows
UPDATE file_locks SET expires_at = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id) AND held_by = sqlc.arg(held_by);

-- name: FileLockDeleteByMergeRequest :execrows
DELETE FROM file_locks WHERE project_id = ? AND merge_request_id = ?;

-- name: FileLockDeleteByBranch :execrows
DELETE FROM file_locks WHERE project_id = ? AND branch_id = ?;
