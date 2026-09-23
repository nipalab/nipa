CREATE TABLE merge_request_approvals (
    id INTEGER PRIMARY KEY,
    merge_request_id INTEGER NOT NULL REFERENCES merge_requests(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (merge_request_id, user_id)
);

CREATE INDEX idx_merge_request_approvals_mr ON merge_request_approvals (merge_request_id);
