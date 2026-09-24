DROP INDEX IF EXISTS idx_files_tree_id;
DROP INDEX IF EXISTS idx_tree_nodes_parent;
DROP INDEX IF EXISTS idx_tree_nodes_hash;

-- Restore the original tree_nodes table with a UNIQUE hash constraint. Note
-- that this schema is not compatible with content-addressed copy-on-write
-- pushes: identical directory content in different parents would violate the
-- constraint.

CREATE TABLE tree_nodes_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB UNIQUE NOT NULL,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    parent_tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO tree_nodes_old SELECT id, hash, name, mode, parent_tree_id, created_at FROM tree_nodes;
DROP TABLE tree_nodes;
ALTER TABLE tree_nodes_old RENAME TO tree_nodes;