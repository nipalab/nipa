CREATE TABLE ssh_login_challenges (
    email TEXT NOT NULL,
    nonce TEXT NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
