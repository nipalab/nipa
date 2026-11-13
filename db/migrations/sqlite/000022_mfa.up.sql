CREATE TABLE user_totp (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_enc BLOB NOT NULL,
    confirmed_at DATETIME,
    last_step INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE mfa_recovery_codes (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at DATETIME,
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE mfa_login_challenges (
    token_hash TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_mfa_login_challenges_expires ON mfa_login_challenges(expires_at);

ALTER TABLE refresh_tokens ADD COLUMN auth_method TEXT;

ALTER TABLE refresh_tokens ADD COLUMN mfa TEXT;
