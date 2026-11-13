-- name: RefreshSessionCreate :one
INSERT INTO refresh_tokens (user_id, token_hash, session_started_at, expires_at, auth_method, mfa)
VALUES (sqlc.arg(user_id), sqlc.arg(token_hash), sqlc.arg(session_started_at), sqlc.arg(expires_at), sqlc.narg(auth_method), sqlc.narg(mfa))
RETURNING id;

-- name: RefreshSessionGet :one
SELECT * FROM refresh_tokens WHERE id = sqlc.arg(id);

-- name: RefreshSessionRotate :execrows
UPDATE refresh_tokens
SET token_hash = sqlc.arg(new_hash),
    previous_hash = sqlc.arg(current_hash),
    rotated_at = sqlc.arg(rotated_at),
    expires_at = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id) AND token_hash = sqlc.arg(current_hash);

-- name: RefreshSessionDelete :execrows
DELETE FROM refresh_tokens WHERE id = sqlc.arg(id);

-- name: SSHLoginChallengeCreate :exec
INSERT INTO ssh_login_challenges (email, nonce, expires_at)
VALUES (?, ?, ?);

-- name: SSHLoginChallengeDeleteExpired :execrows
DELETE FROM ssh_login_challenges
WHERE expires_at < ?;

-- name: SSHLoginChallengeConsume :one
DELETE FROM ssh_login_challenges
WHERE nonce = ?
RETURNING *;
-- name: RefreshTokenDeleteByUser :exec
DELETE FROM refresh_tokens WHERE user_id = ?;

-- name: RefreshTokenDeleteExpiredByUser :exec
DELETE FROM refresh_tokens WHERE user_id = ? AND expires_at < ?;
