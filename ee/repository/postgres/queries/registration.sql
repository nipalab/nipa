-- name: UserCreateUnverified :one
INSERT INTO users (id, name, email, password, photo_url, is_admin, email_verified)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(email), sqlc.arg(password), sqlc.arg(photo_url), false, false)
RETURNING id;

-- name: EmailVerificationUpsert :exec
INSERT INTO email_verifications (user_id, token_hash, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token_hash), sqlc.arg(expires_at))
ON CONFLICT (user_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash, expires_at = EXCLUDED.expires_at, created_at = now();

-- name: EmailVerificationGetByToken :one
SELECT user_id, expires_at FROM email_verifications WHERE token_hash = sqlc.arg(token_hash);

-- name: EmailVerificationDelete :exec
DELETE FROM email_verifications WHERE user_id = sqlc.arg(user_id);

-- name: EmailVerificationDeleteExpired :execrows
DELETE FROM email_verifications WHERE expires_at < sqlc.arg(before);

-- name: UserIdentityGet :one
SELECT * FROM user_identities
WHERE provider = sqlc.arg(provider) AND provider_account_id = sqlc.arg(provider_account_id);

-- name: UserIdentityGetByID :one
SELECT * FROM user_identities WHERE id = sqlc.arg(id);

-- name: UserIdentityCountByUser :one
SELECT COUNT(*)::BIGINT AS count FROM user_identities WHERE user_id = sqlc.arg(user_id);

-- name: UserIdentityListByUser :many
SELECT * FROM user_identities WHERE user_id = sqlc.arg(user_id) ORDER BY provider, id;

-- name: UserIdentityCreate :one
INSERT INTO user_identities (id, user_id, provider, provider_account_id, email)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(provider), sqlc.arg(provider_account_id), sqlc.arg(email))
RETURNING id;

-- name: UserIdentityDelete :execrows
DELETE FROM user_identities WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: OAuthLoginCodeCreate :exec
INSERT INTO oauth_login_codes (code, user_id, expires_at)
VALUES (sqlc.arg(code), sqlc.arg(user_id), sqlc.arg(expires_at));

-- name: OAuthLoginCodeGetAndDelete :one
DELETE FROM oauth_login_codes WHERE code = sqlc.arg(code) RETURNING *;

-- name: OAuthLoginCodeDeleteExpired :execrows
DELETE FROM oauth_login_codes WHERE expires_at < sqlc.arg(before);
