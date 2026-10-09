-- name: UserCreate :one
INSERT INTO users (id, name, email, password, photo_url, is_admin)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(email), sqlc.arg(password), sqlc.arg(photo_url), sqlc.arg(is_admin))
RETURNING id;

-- name: UserGetByEmail :one
SELECT * FROM users WHERE email = sqlc.arg(email) AND deleted = false LIMIT 1;

-- name: UserGetById :one
SELECT * FROM users WHERE id = sqlc.arg(id) AND deleted = false LIMIT 1;

-- name: UserDeleteByID :exec
UPDATE users SET deleted = true, deleted_at = now() WHERE id = sqlc.arg(id) AND deleted = false;

-- name: UserUpdateProfile :exec
UPDATE users SET name = sqlc.arg(name), photo_url = sqlc.arg(photo_url),
    notify_email = COALESCE(sqlc.narg('notify_email')::boolean, notify_email)
WHERE id = sqlc.arg(id) AND deleted = false;

-- name: UserUpdatePassword :exec
UPDATE users SET password = sqlc.arg(password)
WHERE id = sqlc.arg(id) AND deleted = false;

-- name: UserList :many
SELECT * FROM users WHERE deleted = false ORDER BY name, id;

-- name: UserUpdateEmail :exec
UPDATE users SET email = sqlc.arg(email) WHERE id = sqlc.arg(id) AND deleted = false;

-- name: UserUpdateAdminFlags :exec
UPDATE users SET is_admin = sqlc.arg(is_admin), is_super_admin = sqlc.arg(is_super_admin) WHERE id = sqlc.arg(id) AND deleted = false;
