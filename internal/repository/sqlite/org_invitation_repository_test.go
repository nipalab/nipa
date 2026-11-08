package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func newInvitation(t *testing.T, orgID, inviter snow.ID, email string, hash byte, expires time.Time) *domain.OrgInvitation {
	t.Helper()
	node := newTestNode(t)
	return &domain.OrgInvitation{
		ID:        node.Generate(),
		OrgID:     orgID,
		InvitedBy: inviter,
		Email:     email,
		Role:      domain.OrgRoleMember,
		TokenHash: []byte{hash, 1, 2, 3},
		ExpiresAt: expires,
	}
}

func TestOrgInvitationRepositorySQLite(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewOrgInvitationRepository(db)
	orgID := seedOrg(t, q, "Acme Corp", "acme")
	otherOrg := seedOrg(t, q, "Other", "other")
	inviter := seedUser(t, q, "Owner", "owner@example.com", sql.NullString{})
	now := time.Now().UTC().Truncate(time.Second)

	inv := newInvitation(t, orgID, inviter, "bob@example.com", 1, now.Add(time.Hour))
	require.NoError(t, repo.Create(ctx, inv))

	t.Run("get and joined fields", func(t *testing.T) {
		got, err := repo.Get(ctx, inv.ID)
		require.NoError(t, err)
		require.Equal(t, inv.ID, got.ID)
		require.Equal(t, "Acme Corp", got.OrgName)
		require.Equal(t, "acme", got.OrgSlug)
		require.Equal(t, "Owner", got.InviterName)
		require.Equal(t, domain.OrgInvitationPending, got.Status)
		require.Equal(t, []byte{1, 1, 2, 3}, got.TokenHash)
		require.Nil(t, got.AcceptedBy)
		require.Nil(t, got.AcceptedAt)
		require.WithinDuration(t, inv.ExpiresAt, got.ExpiresAt, time.Second)

		byHash, err := repo.GetByTokenHash(ctx, []byte{1, 1, 2, 3})
		require.NoError(t, err)
		require.Equal(t, inv.ID, byHash.ID)

		_, err = repo.GetByTokenHash(ctx, []byte{9})
		requireRecordNotFound(t, err)
		_, err = repo.Get(ctx, 12345)
		requireRecordNotFound(t, err)
	})

	t.Run("one pending invitation per org and email", func(t *testing.T) {
		dup := newInvitation(t, orgID, inviter, "bob@example.com", 2, now.Add(time.Hour))
		err := repo.Create(ctx, dup)
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		require.Equal(t, 409, derr.Code)

		require.NoError(t, repo.Create(ctx, newInvitation(t, otherOrg, inviter, "bob@example.com", 3, now.Add(time.Hour))))
	})

	t.Run("find pending and listings", func(t *testing.T) {
		found, err := repo.FindPending(ctx, orgID, "bob@example.com")
		require.NoError(t, err)
		require.Equal(t, inv.ID, found.ID)
		_, err = repo.FindPending(ctx, orgID, "nobody@example.com")
		requireRecordNotFound(t, err)

		all, err := repo.ListByOrg(ctx, orgID, "")
		require.NoError(t, err)
		require.Len(t, all, 1)
		none, err := repo.ListByOrg(ctx, orgID, domain.OrgInvitationRevoked)
		require.NoError(t, err)
		require.Empty(t, none)

		mine, err := repo.ListPendingByEmail(ctx, "bob@example.com", now)
		require.NoError(t, err)
		require.Len(t, mine, 2)
		late, err := repo.ListPendingByEmail(ctx, "bob@example.com", now.Add(2*time.Hour))
		require.NoError(t, err)
		require.Empty(t, late, "expired invitations are not listed")
	})

	t.Run("rotate resolve accept", func(t *testing.T) {
		ok, err := repo.Rotate(ctx, inv.ID, []byte{7, 7}, now.Add(48*time.Hour))
		require.NoError(t, err)
		require.True(t, ok)
		_, err = repo.GetByTokenHash(ctx, []byte{1, 1, 2, 3})
		requireRecordNotFound(t, err)
		got, err := repo.GetByTokenHash(ctx, []byte{7, 7})
		require.NoError(t, err)
		require.WithinDuration(t, now.Add(48*time.Hour), got.ExpiresAt, time.Second)

		ok, err = repo.Accept(ctx, inv.ID, inviter, now)
		require.NoError(t, err)
		require.True(t, ok)
		got, err = repo.Get(ctx, inv.ID)
		require.NoError(t, err)
		require.Equal(t, domain.OrgInvitationAccepted, got.Status)
		require.NotNil(t, got.AcceptedBy)
		require.Equal(t, inviter, *got.AcceptedBy)
		require.NotNil(t, got.AcceptedAt)

		ok, err = repo.Accept(ctx, inv.ID, inviter, now)
		require.NoError(t, err)
		require.False(t, ok, "only pending invitations can be accepted")
		ok, err = repo.Resolve(ctx, inv.ID, domain.OrgInvitationRevoked)
		require.NoError(t, err)
		require.False(t, ok)
		ok, err = repo.Rotate(ctx, inv.ID, []byte{8}, now)
		require.NoError(t, err)
		require.False(t, ok)

		again := newInvitation(t, orgID, inviter, "bob@example.com", 4, now.Add(time.Hour))
		require.NoError(t, repo.Create(ctx, again), "an accepted invitation frees the pending slot")
		ok, err = repo.Resolve(ctx, again.ID, domain.OrgInvitationDeclined)
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("expire stale frees the slot", func(t *testing.T) {
		stale := newInvitation(t, orgID, inviter, "old@example.com", 5, now.Add(-time.Hour))
		require.NoError(t, repo.Create(ctx, stale))
		fresh := newInvitation(t, orgID, inviter, "new@example.com", 6, now.Add(time.Hour))
		require.NoError(t, repo.Create(ctx, fresh))

		require.NoError(t, repo.ExpireStale(ctx, orgID, "old@example.com", now))
		require.NoError(t, repo.ExpireStale(ctx, orgID, "new@example.com", now))

		got, err := repo.Get(ctx, stale.ID)
		require.NoError(t, err)
		require.Equal(t, domain.OrgInvitationExpired, got.Status)
		got, err = repo.Get(ctx, fresh.ID)
		require.NoError(t, err)
		require.Equal(t, domain.OrgInvitationPending, got.Status)
		require.NoError(t, repo.Create(ctx, newInvitation(t, orgID, inviter, "old@example.com", 7, now.Add(time.Hour))))
	})

	t.Run("deleted organizations hide their invitations", func(t *testing.T) {
		gone := seedOrg(t, q, "Gone", "gone")
		hidden := newInvitation(t, gone, inviter, "ghost@example.com", 8, now.Add(time.Hour))
		require.NoError(t, repo.Create(ctx, hidden))
		_, err := db.ExecContext(ctx, "UPDATE organizations SET deleted = true WHERE id = ?", gone.Int64())
		require.NoError(t, err)
		_, err = repo.Get(ctx, hidden.ID)
		requireRecordNotFound(t, err)
	})
}
