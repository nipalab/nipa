DROP TRIGGER issue_trackers_delete;
DROP TRIGGER issue_tracker_project_cleanup;
DROP TRIGGER issue_tracker_project_delete;

CREATE TABLE issue_tracker_jobs_new (
    id INTEGER PRIMARY KEY,
    tracker_id INTEGER NOT NULL REFERENCES issue_trackers(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    issue_key TEXT NOT NULL,
    action TEXT NOT NULL CHECK (action IN ('link', 'comment', 'transition', 'unlink')),
    payload TEXT NOT NULL DEFAULT '{}',
    dedupe_key TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'delivered', 'failed')),
    attempt INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    next_retry_at DATETIME,
    delivered_at DATETIME,
    created_at DATETIME NOT NULL
);

INSERT INTO issue_tracker_jobs_new (id, tracker_id, project_id, issue_key, action, payload, dedupe_key, state, attempt, last_error, next_retry_at, delivered_at, created_at)
SELECT id, tracker_id, project_id, issue_key, action, payload, dedupe_key, state, attempt, last_error, next_retry_at, delivered_at, created_at
FROM issue_tracker_jobs;

DROP TABLE issue_tracker_jobs;

ALTER TABLE issue_tracker_jobs_new RENAME TO issue_tracker_jobs;

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
