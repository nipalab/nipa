DROP INDEX IF EXISTS commits_project_origin;

ALTER TABLE commits DROP COLUMN origin;
ALTER TABLE commits DROP COLUMN author_email;
ALTER TABLE commits DROP COLUMN author_name;
