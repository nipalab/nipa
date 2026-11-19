-- name: CreateStorageServer :one
INSERT INTO storage_servers (id, owner_user_id, name, endpoint, region, bucket, prefix, access_key_id, secret_sealed)
VALUES (sqlc.arg(id), sqlc.arg(owner_user_id), sqlc.arg(name), sqlc.arg(endpoint), sqlc.arg(region), sqlc.arg(bucket), sqlc.arg(prefix), sqlc.arg(access_key_id), sqlc.arg(secret_sealed))
RETURNING *;

-- name: UpdateStorageServer :one
UPDATE storage_servers
SET name = sqlc.arg(name), endpoint = sqlc.arg(endpoint), region = sqlc.arg(region),
    access_key_id = sqlc.arg(access_key_id), secret_sealed = sqlc.arg(secret_sealed), updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetStorageServer :one
SELECT * FROM storage_servers WHERE id = sqlc.arg(id);

-- name: ListStorageServersByOwner :many
SELECT * FROM storage_servers WHERE owner_user_id = sqlc.arg(owner_user_id) ORDER BY name, id;

-- name: CountStorageServersByOwner :one
SELECT count(*)::BIGINT FROM storage_servers WHERE owner_user_id = sqlc.arg(owner_user_id);

-- name: LockStorageServerOwner :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

-- name: StorageServerInUse :one
SELECT (EXISTS (SELECT 1 FROM project_storage ps
                JOIN projects p ON p.id = ps.project_id
                JOIN organizations o ON o.id = p.org_id
                WHERE ps.storage_server_id = sqlc.arg(id) AND p.deleted = false AND o.deleted = false)
     OR EXISTS (SELECT 1 FROM org_storage_defaults d JOIN organizations o ON o.id = d.org_id
                WHERE d.storage_server_id = sqlc.arg(id) AND o.deleted = false))::BOOLEAN AS in_use;

-- name: DeleteDeletedOrgStorageDefaults :exec
DELETE FROM org_storage_defaults d
USING organizations o
WHERE d.org_id = o.id AND o.deleted = true AND d.storage_server_id = sqlc.arg(id);

-- name: DeleteDeletedProjectStorage :exec
DELETE FROM project_storage ps
USING projects p, organizations o
WHERE ps.project_id = p.id AND o.id = p.org_id AND ps.storage_server_id = sqlc.arg(id)
  AND (p.deleted = true OR o.deleted = true);

-- name: DeleteStorageServer :execrows
DELETE FROM storage_servers WHERE id = sqlc.arg(id);

-- name: GetOrgStorageDefault :one
SELECT storage_server_id FROM org_storage_defaults WHERE org_id = sqlc.arg(org_id);

-- name: UpsertOrgStorageDefault :exec
INSERT INTO org_storage_defaults (org_id, storage_server_id, set_by)
VALUES (sqlc.arg(org_id), sqlc.arg(storage_server_id), sqlc.arg(set_by))
ON CONFLICT (org_id) DO UPDATE SET storage_server_id = EXCLUDED.storage_server_id, set_by = EXCLUDED.set_by, created_at = now();

-- name: DeleteOrgStorageDefault :execrows
DELETE FROM org_storage_defaults WHERE org_id = sqlc.arg(org_id);

-- name: InsertProjectStorage :exec
INSERT INTO project_storage (project_id, storage_server_id)
VALUES (sqlc.arg(project_id), sqlc.arg(storage_server_id));

-- name: GetProjectStorageServer :one
SELECT s.id, s.owner_user_id, s.name, s.endpoint, s.region, s.bucket, s.prefix,
       s.access_key_id, s.secret_sealed, s.created_at, s.updated_at
FROM project_storage ps
JOIN storage_servers s ON s.id = ps.storage_server_id
WHERE ps.project_id = sqlc.arg(project_id);
