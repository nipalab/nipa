CREATE TABLE merge_requests (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_branch_id INTEGER NOT NULL REFERENCES branches(id),
    target_branch_id INTEGER NOT NULL REFERENCES branches(id),
    source_branch_name TEXT NOT NULL,
    target_branch_name TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    merge_commit_id INTEGER REFERENCES commits(id),
    merge_base_commit_id INTEGER REFERENCES commits(id),
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (status IN ('open', 'merged', 'closed'))
);

CREATE INDEX idx_merge_requests_project ON merge_requests (project_id, id DESC);
