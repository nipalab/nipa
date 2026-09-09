CREATE TABLE IF NOT EXISTS tree_nodes (
    path TEXT PRIMARY KEY,
    parent_path TEXT NOT NULL,
    hash BLOB NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    snapshot_id TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB NOT NULL UNIQUE,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    tree_path TEXT NOT NULL REFERENCES tree_nodes(path) ON DELETE CASCADE,
    hash BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    snapshot_id TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS file_chunks (
    file_path TEXT NOT NULL REFERENCES files(path) ON DELETE CASCADE,
    chunk_id INTEGER NOT NULL REFERENCES chunks(id),
    chunk_index INTEGER NOT NULL,
    PRIMARY KEY (file_path, chunk_index)
);

CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS staged_files (
    path TEXT PRIMARY KEY,
    staged_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tree_nodes_parent ON tree_nodes(parent_path);
CREATE INDEX IF NOT EXISTS idx_files_tree ON files(tree_path);
CREATE INDEX IF NOT EXISTS idx_tree_nodes_snapshot ON tree_nodes(snapshot_id);
CREATE INDEX IF NOT EXISTS idx_files_snapshot ON files(snapshot_id);