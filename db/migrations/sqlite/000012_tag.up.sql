CREATE TABLE tags (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    key TEXT NOT NULL,
    commit_id INTEGER NOT NULL REFERENCES commits(id),
    message TEXT NOT NULL DEFAULT '',
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_tags_project_key ON tags (project_id, key);

CREATE INDEX idx_tags_project_created_id ON tags (project_id, created_at DESC, id DESC);
