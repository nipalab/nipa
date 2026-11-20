CREATE TABLE issue_trackers (
    id INTEGER PRIMARY KEY,
    org_id INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    auth_kind TEXT NOT NULL CHECK (auth_kind IN ('basic', 'bearer')),
    account TEXT NOT NULL DEFAULT '',
    secret_sealed BLOB NOT NULL,
    created_by INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (org_id, name)
);

CREATE TABLE project_issue_trackers (
    project_id INTEGER PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    tracker_id INTEGER NOT NULL REFERENCES issue_trackers(id),
    project_keys TEXT NOT NULL,
    link_merge_requests BOOLEAN NOT NULL DEFAULT 1,
    link_commits TEXT NOT NULL DEFAULT 'off' CHECK (link_commits IN ('off', 'default_branch', 'all')),
    comment_on_merge BOOLEAN NOT NULL DEFAULT 1,
    merged_status TEXT NOT NULL DEFAULT '',
    opened_status TEXT NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT 1,
    set_by INTEGER NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE INDEX idx_project_issue_trackers_tracker ON project_issue_trackers (tracker_id);

CREATE TABLE issue_links (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    tracker_id INTEGER NOT NULL REFERENCES issue_trackers(id) ON DELETE CASCADE,
    issue_key TEXT NOT NULL,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('commit', 'merge_request', 'branch')),
    subject_id INTEGER NOT NULL,
    relation TEXT NOT NULL CHECK (relation IN ('mentions', 'closes')),
    created_at DATETIME NOT NULL,
    UNIQUE (project_id, issue_key, subject_kind, subject_id)
);

CREATE INDEX idx_issue_links_subject ON issue_links (project_id, subject_kind, subject_id);

CREATE TABLE issue_tracker_jobs (
    id INTEGER PRIMARY KEY,
    tracker_id INTEGER NOT NULL REFERENCES issue_trackers(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_key TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('link', 'comment', 'transition')),
    payload TEXT NOT NULL DEFAULT '{}',
    dedupe_key TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered', 'failed')),
    attempt INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    next_retry_at DATETIME,
    delivered_at DATETIME,
    created_at DATETIME NOT NULL
);

CREATE INDEX idx_issue_tracker_jobs_retry ON issue_tracker_jobs (state, next_retry_at) WHERE next_retry_at IS NOT NULL;
CREATE INDEX idx_issue_tracker_jobs_project ON issue_tracker_jobs (project_id, id);

CREATE TRIGGER issue_trackers_delete AFTER DELETE ON issue_trackers
BEGIN
    DELETE FROM project_issue_trackers WHERE tracker_id = OLD.id;
    DELETE FROM issue_links WHERE tracker_id = OLD.id;
    DELETE FROM issue_tracker_jobs WHERE tracker_id = OLD.id;
END;

CREATE TRIGGER issue_tracker_project_cleanup AFTER UPDATE OF deleted ON projects
WHEN NEW.deleted
BEGIN
    DELETE FROM project_issue_trackers WHERE project_id = NEW.id;
    DELETE FROM issue_links WHERE project_id = NEW.id;
    DELETE FROM issue_tracker_jobs WHERE project_id = NEW.id;
END;

CREATE TRIGGER issue_tracker_project_delete AFTER DELETE ON projects
BEGIN
    DELETE FROM project_issue_trackers WHERE project_id = OLD.id;
    DELETE FROM issue_links WHERE project_id = OLD.id;
    DELETE FROM issue_tracker_jobs WHERE project_id = OLD.id;
END;

CREATE TRIGGER issue_tracker_org_cleanup AFTER UPDATE OF deleted ON organizations
WHEN NEW.deleted
BEGIN
    DELETE FROM project_issue_trackers WHERE project_id IN (SELECT id FROM projects WHERE org_id = NEW.id);
    DELETE FROM issue_trackers WHERE org_id = NEW.id;
END;

CREATE TRIGGER issue_tracker_org_delete AFTER DELETE ON organizations
BEGIN
    DELETE FROM project_issue_trackers WHERE project_id IN (SELECT id FROM projects WHERE org_id = OLD.id);
    DELETE FROM issue_trackers WHERE org_id = OLD.id;
END;
