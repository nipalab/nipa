-- name: SSHKeyCreate :one
INSERT INTO ssh_public_keys (id, user_id, name, key_type, key_blob, fingerprint, comment)
VALUES (sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(key_type), sqlc.arg(key_blob), sqlc.arg(fingerprint), sqlc.arg(comment))
RETURNING *;

-- name: SSHKeyGetByID :one
SELECT *
FROM ssh_public_keys
WHERE id = sqlc.arg(id)
LIMIT 1;

-- name: SSHKeyGetByFingerprint :one
SELECT *
FROM ssh_public_keys
WHERE fingerprint = sqlc.arg(fingerprint)
LIMIT 1;

-- name: SSHKeyListByUser :many
SELECT *
FROM ssh_public_keys
WHERE user_id = sqlc.arg(user_id)
ORDER BY created_at ASC, id ASC;

-- name: SSHKeyCountByUser :one
SELECT COUNT(*)
FROM ssh_public_keys
WHERE user_id = sqlc.arg(user_id);

-- name: SSHKeyDelete :execrows
DELETE FROM ssh_public_keys
WHERE user_id = sqlc.arg(user_id) AND id = sqlc.arg(id);
