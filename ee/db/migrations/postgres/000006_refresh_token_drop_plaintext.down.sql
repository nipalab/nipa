ALTER TABLE refresh_tokens ADD COLUMN token TEXT;

UPDATE refresh_tokens SET token = token_hash WHERE token IS NULL;

ALTER TABLE refresh_tokens
    ALTER COLUMN token SET NOT NULL,
    ADD CONSTRAINT refresh_tokens_token_key UNIQUE (token),
    DROP CONSTRAINT refresh_tokens_token_hash_key,
    ALTER COLUMN token_hash DROP NOT NULL;
