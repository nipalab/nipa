-- Tree hashes now include the file mode class (internal/treehash). Stored
-- hashes of trees holding executable or read-only files are recomputed by a
-- Go backfill at server startup (usecase.BackfillTreeHashes), which keeps the
-- highest tree node id it has covered and the start of its last pass here, so
-- a later run only looks at newer rows plus the rows created shortly before
-- that pass (an overlap window for writes that committed after it).
CREATE TABLE tree_hash_backfill (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    watermark INTEGER NOT NULL DEFAULT 0,
    started_at DATETIME
);

INSERT INTO tree_hash_backfill (id, watermark) VALUES (1, 0);

-- The pre-backfill hash of a root tree whose hash the backfill changed, so a
-- client that still sends the old base tree hash on push is not refused.
CREATE TABLE tree_hash_legacy (
    tree_id INTEGER PRIMARY KEY REFERENCES tree_nodes(id) ON DELETE CASCADE,
    hash BLOB NOT NULL
);

-- The new hash of every content node the backfill rewrote, keyed by its old
-- hash, when it was the oldest content node with that hash (the one reference
-- rows with the old hash resolved to). A later pass repoints reference rows
-- that an older server wrote with the old hash afterwards.
CREATE TABLE tree_hash_rewrites (
    old_hash BLOB PRIMARY KEY,
    new_hash BLOB NOT NULL
);
