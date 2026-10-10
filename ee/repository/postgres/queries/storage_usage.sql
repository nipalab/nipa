-- name: GetBillingOwner :one
SELECT user_id FROM org_billing_owner WHERE org_id = sqlc.arg(org_id);

-- name: GetOrgCreatedBy :one
SELECT created_by_user_id FROM organizations WHERE id = sqlc.arg(org_id);

-- name: EnsureBillingOwner :one
INSERT INTO org_billing_owner (org_id, user_id)
VALUES (sqlc.arg(org_id), sqlc.arg(user_id))
ON CONFLICT (org_id) DO UPDATE SET user_id = org_billing_owner.user_id
RETURNING user_id;

-- name: UpsertBillingOwner :exec
INSERT INTO org_billing_owner (org_id, user_id)
VALUES (sqlc.arg(org_id), sqlc.arg(user_id))
ON CONFLICT (org_id) DO UPDATE SET user_id = EXCLUDED.user_id;

-- name: GetOwnerQuota :one
SELECT quota_bytes FROM owner_quota WHERE user_id = sqlc.arg(user_id);

-- name: SetOwnerQuota :exec
INSERT INTO owner_quota (user_id, quota_bytes, updated_at)
VALUES (sqlc.arg(user_id), sqlc.narg(quota_bytes)::bigint, now())
ON CONFLICT (user_id) DO UPDATE SET quota_bytes = EXCLUDED.quota_bytes, updated_at = now();

-- name: GetActiveUserID :one
SELECT id FROM users WHERE id = sqlc.arg(id) AND deleted = false;

-- name: GetProjectOrgID :one
SELECT org_id FROM projects WHERE id = sqlc.arg(id);

-- name: GetOwnerUsage :one
SELECT COALESCE(SUM(u.used_bytes), 0)::BIGINT AS used_bytes
FROM org_usage u
JOIN org_billing_owner b ON b.org_id = u.org_id
WHERE b.user_id = sqlc.arg(user_id);

-- name: GetOrgUsage :one
SELECT COALESCE((SELECT used_bytes FROM org_usage WHERE org_id = sqlc.arg(org_id)), 0)::BIGINT AS used_bytes;

-- name: ListProjectUsage :many
SELECT p.id AS project_id, p.slug, p.name, p.deleted,
       COALESCE(u.used_bytes, 0)::BIGINT AS used_bytes
FROM projects p
LEFT JOIN project_usage u ON u.project_id = p.id
WHERE p.org_id = sqlc.arg(org_id)
ORDER BY used_bytes DESC, p.slug;

-- name: ListOwnerOrgUsage :many
SELECT o.id AS org_id, o.slug, o.name,
       COALESCE(u.used_bytes, 0)::BIGINT AS used_bytes
FROM org_billing_owner b
JOIN organizations o ON o.id = b.org_id
LEFT JOIN org_usage u ON u.org_id = b.org_id
WHERE b.user_id = sqlc.arg(user_id)
ORDER BY used_bytes DESC, o.slug;

-- name: GetChunkIDByHash :one
SELECT id, size_bytes FROM chunks WHERE hash = sqlc.arg(hash);

-- name: GetProjectChunkSizeByHash :one
SELECT pc.size_bytes
FROM project_chunks pc
JOIN chunks ch ON ch.id = pc.chunk_id
WHERE pc.project_id = sqlc.arg(project_id) AND ch.hash = sqlc.arg(hash);

-- name: InsertProjectChunk :one
INSERT INTO project_chunks (project_id, chunk_id, size_bytes)
VALUES (sqlc.arg(project_id), sqlc.arg(chunk_id), sqlc.arg(size_bytes))
ON CONFLICT (project_id, chunk_id) DO NOTHING
RETURNING size_bytes;

-- name: AddProjectUsage :exec
INSERT INTO project_usage (project_id, used_bytes)
VALUES (sqlc.arg(project_id), sqlc.arg(delta))
ON CONFLICT (project_id) DO UPDATE SET used_bytes = project_usage.used_bytes + EXCLUDED.used_bytes;

-- name: AddOrgUsage :exec
INSERT INTO org_usage (org_id, used_bytes)
VALUES (sqlc.arg(org_id), sqlc.arg(delta))
ON CONFLICT (org_id) DO UPDATE SET used_bytes = org_usage.used_bytes + EXCLUDED.used_bytes;

-- name: ListProjectIDsByOrg :many
SELECT id FROM projects WHERE org_id = sqlc.arg(org_id);

-- name: DeleteProjectChunks :exec
DELETE FROM project_chunks WHERE project_id = sqlc.arg(project_id);

-- name: RebuildProjectChunks :exec
WITH RECURSIVE tree_ids(id) AS (
    SELECT tree_id FROM commits WHERE project_id = sqlc.arg(project_id)
    UNION
    SELECT tn.id FROM tree_nodes tn JOIN tree_ids ti ON tn.parent_tree_id = ti.id
)
INSERT INTO project_chunks (project_id, chunk_id, size_bytes)
SELECT DISTINCT sqlc.arg(project_id), fc.chunk_id, ch.size_bytes
FROM files f
JOIN file_chunks fc ON fc.file_id = f.id
JOIN chunks ch ON ch.id = fc.chunk_id
WHERE f.tree_id IN (SELECT id FROM tree_ids)
ON CONFLICT (project_id, chunk_id) DO NOTHING;

-- name: SetProjectUsage :exec
INSERT INTO project_usage (project_id, used_bytes)
SELECT sqlc.arg(project_id), COALESCE(SUM(size_bytes), 0)::BIGINT
FROM project_chunks
WHERE project_id = sqlc.arg(project_id)
ON CONFLICT (project_id) DO UPDATE SET used_bytes = EXCLUDED.used_bytes;

-- name: SetOrgUsageFromProjects :exec
INSERT INTO org_usage (org_id, used_bytes)
SELECT sqlc.arg(org_id), COALESCE(SUM(COALESCE(pu.used_bytes, 0)), 0)::BIGINT
FROM projects p
LEFT JOIN project_usage pu ON pu.project_id = p.id
WHERE p.org_id = sqlc.arg(org_id)
ON CONFLICT (org_id) DO UPDATE SET used_bytes = EXCLUDED.used_bytes;
