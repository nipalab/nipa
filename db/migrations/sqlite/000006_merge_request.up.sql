CREATE TABLE merge_requests (
    id INTEGER PRIMARY KEY,
    number INTEGER NOT NULL,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_branch_id INTEGER NOT NULL REFERENCES branches(id),
    target_branch_id INTEGER NOT NULL REFERENCES branches(id),
    source_branch_name TEXT NOT NULL,
    target_branch_name TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    is_draft BOOLEAN NOT NULL DEFAULT FALSE,
    merge_commit_id INTEGER REFERENCES commits(id),
    merge_base_commit_id INTEGER REFERENCES commits(id),
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (status IN ('open', 'merged', 'closed'))
);

CREATE INDEX idx_merge_requests_project ON merge_requests (project_id, id DESC);

CREATE UNIQUE INDEX idx_merge_requests_project_number ON merge_requests (project_id, number);

CREATE TABLE merge_request_assignees (
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id),
    PRIMARY KEY (merge_request_id, user_id)
);

CREATE INDEX idx_merge_request_assignees_user ON merge_request_assignees (user_id);

CREATE TABLE merge_request_checks (
    id INTEGER PRIMARY KEY,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    head_commit_id INTEGER NOT NULL REFERENCES commits(id),
    name TEXT NOT NULL,
    state TEXT NOT NULL,
    details_url TEXT NOT NULL DEFAULT '',
    reporter_id INTEGER NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (state IN ('pending', 'success', 'failed')),
    UNIQUE (merge_request_id, head_commit_id, name)
);

CREATE INDEX idx_merge_request_checks_mr ON merge_request_checks (merge_request_id, head_commit_id);
