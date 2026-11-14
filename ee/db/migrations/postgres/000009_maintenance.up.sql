CREATE TABLE maintenance_windows (
    id BIGINT PRIMARY KEY,
    project_id BIGINT REFERENCES projects(id) ON DELETE CASCADE,
    mode TEXT NOT NULL CHECK (mode IN ('read_only', 'full', 'frozen')),
    message TEXT NOT NULL DEFAULT '',
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ,
    notify BOOLEAN NOT NULL DEFAULT false,
    created_by BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (project_id IS NULL OR mode <> 'frozen')
);

CREATE UNIQUE INDEX maintenance_windows_project ON maintenance_windows (project_id) WHERE project_id IS NOT NULL;
CREATE UNIQUE INDEX maintenance_windows_server ON maintenance_windows ((project_id IS NULL)) WHERE project_id IS NULL;

CREATE FUNCTION maintenance_windows_project_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM maintenance_windows WHERE project_id = NEW.id;
    RETURN NULL;
END;
$$;

CREATE TRIGGER maintenance_windows_project_cleanup AFTER UPDATE OF deleted ON projects
FOR EACH ROW WHEN (NEW.deleted)
EXECUTE FUNCTION maintenance_windows_project_cleanup();

CREATE FUNCTION maintenance_windows_org_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM maintenance_windows WHERE project_id IN (SELECT id FROM projects WHERE org_id = NEW.id);
    RETURN NULL;
END;
$$;

CREATE TRIGGER maintenance_windows_org_cleanup AFTER UPDATE OF deleted ON organizations
FOR EACH ROW WHEN (NEW.deleted)
EXECUTE FUNCTION maintenance_windows_org_cleanup();
