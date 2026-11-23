-- Chunks belong to exactly one project (per-project dedup scope). The chunk
-- row is the project's link to the content now: chunks gains project_id,
-- backfilled from the project's upload marker (project_chunk_uploads; the
-- free server never had project_chunks), and UNIQUE (hash) becomes
-- UNIQUE (project_id, hash). project_chunk_uploads stays as the upload marker.
--
-- Refuse to run when a chunk is linked to more than one project: a chunk row
-- can name only one. The CHECK constraint turns a found hash into an error.
CREATE TABLE chunk_scope_guard (
    hash BLOB,
    CONSTRAINT chunk_linked_to_several_projects CHECK (hash IS NULL)
);
INSERT INTO chunk_scope_guard (hash)
SELECT hash FROM project_chunk_uploads GROUP BY hash HAVING COUNT(DISTINCT project_id) > 1;
DROP TABLE chunk_scope_guard;

-- A chunk a file references must have a project; deleting it would break the
-- file. Pushes only ever reference linked chunks, so this never fires on real
-- data, but it fails loudly instead of dropping history.
CREATE TABLE chunk_orphan_guard (
    hash BLOB,
    CONSTRAINT referenced_chunk_without_project CHECK (hash IS NULL)
);
INSERT INTO chunk_orphan_guard (hash)
SELECT c.hash FROM chunks c
WHERE NOT EXISTS (SELECT 1 FROM project_chunk_uploads u WHERE u.hash = c.hash)
  AND EXISTS (SELECT 1 FROM file_chunks fc WHERE fc.chunk_id = c.id);
DROP TABLE chunk_orphan_guard;

-- SQLite cannot add a NOT NULL column or change a table constraint in place:
-- rebuild the table, keeping every id so file_chunks still resolves. Chunk
-- rows linked to no project (orphans) are not copied.
CREATE TABLE chunks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    hash BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, hash)
);

INSERT INTO chunks_new (id, project_id, hash, size_bytes, created_at)
SELECT c.id, u.project_id, c.hash, c.size_bytes, c.created_at
FROM chunks c
JOIN project_chunk_uploads u ON u.hash = c.hash;

DROP TABLE chunks;
ALTER TABLE chunks_new RENAME TO chunks;
