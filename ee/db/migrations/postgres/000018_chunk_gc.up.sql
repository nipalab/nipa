-- Chunk GC: a chunk no file, upload marker or ledger link references is
-- marked; a mark older than the grace period is swept (object, then row).
-- Confirm and server-side uploads clear the mark before they check content.
ALTER TABLE chunks ADD COLUMN gc_marked_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_chunks_gc_marked ON chunks (gc_marked_at) WHERE gc_marked_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_project_chunk_uploads_hash ON project_chunk_uploads (hash);
CREATE INDEX IF NOT EXISTS idx_project_chunks_chunk_id ON project_chunks (chunk_id);
