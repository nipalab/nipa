DELETE FROM refresh_tokens WHERE token_hash IS NOT NULL;

ALTER TABLE refresh_tokens DROP COLUMN session_started_at;

ALTER TABLE refresh_tokens DROP COLUMN rotated_at;

ALTER TABLE refresh_tokens DROP COLUMN previous_hash;

ALTER TABLE refresh_tokens DROP COLUMN token_hash;
