CREATE TABLE org_invitations (
    id          INTEGER PRIMARY KEY,
    org_id      INTEGER NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    invited_by  INTEGER NOT NULL,
    email       TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT 'member',
    token_hash  BLOB NOT NULL UNIQUE,
    status      TEXT NOT NULL DEFAULT 'pending',
    expires_at  DATETIME NOT NULL,
    accepted_by INTEGER,
    accepted_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (role IN ('owner', 'member')),
    CHECK (status IN ('pending', 'accepted', 'declined', 'revoked', 'expired'))
);

CREATE UNIQUE INDEX idx_org_invitations_pending ON org_invitations (org_id, email) WHERE status = 'pending';

CREATE INDEX idx_org_invitations_email ON org_invitations (email, status);

CREATE INDEX idx_org_invitations_org ON org_invitations (org_id, id DESC);
