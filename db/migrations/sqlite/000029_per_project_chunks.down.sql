-- Back to one global row per hash. This only works while no hash is stored
-- by two projects (UNIQUE (hash) fails otherwise); the upload markers keep
-- the project links.
INSERT INTO project_chunk_uploads (project_id, hash)
SELECT project_id, hash FROM chunks
WHERE true
ON CONFLICT (project_id, hash) DO NOTHING;

CREATE TABLE chunks_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB UNIQUE NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO chunks_old (id, hash, size_bytes, created_at)
SELECT id, hash, size_bytes, created_at FROM chunks;

DROP TABLE chunks;
ALTER TABLE chunks_old RENAME TO chunks;
