CREATE TABLE organizations (
    id INTEGER PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted BOOLEAN NOT NULL DEFAULT 0,
    deleted_at DATETIME,
    created_by_user_id INTEGER REFERENCES users(id)
);

CREATE TABLE projects (
    id INTEGER PRIMARY KEY,
    org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted BOOLEAN NOT NULL DEFAULT 0,
    deleted_at DATETIME,
    UNIQUE(org_id, slug)
);

INSERT INTO organizations (id, slug, name) VALUES (1, 'default', 'Default Organization');
INSERT INTO projects (id, org_id, slug, name) VALUES (1, 1, 'default', 'Default Project');