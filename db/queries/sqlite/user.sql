-- name: UserCreate :one
INSERT INTO users (id, name, email, password, photo_url, is_admin) 
VALUES (?, ?, ?, ?, ?, ?) RETURNING id;

-- name: UserGetByEmail :one
SELECT * FROM users WHERE email = ? AND deleted = false LIMIT 1;

-- name: UserGetById :one
SELECT * FROM users WHERE id = ? AND deleted = false LIMIT 1;

-- name: UserDeleteByID :exec
UPDATE users SET deleted = true, deleted_at = CURRENT_TIMESTAMP WHERE id = ? AND deleted = false;

-- name: UserUpdateProfile :exec
UPDATE users SET name = ?, photo_url = ? 
WHERE id = ? AND deleted = false;

-- name: UserUpdatePassword :exec
UPDATE users SET password = ?
WHERE id = ? AND deleted = false;

-- name: UserList :many
SELECT * FROM users WHERE deleted = false ORDER BY name, id;

-- name: UserUpdateEmail :exec
UPDATE users SET email = ? WHERE id = ? AND deleted = false;

-- name: UserUpdateAdminFlags :exec
UPDATE users SET is_admin = ?, is_super_admin = ? WHERE id = ? AND deleted = false;
