-- name: OrgMemberUpsert :exec
INSERT INTO org_members (org_id, user_id, role) VALUES (?, ?, ?)
ON CONFLICT(org_id, user_id) DO UPDATE SET role = excluded.role, updated_at = CURRENT_TIMESTAMP;

-- name: OrgMemberGet :one
SELECT role FROM org_members WHERE org_id = ? AND user_id = ?;

-- name: OrgMemberList :many
SELECT u.id, u.name, u.email, u.photo_url, u.is_admin, u.is_super_admin, om.role, om.created_at
FROM org_members om
JOIN users u ON u.id = om.user_id
WHERE om.org_id = ? AND u.deleted = false
ORDER BY u.name, u.id;

-- name: OrgMemberListForUser :many
SELECT o.id, o.name, o.slug, o.created_at, o.updated_at, o.deleted, o.deleted_at, om.role
FROM org_members om
JOIN organizations o ON o.id = om.org_id
WHERE om.user_id = ? AND o.deleted = false
ORDER BY o.name, o.id;

-- name: OrgMemberDelete :exec
DELETE FROM org_members WHERE org_id = ? AND user_id = ?;

-- name: OrgMemberCountByRole :one
SELECT COUNT(*) FROM org_members WHERE org_id = ? AND role = ?;
