DROP TRIGGER issue_tracker_org_cleanup ON organizations;
DROP FUNCTION issue_tracker_org_cleanup();
DROP TRIGGER issue_tracker_project_cleanup ON projects;
DROP FUNCTION issue_tracker_project_cleanup();
DROP TABLE issue_tracker_jobs;
DROP TABLE issue_links;
DROP TABLE project_issue_trackers;
DROP TABLE issue_trackers;
