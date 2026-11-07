CREATE TABLE audit_log (
    id            INTEGER PRIMARY KEY,
    occurred_at   DATETIME NOT NULL,
    actor_type    TEXT NOT NULL,
    actor_user_id INTEGER,
    actor_label   TEXT NOT NULL DEFAULT '',
    action        TEXT NOT NULL,
    org_id        INTEGER,
    project_id    INTEGER,
    target_type   TEXT NOT NULL DEFAULT '',
    target_id     TEXT NOT NULL DEFAULT '',
    metadata      TEXT NOT NULL DEFAULT '{}',
    request_id    TEXT NOT NULL DEFAULT '',
    client_ip     TEXT NOT NULL DEFAULT '',
    user_agent    TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_audit_log_id ON audit_log (id DESC);

CREATE INDEX idx_audit_log_actor ON audit_log (actor_user_id, id DESC);

CREATE INDEX idx_audit_log_org ON audit_log (org_id, id DESC);

CREATE INDEX idx_audit_log_project ON audit_log (project_id, id DESC);

CREATE INDEX idx_audit_log_action ON audit_log (action, id DESC);
