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

-- name: IssueTrackerLockOrg :exec
SELECT pg_advisory_xact_lock(sqlc.arg(lock_key)::bigint);

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

-- name: IssueTrackerGetByID :one
SELECT *
FROM issue_trackers
WHERE id = sqlc.arg(id);

-- name: IssueLinkUpsert :exec
INSERT INTO issue_links (id, project_id, tracker_id, issue_key, subject_kind, subject_id, relation, created_at)
VALUES (sqlc.arg(id), sqlc.arg(project_id), sqlc.arg(tracker_id), sqlc.arg(issue_key), sqlc.arg(subject_kind), sqlc.arg(subject_id), sqlc.arg(relation), sqlc.arg(created_at))
ON CONFLICT (project_id, issue_key, subject_kind, subject_id) DO UPDATE
SET relation = excluded.relation, tracker_id = excluded.tracker_id;

-- name: IssueLinkListBySubject :many
SELECT *
FROM issue_links
WHERE project_id = sqlc.arg(project_id) AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
ORDER BY issue_key;

-- name: IssueLinkListBySubjects :many
SELECT *
FROM issue_links
WHERE project_id = sqlc.arg(project_id) AND subject_kind = sqlc.arg(subject_kind) AND subject_id = ANY(sqlc.arg(subject_ids)::bigint[])
ORDER BY subject_id, issue_key;

-- name: IssueLinkDeleteKeys :execrows
DELETE FROM issue_links
WHERE project_id = sqlc.arg(project_id) AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
  AND issue_key = ANY(sqlc.arg(issue_keys)::text[]);

-- name: IssueLinkListSubjects :many
SELECT l.id, l.subject_kind, l.subject_id, l.relation, l.created_at,
    c.message AS commit_message, c.created_at AS commit_created_at,
    mr.number AS mr_number, mr.title AS mr_title, mr.status AS mr_status,
    b.name AS branch_name
FROM issue_links l
LEFT JOIN commits c ON l.subject_kind = 'commit' AND c.id = l.subject_id AND c.project_id = l.project_id
LEFT JOIN merge_requests mr ON l.subject_kind = 'merge_request' AND mr.id = l.subject_id AND mr.project_id = l.project_id
LEFT JOIN branches b ON l.subject_kind = 'branch' AND b.id = l.subject_id AND b.project_id = l.project_id AND b.deleted = FALSE
WHERE l.project_id = sqlc.arg(project_id) AND l.issue_key = sqlc.arg(issue_key) AND l.id < sqlc.arg(before_id)
ORDER BY l.id DESC
LIMIT sqlc.arg(row_limit)::bigint;
