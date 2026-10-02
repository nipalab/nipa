-- name: RefreshTokenCreate :one
INSERT INTO refresh_tokens (user_id, token, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token), sqlc.arg(expires_at)) RETURNING id;

-- name: RefreshTokenDeleteByToken :one
DELETE FROM refresh_tokens WHERE token = sqlc.arg(token) RETURNING *;
