ALTER TABLE refresh_tokens ADD COLUMN token_hash TEXT;

ALTER TABLE refresh_tokens ADD COLUMN previous_hash TEXT;

ALTER TABLE refresh_tokens ADD COLUMN rotated_at TIMESTAMP;

ALTER TABLE refresh_tokens ADD COLUMN session_started_at TIMESTAMP;
