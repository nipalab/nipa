DELETE FROM refresh_tokens WHERE token_hash IS NULL;

CREATE TABLE refresh_tokens_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    previous_hash TEXT,
    rotated_at TIMESTAMP,
    session_started_at TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO refresh_tokens_new (id, user_id, token_hash, previous_hash, rotated_at, session_started_at, expires_at, created_at)
SELECT id, user_id, token_hash, previous_hash, rotated_at, session_started_at, expires_at, created_at
FROM refresh_tokens;

DELETE FROM sqlite_sequence WHERE name = 'refresh_tokens_new';

INSERT INTO sqlite_sequence (name, seq)
SELECT 'refresh_tokens_new', seq FROM sqlite_sequence WHERE name = 'refresh_tokens';

DROP TABLE refresh_tokens;

ALTER TABLE refresh_tokens_new RENAME TO refresh_tokens;

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens (user_id);
