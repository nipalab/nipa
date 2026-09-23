CREATE TABLE merge_request_threads (
    id INTEGER PRIMARY KEY,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    file_path TEXT,
    old_line INTEGER,
    new_line INTEGER,
    base_commit_id INTEGER REFERENCES commits(id),
    head_commit_id INTEGER REFERENCES commits(id),
    resolved BOOLEAN NOT NULL DEFAULT 0,
    resolved_by INTEGER REFERENCES users(id),
    resolved_at DATETIME,
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merge_request_threads_mr ON merge_request_threads (merge_request_id, id);

CREATE TABLE merge_request_comments (
    id INTEGER PRIMARY KEY,
    thread_id INTEGER NOT NULL REFERENCES merge_request_threads(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id),
    body TEXT NOT NULL,
    system BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_merge_request_comments_thread ON merge_request_comments (thread_id, id);
