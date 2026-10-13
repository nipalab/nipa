-- name: RefreshTokenCreate :one
INSERT INTO refresh_tokens (user_id, token, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token), sqlc.arg(expires_at)) RETURNING id;

-- name: RefreshTokenDeleteByToken :one
DELETE FROM refresh_tokens WHERE token = sqlc.arg(token) RETURNING *;

-- name: SSHLoginChallengeCreate :exec
INSERT INTO ssh_login_challenges (email, nonce, expires_at)
VALUES (sqlc.arg(email), sqlc.arg(nonce), sqlc.arg(expires_at));

-- name: SSHLoginChallengeDeleteExpired :execrows
DELETE FROM ssh_login_challenges
WHERE expires_at < sqlc.arg(expires_at);

-- name: SSHLoginChallengeConsume :one
DELETE FROM ssh_login_challenges
WHERE nonce = sqlc.arg(nonce)
RETURNING *;
