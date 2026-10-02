-- name: GroupCreate :one
INSERT INTO groups (id, org_id, name, description)
VALUES (sqlc.arg(id), sqlc.arg(org_id), sqlc.arg(name), sqlc.arg(description))
RETURNING *;

-- name: GroupGet :one
SELECT * FROM groups WHERE id = sqlc.arg(id) AND deleted = false LIMIT 1;

-- name: GroupListByOrg :many
SELECT * FROM groups WHERE org_id = sqlc.arg(org_id) AND deleted = false ORDER BY name;

-- name: GroupListByUser :many
SELECT g.* FROM groups g
JOIN group_members gm ON gm.group_id = g.id
WHERE gm.user_id = sqlc.arg(user_id) AND g.deleted = false
ORDER BY g.name;

-- name: GroupMemberAdd :exec
INSERT INTO group_members (group_id, user_id) VALUES (sqlc.arg(group_id), sqlc.arg(user_id))
ON CONFLICT DO NOTHING;

-- name: GroupMemberRemove :exec
DELETE FROM group_members WHERE group_id = sqlc.arg(group_id) AND user_id = sqlc.arg(user_id);

-- name: GroupMemberList :many
SELECT gm.user_id, u.name, u.email
FROM group_members gm
JOIN users u ON u.id = gm.user_id
WHERE gm.group_id = sqlc.arg(group_id) AND u.deleted = false
ORDER BY u.name, u.id;
