-- name: MaintenanceList :many
SELECT w.id, w.org_id, w.project_id, w.mode, w.message, w.starts_at, w.ends_at, w.notify, w.created_by, w.updated_at,
       CAST(COALESCE(o.slug, po.slug, '') AS TEXT) AS org_slug,
       CAST(COALESCE(p.slug, '') AS TEXT) AS project_slug
FROM maintenance_windows w
LEFT JOIN organizations o ON o.id = w.org_id
LEFT JOIN projects p ON p.id = w.project_id
LEFT JOIN organizations po ON po.id = p.org_id
WHERE (w.org_id IS NULL AND w.project_id IS NULL)
   OR (w.org_id IS NOT NULL AND o.deleted = false)
   OR (w.project_id IS NOT NULL AND p.deleted = false AND po.deleted = false)
ORDER BY w.id;

-- name: MaintenanceGetServer :one
SELECT * FROM maintenance_windows WHERE org_id IS NULL AND project_id IS NULL;

-- name: MaintenanceGetOrg :one
SELECT w.id, w.org_id, w.project_id, w.mode, w.message, w.starts_at, w.ends_at, w.notify, w.created_by, w.updated_at,
       o.slug AS org_slug
FROM maintenance_windows w
JOIN organizations o ON o.id = w.org_id
WHERE w.org_id = sqlc.arg(org_id) AND o.deleted = false;

-- name: MaintenanceGetProject :one
SELECT w.id, w.org_id, w.project_id, w.mode, w.message, w.starts_at, w.ends_at, w.notify, w.created_by, w.updated_at,
       o.slug AS org_slug, p.slug AS project_slug
FROM maintenance_windows w
JOIN projects p ON p.id = w.project_id
JOIN organizations o ON o.id = p.org_id
WHERE w.project_id = sqlc.arg(project_id) AND p.deleted = false AND o.deleted = false;

-- name: MaintenanceUpsertServer :one
INSERT INTO maintenance_windows (id, org_id, project_id, mode, message, starts_at, ends_at, notify, created_by, updated_at)
VALUES (sqlc.arg(id), NULL, NULL, sqlc.arg(mode), sqlc.arg(message), sqlc.arg(starts_at), sqlc.narg(ends_at), sqlc.arg(notify), sqlc.arg(created_by), sqlc.arg(updated_at))
ON CONFLICT ((org_id IS NULL AND project_id IS NULL)) WHERE org_id IS NULL AND project_id IS NULL DO UPDATE SET
    mode = excluded.mode,
    message = excluded.message,
    starts_at = excluded.starts_at,
    ends_at = excluded.ends_at,
    notify = excluded.notify,
    updated_at = excluded.updated_at
RETURNING *;

-- name: MaintenanceUpsertOrg :one
INSERT INTO maintenance_windows (id, org_id, project_id, mode, message, starts_at, ends_at, notify, created_by, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(org_id), NULL, sqlc.arg(mode), sqlc.arg(message), sqlc.arg(starts_at), sqlc.narg(ends_at), sqlc.arg(notify), sqlc.arg(created_by), sqlc.arg(updated_at))
ON CONFLICT (org_id) WHERE org_id IS NOT NULL DO UPDATE SET
    mode = excluded.mode,
    message = excluded.message,
    starts_at = excluded.starts_at,
    ends_at = excluded.ends_at,
    notify = excluded.notify,
    updated_at = excluded.updated_at
RETURNING *;

-- name: MaintenanceUpsertProject :one
INSERT INTO maintenance_windows (id, org_id, project_id, mode, message, starts_at, ends_at, notify, created_by, updated_at)
VALUES (sqlc.arg(id), NULL, sqlc.arg(project_id), sqlc.arg(mode), sqlc.arg(message), sqlc.arg(starts_at), sqlc.narg(ends_at), sqlc.arg(notify), sqlc.arg(created_by), sqlc.arg(updated_at))
ON CONFLICT (project_id) WHERE project_id IS NOT NULL DO UPDATE SET
    mode = excluded.mode,
    message = excluded.message,
    starts_at = excluded.starts_at,
    ends_at = excluded.ends_at,
    notify = excluded.notify,
    updated_at = excluded.updated_at
RETURNING *;

-- name: MaintenanceDelete :execrows
DELETE FROM maintenance_windows WHERE id = sqlc.arg(id);

-- name: MaintenanceRecipientsServer :many
SELECT u.id, u.name, u.email
FROM users u
WHERE u.deleted = false
  AND u.email_verified = true
  AND u.id IN (
      SELECT om.user_id
      FROM org_members om
      JOIN organizations o ON o.id = om.org_id
      WHERE om.role = 'owner' AND o.deleted = false
  )
ORDER BY u.id;

-- name: MaintenanceRecipientsOrg :many
SELECT u.id, u.name, u.email
FROM users u
WHERE u.deleted = false
  AND u.email_verified = true
  AND u.id IN (
      SELECT om.user_id
      FROM org_members om
      JOIN organizations o ON o.id = om.org_id
      WHERE om.org_id = sqlc.arg(org_id) AND o.deleted = false AND om.role = 'owner'
      UNION
      SELECT r.user_id
      FROM pbac_rules r
      JOIN projects p ON p.org_id = sqlc.arg(org_id)
      JOIN organizations o ON o.id = p.org_id
      WHERE p.deleted = false AND o.deleted = false
        AND r.user_id IS NOT NULL
        AND (r.project_id = p.id OR (r.project_id IS NULL AND r.org_id = p.org_id))
        AND r.path_prefix = ''
        AND (r.permission & 65536) <> 0
      UNION
      SELECT gm.user_id
      FROM pbac_rules r
      JOIN projects p ON p.org_id = sqlc.arg(org_id)
      JOIN organizations o ON o.id = p.org_id
      JOIN groups g ON g.id = r.group_id AND g.org_id = r.org_id AND g.deleted = false
      JOIN group_members gm ON gm.group_id = g.id
      WHERE p.deleted = false AND o.deleted = false
        AND (r.project_id = p.id OR (r.project_id IS NULL AND r.org_id = p.org_id))
        AND r.path_prefix = ''
        AND (r.permission & 65536) <> 0
  )
ORDER BY u.id;

-- name: MaintenanceRecipientsProject :many
SELECT u.id, u.name, u.email
FROM users u
WHERE u.deleted = false
  AND u.email_verified = true
  AND u.id IN (
      SELECT om.user_id
      FROM org_members om
      JOIN projects p ON p.org_id = om.org_id
      JOIN organizations o ON o.id = p.org_id
      WHERE p.id = sqlc.arg(project_id) AND p.deleted = false AND o.deleted = false AND om.role = 'owner'
      UNION
      SELECT r.user_id
      FROM pbac_rules r
      JOIN projects p ON p.id = sqlc.arg(project_id)
      JOIN organizations o ON o.id = p.org_id
      WHERE p.deleted = false AND o.deleted = false
        AND r.user_id IS NOT NULL
        AND (r.project_id = p.id OR (r.project_id IS NULL AND r.org_id = p.org_id))
        AND r.path_prefix = ''
        AND (r.permission & 65536) <> 0
      UNION
      SELECT gm.user_id
      FROM pbac_rules r
      JOIN projects p ON p.id = sqlc.arg(project_id)
      JOIN organizations o ON o.id = p.org_id
      JOIN groups g ON g.id = r.group_id AND g.org_id = r.org_id AND g.deleted = false
      JOIN group_members gm ON gm.group_id = g.id
      WHERE p.deleted = false AND o.deleted = false
        AND (r.project_id = p.id OR (r.project_id IS NULL AND r.org_id = p.org_id))
        AND r.path_prefix = ''
        AND (r.permission & 65536) <> 0
  )
ORDER BY u.id;
