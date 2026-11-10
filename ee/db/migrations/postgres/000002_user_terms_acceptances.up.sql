CREATE TABLE user_terms_acceptances (
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    version     TEXT NOT NULL,
    source      TEXT NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, version)
);
