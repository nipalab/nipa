-- name: ListPurgeableProjects :many
SELECT id FROM projects
WHERE deleted = true AND deleted_at < sqlc.arg(cutoff)::timestamptz
ORDER BY deleted_at, id
LIMIT sqlc.arg('limit')::bigint;

-- name: LockPurgeableProject :one
SELECT id, org_id, slug, name, deleted_at FROM projects
WHERE id = sqlc.arg(id) AND deleted = true AND deleted_at < sqlc.arg(cutoff)::timestamptz
FOR UPDATE;

-- name: PurgeProjectFileLocks :execrows
DELETE FROM file_locks WHERE project_id = sqlc.arg(project_id);

-- name: PurgeProjectMergeRequests :execrows
DELETE FROM merge_requests WHERE project_id = sqlc.arg(project_id);

-- name: PurgeProjectTags :execrows
DELETE FROM tags WHERE project_id = sqlc.arg(project_id);

-- name: PurgeProjectBranches :execrows
DELETE FROM branches WHERE project_id = sqlc.arg(project_id);

-- name: DetachProjectCommitParents :execrows
UPDATE commits SET parent_1_id = NULL, parent_2_id = NULL
WHERE id IN (
    SELECT c.id FROM commits c
    WHERE c.project_id = sqlc.arg(project_id)
      AND (c.parent_1_id IS NOT NULL OR c.parent_2_id IS NOT NULL)
    LIMIT sqlc.arg('limit')::bigint
);

-- name: PurgeProjectCommitBatch :many
DELETE FROM commits
WHERE id IN (
    SELECT c.id FROM commits c
    WHERE c.project_id = sqlc.arg(project_id)
    ORDER BY c.id
    LIMIT sqlc.arg('limit')::bigint
)
RETURNING tree_id;

-- name: PurgeTreeRoots :execrows
DELETE FROM tree_nodes t
WHERE t.id = ANY(sqlc.arg(tree_ids)::bigint[])
  AND NOT EXISTS (SELECT 1 FROM commits c WHERE c.tree_id = t.id);

-- name: PurgeProjectChunkUploads :execrows
DELETE FROM project_chunk_uploads d
WHERE d.project_id = sqlc.arg(project_id)
  AND d.hash IN (
      SELECT u.hash FROM project_chunk_uploads u
      WHERE u.project_id = sqlc.arg(project_id)
      LIMIT sqlc.arg('limit')::bigint
  );

-- name: PurgeProjectChunkRows :execrows
DELETE FROM chunks d
WHERE d.id IN (
    SELECT c.id FROM chunks c
    WHERE c.project_id = sqlc.arg(project_id)
      AND NOT EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
    ORDER BY c.id
    LIMIT sqlc.arg('limit')::bigint
);

-- name: IsProjectOnOwnStorage :one
SELECT EXISTS (SELECT 1 FROM project_storage WHERE project_id = sqlc.arg(project_id))::BOOLEAN AS external;

-- name: PurgeProjectRow :execrows
DELETE FROM projects WHERE id = sqlc.arg(id) AND deleted = true;
