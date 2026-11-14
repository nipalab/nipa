CREATE TABLE maintenance_windows (
    id INTEGER PRIMARY KEY,
    org_id INTEGER REFERENCES organizations(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('read_only', 'full', 'frozen')),
    message TEXT NOT NULL DEFAULT '',
    starts_at DATETIME NOT NULL,
    ends_at DATETIME,
    notify BOOLEAN NOT NULL DEFAULT 0,
    created_by INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    CHECK (org_id IS NULL OR project_id IS NULL),
    CHECK ((org_id IS NULL AND project_id IS NULL) OR mode <> 'frozen')
);

CREATE UNIQUE INDEX maintenance_windows_project ON maintenance_windows (project_id) WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX maintenance_windows_org ON maintenance_windows (org_id) WHERE org_id IS NOT NULL;
CREATE UNIQUE INDEX maintenance_windows_server ON maintenance_windows ((org_id IS NULL AND project_id IS NULL))
    WHERE org_id IS NULL AND project_id IS NULL;

CREATE TRIGGER maintenance_windows_project_cleanup AFTER UPDATE OF deleted ON projects
WHEN NEW.deleted
BEGIN
    DELETE FROM maintenance_windows WHERE project_id = NEW.id;
END;

CREATE TRIGGER maintenance_windows_project_delete AFTER DELETE ON projects
BEGIN
    DELETE FROM maintenance_windows WHERE project_id = OLD.id;
END;

CREATE TRIGGER maintenance_windows_org_cleanup AFTER UPDATE OF deleted ON organizations
WHEN NEW.deleted
BEGIN
    DELETE FROM maintenance_windows WHERE org_id = NEW.id;
    DELETE FROM maintenance_windows WHERE project_id IN (SELECT id FROM projects WHERE org_id = NEW.id);
END;

CREATE TRIGGER maintenance_windows_org_delete AFTER DELETE ON organizations
BEGIN
    DELETE FROM maintenance_windows WHERE org_id = OLD.id;
    DELETE FROM maintenance_windows WHERE project_id IN (SELECT id FROM projects WHERE org_id = OLD.id);
END;
