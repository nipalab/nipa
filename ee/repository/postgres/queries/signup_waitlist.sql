-- name: SignupWaitlistJoin :execrows
INSERT INTO signup_waitlist (id, email, name, source)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.arg(name), sqlc.arg(source))
ON CONFLICT (email) DO NOTHING;

-- name: SignupWaitlistList :many
SELECT id, email, name, source, created_at, notified_at FROM signup_waitlist
WHERE id > sqlc.arg(after)
  AND (sqlc.narg('pending')::boolean IS NULL OR (notified_at IS NULL) = sqlc.narg('pending')::boolean)
ORDER BY id
LIMIT sqlc.arg('limit')::bigint;

-- name: SignupWaitlistDelete :execrows
DELETE FROM signup_waitlist WHERE id = sqlc.arg(id);

-- name: SignupWaitlistPending :many
SELECT w.id, w.email, w.name, w.source, w.created_at, w.notified_at FROM signup_waitlist w
WHERE w.notified_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM users u WHERE lower(u.email) = w.email AND NOT u.deleted)
ORDER BY w.id
LIMIT sqlc.arg('limit')::bigint
FOR UPDATE OF w SKIP LOCKED;

-- name: SignupWaitlistMarkNotified :exec
UPDATE signup_waitlist SET notified_at = sqlc.arg(notified_at)
WHERE id = ANY(sqlc.arg(ids)::bigint[]);
