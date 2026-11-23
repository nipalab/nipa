-- SQLite cannot drop a column that takes part in a foreign key: rebuild
-- chunks without the pack columns, keeping every id so file_chunks still
-- resolves. Packed rows have no loose object to fall back to and are dropped.
DROP INDEX IF EXISTS idx_chunks_pack_id;

CREATE TABLE chunks_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    hash BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, hash)
);

INSERT INTO chunks_old (id, project_id, hash, size_bytes, created_at)
SELECT id, project_id, hash, size_bytes, created_at FROM chunks
WHERE pack_id IS NULL;

DROP TABLE chunks;
ALTER TABLE chunks_old RENAME TO chunks;
DROP TABLE chunk_packs;
