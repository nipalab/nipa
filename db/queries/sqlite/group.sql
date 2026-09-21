-- name: GroupCreate :one
INSERT INTO groups (id, org_id, name, description)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: GroupGet :one
SELECT * FROM groups WHERE id = ? AND deleted = false LIMIT 1;

-- name: GroupListByOrg :many
SELECT * FROM groups WHERE org_id = ? AND deleted = false ORDER BY name;

-- name: GroupListByUser :many
SELECT g.* FROM groups g
JOIN group_members gm ON gm.group_id = g.id
WHERE gm.user_id = ? AND g.deleted = false
ORDER BY g.name;

-- name: GroupMemberAdd :exec
INSERT OR IGNORE INTO group_members (group_id, user_id) VALUES (?, ?);

-- name: GroupMemberRemove :exec
DELETE FROM group_members WHERE group_id = ? AND user_id = ?;

-- name: GroupMemberList :many
SELECT gm.user_id, u.name, u.email
FROM group_members gm
JOIN users u ON u.id = gm.user_id
WHERE gm.group_id = ? AND u.deleted = false
ORDER BY u.name, u.id;
