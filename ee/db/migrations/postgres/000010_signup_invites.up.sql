CREATE TABLE signup_invites (
    id BIGINT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    invited_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ
);
