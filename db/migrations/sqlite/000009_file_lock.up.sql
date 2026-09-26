CREATE TABLE file_locks (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    branch_id INTEGER REFERENCES branches(id),
    path TEXT NOT NULL,
    held_by INTEGER NOT NULL REFERENCES users(id),
    merge_request_id INTEGER REFERENCES merge_requests(id),
    acquired_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX idx_file_locks_global ON file_locks (project_id, path) WHERE branch_id IS NULL;

CREATE UNIQUE INDEX idx_file_locks_scoped ON file_locks (project_id, branch_id, path) WHERE branch_id IS NOT NULL;

CREATE INDEX idx_file_locks_project ON file_locks (project_id);

CREATE INDEX idx_file_locks_branch ON file_locks (branch_id);
