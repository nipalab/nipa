-- name: SignupInviteUpsert :one
INSERT INTO signup_invites (id, email, invited_by, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.arg(invited_by), sqlc.narg(expires_at))
ON CONFLICT (email) DO UPDATE
SET expires_at = EXCLUDED.expires_at, invited_by = EXCLUDED.invited_by, created_at = now()
RETURNING id, email, invited_by, created_at, expires_at;

-- name: SignupInviteList :many
SELECT id, email, invited_by, created_at, expires_at FROM signup_invites
ORDER BY created_at DESC, id DESC;

-- name: SignupInviteDelete :execrows
DELETE FROM signup_invites WHERE id = sqlc.arg(id);

-- name: SignupInviteAllowed :one
SELECT EXISTS (
    SELECT 1 FROM signup_invites
    WHERE email = sqlc.arg(email) AND (expires_at IS NULL OR expires_at > sqlc.arg(now)::timestamptz)
) AS allowed;
