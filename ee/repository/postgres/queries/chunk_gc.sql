-- name: UnmarkChunks :execrows
UPDATE chunks SET gc_marked_at = NULL
WHERE hash = ANY(sqlc.arg(hashes)::bytea[]) AND gc_marked_at IS NOT NULL;

-- name: MaxChunkID :one
SELECT COALESCE(MAX(id), 0)::BIGINT AS max_id FROM chunks;

-- name: MarkUnreferencedChunks :many
UPDATE chunks c SET gc_marked_at = sqlc.arg(marked_at)::timestamptz
WHERE c.id > sqlc.arg(after_id) AND c.id <= sqlc.arg(upto_id)
  AND c.gc_marked_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM project_chunk_uploads u WHERE u.hash = c.hash)
  AND NOT EXISTS (SELECT 1 FROM project_chunks pc WHERE pc.chunk_id = c.id)
RETURNING c.size_bytes;

-- name: LockSweepableChunks :many
SELECT c.id, c.hash, c.size_bytes FROM chunks c
WHERE c.gc_marked_at IS NOT NULL AND c.gc_marked_at < sqlc.arg(cutoff)::timestamptz
  AND NOT EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM project_chunk_uploads u WHERE u.hash = c.hash)
  AND NOT EXISTS (SELECT 1 FROM project_chunks pc WHERE pc.chunk_id = c.id)
ORDER BY c.id
LIMIT sqlc.arg('limit')::bigint
FOR UPDATE OF c SKIP LOCKED;

-- name: CountSweepableChunks :one
SELECT COUNT(*)::BIGINT AS chunks, COALESCE(SUM(c.size_bytes), 0)::BIGINT AS bytes FROM chunks c
WHERE c.gc_marked_at IS NOT NULL AND c.gc_marked_at < sqlc.arg(cutoff)::timestamptz
  AND NOT EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM project_chunk_uploads u WHERE u.hash = c.hash)
  AND NOT EXISTS (SELECT 1 FROM project_chunks pc WHERE pc.chunk_id = c.id);

-- name: DeleteChunksByIDs :execrows
DELETE FROM chunks WHERE id = ANY(sqlc.arg(ids)::bigint[]);

-- name: ExistingChunkHashes :many
SELECT hash FROM chunks WHERE hash = ANY(sqlc.arg(hashes)::bytea[]);

-- name: AdoptOrphanChunks :execrows
INSERT INTO chunks (hash, size_bytes, gc_marked_at)
SELECT unnest(sqlc.arg(hashes)::bytea[]), unnest(sqlc.arg(sizes)::bigint[]), sqlc.arg(marked_at)::timestamptz
ON CONFLICT (hash) DO NOTHING;
