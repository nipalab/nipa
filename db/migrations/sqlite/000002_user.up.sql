CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE,
    password TEXT NOT NULL,
    photo_url TEXT,
    is_super_admin BOOLEAN NOT NULL DEFAULT 0,
    is_admin BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted BOOLEAN NOT NULL DEFAULT 0,
    deleted_at DATETIME
);

CREATE TABLE groups (
    id INTEGER PRIMARY KEY,
    org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted BOOLEAN NOT NULL DEFAULT 0,
    deleted_at DATETIME,
    UNIQUE (org_id, name)
);

CREATE TABLE group_members (
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE pbac_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    group_id INTEGER REFERENCES groups(id) ON DELETE CASCADE,
    org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    path_prefix TEXT NOT NULL DEFAULT '',
    permission INTEGER NOT NULL DEFAULT 0,
    CHECK (user_id IS NOT NULL OR group_id IS NOT NULL),
    CHECK (user_id IS NULL OR group_id IS NULL)
);

CREATE INDEX idx_pbac_rules_project ON pbac_rules(project_id);
CREATE INDEX idx_pbac_rules_user ON pbac_rules(user_id);
CREATE INDEX idx_pbac_rules_group ON pbac_rules(group_id);

CREATE TABLE project_paths_permissions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    path_prefix TEXT NOT NULL,
    permission INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, path_prefix)
);

CREATE INDEX idx_group_members_user ON group_members(user_id);

CREATE TABLE refresh_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO users (id, name, email, password, is_super_admin) VALUES (1, 'Super Admin', 'supernipa', '$2a$10$kBH8NXuoJIXiUuA7jPEXOe8cybytpUH6eR6M3EO71tZgSU2D9QncW', 1);