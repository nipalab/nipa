-- Chunk packs: a bundle upload on a store with packs (S3/R2 in proxy mode,
-- CHUNK_PACKS=on) is written as one object packs/<project>/<pack id> holding
-- many chunks. chunk_packs has one row per pack object; a chunk row points at
-- its pack and offset (pack_id/pack_offset), or at nothing for a loose object
-- (p/<project>/ab/cdef...). The chunk's length is chunks.size_bytes.
--
-- state: 'pending' while the object is written (a failure leaves the row for
-- the garbage collector), 'stored' once it is durable, 'retired' after
-- compaction moved its live chunks elsewhere.
CREATE TABLE chunk_packs (
    id           BIGINT PRIMARY KEY,
    project_id   BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    size_bytes   BIGINT NOT NULL,
    chunk_count  BIGINT NOT NULL,
    state        TEXT NOT NULL CHECK (state IN ('pending', 'stored', 'retired')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    gc_marked_at TIMESTAMPTZ
);

ALTER TABLE chunks ADD COLUMN pack_id BIGINT REFERENCES chunk_packs(id);
ALTER TABLE chunks ADD COLUMN pack_offset BIGINT;
ALTER TABLE chunks ADD CONSTRAINT chunks_pack_location CHECK ((pack_id IS NULL) = (pack_offset IS NULL));

CREATE INDEX idx_chunks_pack_id ON chunks (pack_id) WHERE pack_id IS NOT NULL;
CREATE INDEX idx_chunk_packs_project ON chunk_packs (project_id);
CREATE INDEX idx_chunk_packs_gc ON chunk_packs (gc_marked_at) WHERE gc_marked_at IS NOT NULL;
