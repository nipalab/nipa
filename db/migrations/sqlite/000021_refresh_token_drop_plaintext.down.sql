CREATE TABLE refresh_tokens_old (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    token_hash TEXT,
    previous_hash TEXT,
    rotated_at TIMESTAMP,
    session_started_at TIMESTAMP
);

INSERT INTO refresh_tokens_old (id, user_id, token, expires_at, created_at, token_hash, previous_hash, rotated_at, session_started_at)
SELECT id, user_id, token_hash, expires_at, created_at, token_hash, previous_hash, rotated_at, session_started_at
FROM refresh_tokens;

DELETE FROM sqlite_sequence WHERE name = 'refresh_tokens_old';

INSERT INTO sqlite_sequence (name, seq)
SELECT 'refresh_tokens_old', seq FROM sqlite_sequence WHERE name = 'refresh_tokens';

DROP TABLE refresh_tokens;

ALTER TABLE refresh_tokens_old RENAME TO refresh_tokens;

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens (user_id);
