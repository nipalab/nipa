DELETE FROM refresh_tokens WHERE token_hash IS NOT NULL;

ALTER TABLE refresh_tokens
    DROP COLUMN session_started_at,
    DROP COLUMN rotated_at,
    DROP COLUMN previous_hash,
    DROP COLUMN token_hash;
