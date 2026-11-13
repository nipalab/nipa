DELETE FROM refresh_tokens WHERE token_hash IS NULL;

ALTER TABLE refresh_tokens
    DROP COLUMN token,
    ALTER COLUMN token_hash SET NOT NULL,
    ADD CONSTRAINT refresh_tokens_token_hash_key UNIQUE (token_hash);
