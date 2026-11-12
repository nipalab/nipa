ALTER TABLE refresh_tokens
    ADD COLUMN token_hash TEXT,
    ADD COLUMN previous_hash TEXT,
    ADD COLUMN rotated_at TIMESTAMPTZ,
    ADD COLUMN session_started_at TIMESTAMPTZ;
