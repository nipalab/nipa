-- Chunk packs (parity with the enterprise schema): the free server stores
-- every chunk as a loose file and never writes packs, so its rows keep
-- pack_id and pack_offset NULL. See ee/db/migrations/postgres/000021.
CREATE TABLE chunk_packs (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    size_bytes INTEGER NOT NULL,
    chunk_count INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'stored', 'retired')),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    gc_marked_at DATETIME
);

ALTER TABLE chunks ADD COLUMN pack_id INTEGER REFERENCES chunk_packs(id);
ALTER TABLE chunks ADD COLUMN pack_offset INTEGER;

CREATE INDEX idx_chunks_pack_id ON chunks (pack_id) WHERE pack_id IS NOT NULL;
CREATE INDEX idx_chunk_packs_project ON chunk_packs (project_id);
CREATE INDEX idx_chunk_packs_gc ON chunk_packs (gc_marked_at) WHERE gc_marked_at IS NOT NULL;
