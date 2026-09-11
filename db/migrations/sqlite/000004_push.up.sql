-- Push uses content-addressed copy-on-write trees: identical directory and
-- file content hashes may legitimately appear in multiple rows, so the UNIQUE
-- constraints on tree_nodes.hash and files.hash are removed. Identity is
-- guaranteed by (parent_tree_id, name) for trees and (tree_id, name) for files.

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

CREATE TABLE files_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE,
    hash BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tree_id, name)
);

INSERT INTO files_new SELECT id, name, mode, tree_id, hash, size_bytes, is_binary, created_at FROM files;
DROP TABLE files;
ALTER TABLE files_new RENAME TO files;