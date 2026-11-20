-- name: IssueTrackerCreate :one
INSERT INTO issue_trackers (id, org_id, kind, name, base_url, auth_kind, account, secret_sealed, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(org_id), sqlc.arg(kind), sqlc.arg(name), sqlc.arg(base_url), sqlc.arg(auth_kind), sqlc.arg(account), sqlc.arg(secret_sealed), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(updated_at))
RETURNING *;

-- name: IssueTrackerUpdate :one
UPDATE issue_trackers
SET kind = sqlc.arg(kind), name = sqlc.arg(name), base_url = sqlc.arg(base_url), auth_kind = sqlc.arg(auth_kind),
    account = sqlc.arg(account), secret_sealed = sqlc.arg(secret_sealed), updated_at = sqlc.arg(updated_at)
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: IssueTrackerGetByOrg :one
SELECT *
FROM issue_trackers
WHERE org_id = sqlc.arg(org_id)
ORDER BY id
LIMIT 1;

-- name: IssueTrackerDelete :execrows
DELETE FROM issue_trackers
WHERE org_id = sqlc.arg(org_id) AND id = sqlc.arg(id);

-- name: IssueTrackerCountProjects :one
SELECT COUNT(*)
FROM project_issue_trackers
WHERE tracker_id = sqlc.arg(tracker_id);

-- name: ProjectIssueTrackerGet :one
SELECT *
FROM project_issue_trackers
WHERE project_id = sqlc.arg(project_id);

-- name: ProjectIssueTrackerUpsert :one
INSERT INTO project_issue_trackers (
    project_id, tracker_id, project_keys, link_merge_requests, link_commits, comment_on_merge,
    merged_status, opened_status, is_active, set_by, updated_at
)
VALUES (
    sqlc.arg(project_id), sqlc.arg(tracker_id), sqlc.arg(project_keys), sqlc.arg(link_merge_requests), sqlc.arg(link_commits),
    sqlc.arg(comment_on_merge), sqlc.arg(merged_status), sqlc.arg(opened_status), sqlc.arg(is_active), sqlc.arg(set_by), sqlc.arg(updated_at)
)
ON CONFLICT (project_id) DO UPDATE
SET tracker_id = excluded.tracker_id, project_keys = excluded.project_keys,
    link_merge_requests = excluded.link_merge_requests, link_commits = excluded.link_commits,
    comment_on_merge = excluded.comment_on_merge, merged_status = excluded.merged_status,
    opened_status = excluded.opened_status, is_active = excluded.is_active,
    set_by = excluded.set_by, updated_at = excluded.updated_at
RETURNING *;

-- name: ProjectIssueTrackerDelete :execrows
DELETE FROM project_issue_trackers
WHERE project_id = sqlc.arg(project_id);
