-- name: SSHKeyCreate :one
INSERT INTO ssh_public_keys (id, user_id, name, key_type, key_blob, fingerprint, comment)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: SSHKeyGetByID :one
SELECT *
FROM ssh_public_keys
WHERE id = ?
LIMIT 1;

-- name: SSHKeyGetByFingerprint :one
SELECT *
FROM ssh_public_keys
WHERE fingerprint = ?
LIMIT 1;

-- name: SSHKeyListByUser :many
SELECT *
FROM ssh_public_keys
WHERE user_id = ?
ORDER BY created_at ASC, id ASC;

-- name: SSHKeyCountByUser :one
SELECT COUNT(*)
FROM ssh_public_keys
WHERE user_id = ?;

-- name: SSHKeyDelete :execrows
DELETE FROM ssh_public_keys
WHERE user_id = ? AND id = ?;
