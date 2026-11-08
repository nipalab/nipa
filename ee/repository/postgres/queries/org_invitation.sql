-- name: OrgInvitationInsert :exec
INSERT INTO org_invitations (id, org_id, invited_by, email, role, token_hash, status, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(org_id), sqlc.arg(invited_by), sqlc.arg(email), sqlc.arg(role), sqlc.arg(token_hash), 'pending', sqlc.arg(expires_at));

-- name: OrgInvitationGet :one
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.id = sqlc.arg(id);

-- name: OrgInvitationGetByTokenHash :one
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.token_hash = sqlc.arg(token_hash);

-- name: OrgInvitationListByOrg :many
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.org_id = sqlc.arg(org_id) AND (
    sqlc.arg(status)::text = ''
    OR (sqlc.arg(status)::text = 'pending' AND i.status = 'pending' AND i.expires_at > sqlc.arg(now)::timestamptz)
    OR (sqlc.arg(status)::text = 'expired' AND (i.status = 'expired' OR (i.status = 'pending' AND i.expires_at <= sqlc.arg(now)::timestamptz)))
    OR (i.status = sqlc.arg(status)::text AND sqlc.arg(status)::text <> 'pending' AND sqlc.arg(status)::text <> 'expired')
)
ORDER BY i.id DESC
LIMIT 200;

-- name: OrgInvitationListPendingByEmail :many
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.email = sqlc.arg(email) AND i.status = 'pending' AND i.expires_at > sqlc.arg(now)::timestamptz
ORDER BY i.id DESC
LIMIT 200;

-- name: OrgInvitationFindPending :one
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.org_id = sqlc.arg(org_id) AND i.email = sqlc.arg(email) AND i.status = 'pending';

-- name: OrgInvitationExpireStale :exec
UPDATE org_invitations SET status = 'expired', updated_at = now()
WHERE org_id = sqlc.arg(org_id) AND email = sqlc.arg(email) AND status = 'pending' AND expires_at <= sqlc.arg(now)::timestamptz;

-- name: OrgInvitationRotate :execrows
UPDATE org_invitations SET token_hash = sqlc.arg(token_hash), expires_at = sqlc.arg(expires_at), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'pending';

-- name: OrgInvitationResolve :execrows
UPDATE org_invitations SET status = sqlc.arg(status)::text, updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'pending';

-- name: OrgInvitationAccept :execrows
UPDATE org_invitations SET status = 'accepted', accepted_by = sqlc.arg(accepted_by), accepted_at = sqlc.arg(accepted_at), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'pending';
