CREATE TABLE signup_waitlist (
    id BIGINT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'registration',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    notified_at TIMESTAMPTZ
);

CREATE INDEX idx_signup_waitlist_pending ON signup_waitlist (id) WHERE notified_at IS NULL;
