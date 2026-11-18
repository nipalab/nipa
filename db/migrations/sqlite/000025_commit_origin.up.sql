ALTER TABLE commits ADD COLUMN author_name TEXT;
ALTER TABLE commits ADD COLUMN author_email TEXT;
ALTER TABLE commits ADD COLUMN authored_at DATETIME;
ALTER TABLE commits ADD COLUMN origin TEXT;

CREATE UNIQUE INDEX commits_project_origin ON commits (project_id, origin) WHERE origin IS NOT NULL;
