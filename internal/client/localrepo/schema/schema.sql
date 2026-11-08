CREATE TABLE IF NOT EXISTS tree_nodes (
    path TEXT PRIMARY KEY,
    parent_path TEXT NOT NULL,
    hash BLOB NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444
);

CREATE TABLE IF NOT EXISTS files (
    path TEXT PRIMARY KEY,
    tree_path TEXT NOT NULL REFERENCES tree_nodes(path) ON DELETE CASCADE,
    hash BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444,
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    encoding TEXT NOT NULL DEFAULT 'raw',
    chunks BLOB NOT NULL DEFAULT X''
);

CREATE TABLE IF NOT EXISTS meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS stat_cache (
    path TEXT PRIMARY KEY,
    size_bytes INTEGER NOT NULL,
    mtime_ns INTEGER NOT NULL,
    mode INTEGER NOT NULL,
    hash BLOB NOT NULL,
    cached_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS staged_files (
    path TEXT PRIMARY KEY,
    staged_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS pinned_paths (
    path TEXT PRIMARY KEY,
    pinned_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tree_nodes_parent ON tree_nodes(parent_path);
CREATE INDEX IF NOT EXISTS idx_files_tree ON files(tree_path);