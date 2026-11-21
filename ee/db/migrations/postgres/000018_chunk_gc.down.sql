DROP INDEX IF EXISTS idx_project_chunks_chunk_id;
DROP INDEX IF EXISTS idx_project_chunk_uploads_hash;
DROP INDEX IF EXISTS idx_chunks_gc_marked;
ALTER TABLE chunks DROP COLUMN IF EXISTS gc_marked_at;
