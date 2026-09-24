-- Push uses content-addressed copy-on-write trees: identical directory content
-- hashes may legitimately appear in multiple rows, so the UNIQUE constraint on
-- tree_nodes.hash is removed. Identity is guaranteed by (parent_tree_id, name).

CREATE TABLE tree_nodes_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB NOT NULL,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    parent_tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(parent_tree_id, name)
);

INSERT INTO tree_nodes_new SELECT id, hash, name, mode, parent_tree_id, created_at FROM tree_nodes;
DROP TABLE tree_nodes;
ALTER TABLE tree_nodes_new RENAME TO tree_nodes;

-- Manifests resolve directory content through the tree hash (content-addressed
-- rows may be shared across commits), so both lookups need an index.
CREATE INDEX idx_tree_nodes_hash ON tree_nodes(hash);
CREATE INDEX idx_tree_nodes_parent ON tree_nodes(parent_tree_id);
CREATE INDEX idx_files_tree_id ON files(tree_id);