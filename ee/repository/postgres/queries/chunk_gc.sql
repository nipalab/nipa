-- name: UnmarkChunks :execrows
UPDATE chunks SET gc_marked_at = NULL
WHERE project_id = sqlc.arg(project_id)
  AND hash = ANY(sqlc.arg(hashes)::bytea[])
  AND gc_marked_at IS NOT NULL;

-- name: MaxChunkID :one
SELECT COALESCE(MAX(id), 0)::BIGINT AS max_id FROM chunks;

-- name: MarkUnreferencedChunks :many
UPDATE chunks c SET gc_marked_at = sqlc.arg(marked_at)::timestamptz
WHERE c.id > sqlc.arg(after_id) AND c.id <= sqlc.arg(upto_id)
  AND c.gc_marked_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM project_chunk_uploads u WHERE u.project_id = c.project_id AND u.hash = c.hash)
RETURNING c.size_bytes;

-- name: LockSweepableChunks :many
SELECT c.id, c.project_id, c.hash, c.size_bytes, c.pack_id FROM chunks c
WHERE c.gc_marked_at IS NOT NULL AND c.gc_marked_at < sqlc.arg(cutoff)::timestamptz
  AND NOT EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM project_chunk_uploads u WHERE u.project_id = c.project_id AND u.hash = c.hash)
ORDER BY c.id
LIMIT sqlc.arg('limit')::bigint
FOR UPDATE OF c SKIP LOCKED;

-- name: CountSweepableChunks :one
SELECT COUNT(*)::BIGINT AS chunks, COALESCE(SUM(c.size_bytes), 0)::BIGINT AS bytes FROM chunks c
WHERE c.gc_marked_at IS NOT NULL AND c.gc_marked_at < sqlc.arg(cutoff)::timestamptz
  AND NOT EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM project_chunk_uploads u WHERE u.project_id = c.project_id AND u.hash = c.hash);

-- name: DeleteChunksByIDs :execrows
DELETE FROM chunks WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: ExistingProjectChunks :many
SELECT c.project_id, c.hash
FROM chunks c
WHERE (c.project_id, c.hash) IN (
    SELECT unnest(sqlc.arg(project_ids)::bigint[]), unnest(sqlc.arg(hashes)::bytea[])
);

-- name: AdoptOrphanChunks :execrows
INSERT INTO chunks (project_id, hash, size_bytes, gc_marked_at)
SELECT k.project_id, k.hash, k.size_bytes, sqlc.arg(marked_at)::timestamptz
FROM (
    SELECT unnest(sqlc.arg(project_ids)::bigint[]) AS project_id,
           unnest(sqlc.arg(hashes)::bytea[]) AS hash,
           unnest(sqlc.arg(sizes)::bigint[]) AS size_bytes
) AS k
WHERE EXISTS (SELECT 1 FROM projects p WHERE p.id = k.project_id)
ON CONFLICT (project_id, hash) DO NOTHING;

-- name: MarkChunkPacks :many
UPDATE chunk_packs p SET gc_marked_at = sqlc.arg(marked_at)::timestamptz
WHERE p.gc_marked_at IS NULL
  AND (p.state = 'pending'
       OR NOT EXISTS (SELECT 1 FROM chunks c WHERE c.pack_id = p.id))
RETURNING p.size_bytes;

-- name: UnmarkReferencedChunkPacks :execrows
UPDATE chunk_packs p SET gc_marked_at = NULL
WHERE p.gc_marked_at IS NOT NULL
  AND p.state <> 'pending'
  AND EXISTS (SELECT 1 FROM chunks c WHERE c.pack_id = p.id);

-- name: LockSweepableChunkPacks :many
SELECT p.id, p.project_id, p.size_bytes FROM chunk_packs p
WHERE p.gc_marked_at IS NOT NULL AND p.gc_marked_at < sqlc.arg(cutoff)::timestamptz
  AND (p.state = 'pending'
       OR NOT EXISTS (SELECT 1 FROM chunks c WHERE c.pack_id = p.id))
ORDER BY p.id
LIMIT sqlc.arg('limit')::bigint
FOR UPDATE OF p SKIP LOCKED;

-- name: CountSweepableChunkPacks :one
SELECT COUNT(*)::BIGINT AS packs, COALESCE(SUM(p.size_bytes), 0)::BIGINT AS bytes FROM chunk_packs p
WHERE p.gc_marked_at IS NOT NULL AND p.gc_marked_at < sqlc.arg(cutoff)::timestamptz
  AND (p.state = 'pending'
       OR NOT EXISTS (SELECT 1 FROM chunks c WHERE c.pack_id = p.id));

-- name: DeleteChunkPacksByIDs :execrows
DELETE FROM chunk_packs WHERE id = ANY(sqlc.arg(ids)::bigint[]);
