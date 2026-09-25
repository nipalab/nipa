CREATE TABLE tree_nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB UNIQUE NOT NULL,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444, -- OS file mode (0444, 0644, 0755)
    parent_tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE chunks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash BLOB UNIQUE NOT NULL,
    size_bytes INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    mode INTEGER NOT NULL DEFAULT 444, -- OS file mode (0444, 0644, 0755)
    tree_id INTEGER REFERENCES tree_nodes(id) ON DELETE CASCADE,
    hash BLOB NOT NULL,
    size_bytes INTEGER NOT NULL,
    is_binary BOOLEAN NOT NULL DEFAULT FALSE,
    encoding TEXT NOT NULL DEFAULT 'raw',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(tree_id, name)
);

CREATE TABLE file_chunks (
    file_id INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    chunk_id INTEGER NOT NULL REFERENCES chunks(id),
    chunk_index INTEGER NOT NULL,
    PRIMARY KEY (file_id, chunk_index)
);

CREATE TABLE commits (
    id INTEGER PRIMARY KEY,
    hash BLOB NOT NULL UNIQUE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tree_id INTEGER NOT NULL REFERENCES tree_nodes(id),
    parent_1_id INTEGER REFERENCES commits(id),
    parent_2_id INTEGER REFERENCES commits(id),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE branches (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL, 
    key TEXT NOT NULL,
    is_protected BOOLEAN NOT NULL DEFAULT FALSE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    commit_id INTEGER REFERENCES commits(id),
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    deleted_at DATETIME
);

CREATE UNIQUE INDEX idx_branches_project_key
    ON branches (project_id, key)
    WHERE deleted = FALSE;

CREATE INDEX idx_branches_project_updated_id 
    ON branches (project_id, updated_at DESC, id DESC);

CREATE UNIQUE INDEX idx_branches_one_default_per_project 
    ON branches (project_id) 
    WHERE is_default = TRUE;

INSERT INTO branches (id, project_id, name, key, is_default) VALUES (1, 1, 'main', 'main', TRUE);