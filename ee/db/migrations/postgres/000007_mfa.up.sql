CREATE TABLE user_totp (
    user_id      BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_enc   BYTEA NOT NULL,
    confirmed_at TIMESTAMPTZ,
    last_step    BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE mfa_recovery_codes (
    user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at   TIMESTAMPTZ,
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE mfa_login_challenges (
    token_hash TEXT PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method     TEXT NOT NULL,
    attempts   BIGINT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_mfa_login_challenges_expires ON mfa_login_challenges(expires_at);

ALTER TABLE refresh_tokens
    ADD COLUMN auth_method TEXT,
    ADD COLUMN mfa TEXT;
