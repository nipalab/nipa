-- Chunks belong to exactly one project (per-project dedup scope). The chunk
-- row is the project's link to the content now: chunks gains project_id,
-- backfilled from the ledger link (project_chunks) with the upload marker
-- (project_chunk_uploads) as fallback, UNIQUE (hash) becomes
-- UNIQUE (project_id, hash) and project_chunks is dropped (its sizes are
-- chunks.size_bytes). project_chunk_uploads stays as the upload marker.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM (
            SELECT c.hash, pc.project_id
            FROM project_chunks pc JOIN chunks c ON c.id = pc.chunk_id
            UNION
            SELECT u.hash, u.project_id FROM project_chunk_uploads u
        ) links
        GROUP BY links.hash
        HAVING COUNT(DISTINCT links.project_id) > 1
    ) THEN
        RAISE EXCEPTION 'per-project chunks: a chunk is linked to more than one project; wipe the chunk data before upgrading (docs/DEPLOY.md)';
    END IF;
END $$;

ALTER TABLE chunks ADD COLUMN project_id BIGINT REFERENCES projects(id) ON DELETE CASCADE;

UPDATE chunks c SET project_id = pc.project_id
FROM project_chunks pc
WHERE pc.chunk_id = c.id;

UPDATE chunks c SET project_id = u.project_id
FROM project_chunk_uploads u
WHERE u.hash = c.hash AND c.project_id IS NULL;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM chunks c
        WHERE c.project_id IS NULL
          AND EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id)
    ) THEN
        RAISE EXCEPTION 'per-project chunks: a chunk referenced by a file is linked to no project; wipe the chunk data before upgrading (docs/DEPLOY.md)';
    END IF;
END $$;

-- Orphans: rows no project links (adopted bucket objects, rows whose
-- confirm was refused by the quota).
DELETE FROM chunks WHERE project_id IS NULL;

ALTER TABLE chunks ALTER COLUMN project_id SET NOT NULL;
ALTER TABLE chunks DROP CONSTRAINT IF EXISTS chunks_hash_key;
ALTER TABLE chunks ADD CONSTRAINT chunks_project_hash_key UNIQUE (project_id, hash);

DROP TABLE project_chunks;
