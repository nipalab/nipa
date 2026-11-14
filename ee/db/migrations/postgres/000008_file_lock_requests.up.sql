CREATE TABLE file_lock_requests (
    id BIGINT PRIMARY KEY,
    lock_id BIGINT NOT NULL REFERENCES file_locks(id) ON DELETE CASCADE,
    requester_id BIGINT NOT NULL REFERENCES users(id),
    message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (lock_id, requester_id)
);

CREATE INDEX idx_file_lock_requests_requester ON file_lock_requests (requester_id);
