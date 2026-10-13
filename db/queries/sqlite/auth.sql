-- name: RefreshTokenCreate :one
INSERT INTO refresh_tokens (user_id, token, expires_at) 
VALUES (?, ?, ?) RETURNING id;

-- name: RefreshTokenDeleteByToken :one
DELETE FROM refresh_tokens WHERE token = ? RETURNING *;

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