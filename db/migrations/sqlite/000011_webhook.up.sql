CREATE TABLE webhooks (
    id           INTEGER PRIMARY KEY,
    project_id   INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    url          TEXT NOT NULL,
    secret       TEXT NOT NULL,
    events       TEXT NOT NULL,
    path_prefix  TEXT NOT NULL DEFAULT '',
    is_active    BOOLEAN NOT NULL DEFAULT TRUE,
    insecure_tls BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_webhooks_project ON webhooks (project_id, id);

CREATE TABLE webhook_deliveries (
    id              INTEGER PRIMARY KEY,
    webhook_id      INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    payload         BLOB NOT NULL,
    state           TEXT NOT NULL DEFAULT 'pending',
    attempt         INTEGER NOT NULL DEFAULT 0,
    response_status INTEGER,
    last_error      TEXT NOT NULL DEFAULT '',
    next_retry_at   DATETIME,
    delivered_at    DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (state IN ('pending', 'delivered', 'failed'))
);

CREATE INDEX idx_webhook_deliveries_webhook ON webhook_deliveries (webhook_id, id);

CREATE INDEX idx_webhook_deliveries_retry
    ON webhook_deliveries (state, next_retry_at) WHERE next_retry_at IS NOT NULL;
