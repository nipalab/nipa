-- Packed chunk rows have no loose object to fall back to; they are dropped
-- with their packs (their content is unreadable without them).
DROP INDEX IF EXISTS idx_chunks_pack_id;
DELETE FROM chunks WHERE pack_id IS NOT NULL;
ALTER TABLE chunks DROP CONSTRAINT IF EXISTS chunks_pack_location;
ALTER TABLE chunks DROP COLUMN pack_offset;
ALTER TABLE chunks DROP COLUMN pack_id;
DROP TABLE chunk_packs;
