CREATE TABLE file_lock_requests (
    id INTEGER PRIMARY KEY,
    lock_id INTEGER NOT NULL REFERENCES file_locks(id) ON DELETE CASCADE,
    requester_id INTEGER NOT NULL REFERENCES users(id),
    message TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (lock_id, requester_id)
);

CREATE INDEX idx_file_lock_requests_requester ON file_lock_requests (requester_id);

CREATE TRIGGER file_lock_requests_cleanup AFTER DELETE ON file_locks
BEGIN
    DELETE FROM file_lock_requests WHERE lock_id = OLD.id;
END;
