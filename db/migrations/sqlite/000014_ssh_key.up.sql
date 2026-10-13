CREATE TABLE ssh_public_keys (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT '',
    key_type TEXT NOT NULL,
    key_blob TEXT NOT NULL,
    fingerprint TEXT NOT NULL UNIQUE,
    comment TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_ssh_public_keys_user ON ssh_public_keys(user_id);
