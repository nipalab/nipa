CREATE TABLE storage_servers (
    id BIGINT PRIMARY KEY,
    owner_user_id BIGINT NOT NULL REFERENCES users(id),
    name TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    region TEXT NOT NULL DEFAULT '',
    bucket TEXT NOT NULL,
    prefix TEXT NOT NULL DEFAULT '',
    access_key_id TEXT NOT NULL,
    secret_sealed BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner_user_id, name)
);

CREATE TABLE org_storage_defaults (
    org_id BIGINT PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    storage_server_id BIGINT NOT NULL REFERENCES storage_servers(id) ON DELETE RESTRICT,
    set_by BIGINT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_org_storage_defaults_server ON org_storage_defaults (storage_server_id);

CREATE TABLE project_storage (
    project_id BIGINT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    storage_server_id BIGINT NOT NULL REFERENCES storage_servers(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_project_storage_server ON project_storage (storage_server_id);
