-- Tree hashes now include the file mode class (internal/treehash). Stored
-- hashes of trees holding executable or read-only files are recomputed by a
-- Go backfill at server startup (usecase.BackfillTreeHashes), which keeps the
-- highest tree node id it has covered here, so a later run only looks at
-- newer rows.
CREATE TABLE tree_hash_backfill (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    watermark INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME
);

INSERT INTO tree_hash_backfill (id, watermark) VALUES (1, 0);

-- The pre-backfill hash of a root tree whose hash the backfill changed, so a
-- client that still sends the old base tree hash on push is not refused.
CREATE TABLE tree_hash_legacy (
    tree_id INTEGER PRIMARY KEY REFERENCES tree_nodes(id) ON DELETE CASCADE,
    hash BLOB NOT NULL
);
