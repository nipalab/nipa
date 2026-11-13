-- name: UserTOTPGet :one
SELECT * FROM user_totp WHERE user_id = sqlc.arg(user_id);

-- name: UserTOTPUpsertPending :execrows
INSERT INTO user_totp (user_id, secret_enc, last_step, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(secret_enc), 0, sqlc.arg(created_at))
ON CONFLICT (user_id) DO UPDATE
SET secret_enc = excluded.secret_enc,
    last_step = 0,
    created_at = excluded.created_at
WHERE user_totp.confirmed_at IS NULL;

-- name: UserTOTPConfirm :execrows
UPDATE user_totp
SET confirmed_at = sqlc.arg(confirmed_at),
    last_step = sqlc.arg(last_step)
WHERE user_id = sqlc.arg(user_id)
  AND confirmed_at IS NULL
  AND secret_enc = sqlc.arg(secret_enc);

-- name: UserTOTPAdvanceStep :execrows
UPDATE user_totp
SET last_step = sqlc.arg(step)
WHERE user_id = sqlc.arg(user_id)
  AND confirmed_at IS NOT NULL
  AND last_step < sqlc.arg(step);

-- name: UserTOTPDelete :execrows
DELETE FROM user_totp WHERE user_id = sqlc.arg(user_id);

-- name: UserTOTPCountConfirmed :one
SELECT COUNT(*) FROM user_totp WHERE confirmed_at IS NOT NULL;

-- name: MFARecoveryCodeInsert :exec
INSERT INTO mfa_recovery_codes (user_id, code_hash)
VALUES (sqlc.arg(user_id), sqlc.arg(code_hash));

-- name: MFARecoveryCodeDeleteByUser :exec
DELETE FROM mfa_recovery_codes WHERE user_id = sqlc.arg(user_id);

-- name: MFARecoveryCodeUse :execrows
UPDATE mfa_recovery_codes
SET used_at = sqlc.arg(used_at)
WHERE user_id = sqlc.arg(user_id)
  AND code_hash = sqlc.arg(code_hash)
  AND used_at IS NULL;

-- name: MFARecoveryCodeCountUnused :one
SELECT COUNT(*) FROM mfa_recovery_codes
WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL;

-- name: MFAChallengeCreate :exec
INSERT INTO mfa_login_challenges (token_hash, user_id, method, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(method), sqlc.arg(expires_at));

-- name: MFAChallengeDeleteExpired :execrows
DELETE FROM mfa_login_challenges WHERE expires_at < sqlc.arg(before);

-- name: MFAChallengeGet :one
SELECT * FROM mfa_login_challenges WHERE token_hash = sqlc.arg(token_hash);

-- name: MFAChallengeAddAttempt :one
UPDATE mfa_login_challenges
SET attempts = attempts + 1
WHERE token_hash = sqlc.arg(token_hash)
RETURNING attempts;

-- name: MFAChallengeDelete :execrows
DELETE FROM mfa_login_challenges WHERE token_hash = sqlc.arg(token_hash);

-- name: MFAChallengeDeleteByUser :exec
DELETE FROM mfa_login_challenges WHERE user_id = sqlc.arg(user_id);
