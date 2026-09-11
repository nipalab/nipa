-- Restore the original files table with a UNIQUE hash constraint. Note that
-- this schema is not compatible with content-addressed copy-on-write pushes:
-- identical file content in different directories would violate the constraint.

CREATE TABLE files_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE,
    hash BLOB UNIQUE NOT NULL,
    size_bytes INTEGER NOT NULL,
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO files_old SELECT id, name, mode, tree_id, hash, size_bytes, is_binary, created_at FROM files;
DROP TABLE files;
ALTER TABLE files_old RENAME TO files;

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