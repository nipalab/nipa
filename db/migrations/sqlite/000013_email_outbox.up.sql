CREATE TABLE email_deliveries (
    id              INTEGER PRIMARY KEY,
    event           TEXT NOT NULL,
    project_id      INTEGER NOT NULL,
    user_id         INTEGER NOT NULL,
    email           TEXT NOT NULL,
    subject         TEXT NOT NULL DEFAULT '',
    body            BLOB NOT NULL,
    state           TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME,
    last_error      TEXT NOT NULL DEFAULT '',
    claimed_at      DATETIME,
    delivered_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (state IN ('pending', 'sending', 'delivered', 'failed'))
);

CREATE INDEX idx_email_deliveries_due ON email_deliveries (state, next_attempt_at);

CREATE INDEX idx_email_deliveries_project ON email_deliveries (project_id, id DESC);
