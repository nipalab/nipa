-- Back to one global row per hash plus ledger links. This only works while
-- no hash is stored by two projects (UNIQUE (hash) fails otherwise).
CREATE TABLE project_chunks (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    chunk_id   BIGINT NOT NULL REFERENCES chunks(id),
    size_bytes BIGINT NOT NULL,
    PRIMARY KEY (project_id, chunk_id)
);
CREATE INDEX IF NOT EXISTS idx_project_chunks_chunk_id ON project_chunks (chunk_id);

INSERT INTO project_chunks (project_id, chunk_id, size_bytes)
SELECT project_id, id, size_bytes FROM chunks;

INSERT INTO project_chunk_uploads (project_id, hash)
SELECT project_id, hash FROM chunks
ON CONFLICT (project_id, hash) DO NOTHING;

ALTER TABLE chunks DROP CONSTRAINT IF EXISTS chunks_project_hash_key;
ALTER TABLE chunks ADD CONSTRAINT chunks_hash_key UNIQUE (hash);
ALTER TABLE chunks DROP COLUMN project_id;
