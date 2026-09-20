-- name: PBACRuleListEffective :many
SELECT r.* FROM pbac_rules r
LEFT JOIN group_members gm ON gm.group_id = r.group_id AND gm.user_id = sqlc.arg(user_id)
WHERE (
        r.project_id = sqlc.arg(project_id)
        OR (
            r.project_id IS NULL
            AND r.org_id = (SELECT projects.org_id FROM projects WHERE projects.id = sqlc.arg(project_id))
        )
    )
    AND (r.user_id = sqlc.arg(user_id) OR gm.user_id IS NOT NULL)
ORDER BY r.id;

-- name: PBACRuleListByProject :many
SELECT * FROM pbac_rules WHERE project_id = ? ORDER BY id;

-- name: PBACRuleCreate :one
INSERT INTO pbac_rules (user_id, group_id, org_id, project_id, path_prefix, permission)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: PBACRuleDelete :exec
DELETE FROM pbac_rules WHERE id = ?;

-- name: ProjectPathPermissionList :many
SELECT * FROM project_paths_permissions WHERE project_id = ? ORDER BY path_prefix;

-- name: ProjectPathPermissionUpsert :one
INSERT INTO project_paths_permissions (project_id, path_prefix, permission)
VALUES (?, ?, ?)
ON CONFLICT(project_id, path_prefix) DO UPDATE SET permission = excluded.permission
RETURNING *;

-- name: ProjectPathPermissionDelete :exec
DELETE FROM project_paths_permissions WHERE project_id = ? AND path_prefix = ?;
