-- name: OrgInvitationInsert :exec
INSERT INTO org_invitations (id, org_id, invited_by, email, role, token_hash, status, expires_at)
VALUES (:id, :org_id, :invited_by, :email, :role, :token_hash, 'pending', :expires_at);

-- name: OrgInvitationGet :one
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.id = :id;

-- name: OrgInvitationGetByTokenHash :one
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.token_hash = :token_hash;

-- name: OrgInvitationListByOrg :many
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.org_id = sqlc.arg(org_id) AND (sqlc.arg(status) = '' OR i.status = sqlc.arg(status))
ORDER BY i.id DESC
LIMIT 200;

-- name: OrgInvitationListPendingByEmail :many
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.email = sqlc.arg(email) AND i.status = 'pending' AND i.expires_at > sqlc.arg(now)
ORDER BY i.id DESC
LIMIT 200;

-- name: OrgInvitationFindPending :one
SELECT i.id, i.org_id, i.invited_by, i.email, i.role, i.token_hash, i.status, i.expires_at, i.accepted_by, i.accepted_at, i.created_at, o.name AS org_name, o.slug AS org_slug, COALESCE(u.name, '') AS inviter_name
FROM org_invitations i
JOIN organizations o ON o.id = i.org_id AND o.deleted = false
LEFT JOIN users u ON u.id = i.invited_by
WHERE i.org_id = :org_id AND i.email = :email AND i.status = 'pending';

-- name: OrgInvitationExpireStale :exec
UPDATE org_invitations SET status = 'expired', updated_at = CURRENT_TIMESTAMP
WHERE org_id = :org_id AND email = :email AND status = 'pending' AND expires_at <= :now;

-- name: OrgInvitationRotate :execrows
UPDATE org_invitations SET token_hash = :token_hash, expires_at = :expires_at, updated_at = CURRENT_TIMESTAMP
WHERE id = :id AND status = 'pending';

-- name: OrgInvitationResolve :execrows
UPDATE org_invitations SET status = :status, updated_at = CURRENT_TIMESTAMP
WHERE id = :id AND status = 'pending';

-- name: OrgInvitationAccept :execrows
UPDATE org_invitations SET status = 'accepted', accepted_by = :accepted_by, accepted_at = :accepted_at, updated_at = CURRENT_TIMESTAMP
WHERE id = :id AND status = 'pending';
