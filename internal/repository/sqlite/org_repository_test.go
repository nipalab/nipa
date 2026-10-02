package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func seedOrg(t *testing.T, q *sqlite.Queries, name, slug string) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id := node.Generate()

	_, err := q.CreateOrganization(context.Background(), sqlite.CreateOrganizationParams{
		ID:              id.Int64(),
		Name:            name,
		Slug:            slug,
		CreatedByUserID: 1,
	})
	require.NoError(t, err)
	return id
}

func TestOrgRepositorySQLite_GetBySlug_Success(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	orgID := seedOrg(t, q, "Acme Corp", "acme")

	got, err := repo.GetBySlug(ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, orgID, got.ID)
	require.Equal(t, "acme", got.Slug)
	require.Equal(t, "Acme Corp", got.Name)
	require.False(t, got.CreatedAt.IsZero())
	require.False(t, got.UpdatedAt.IsZero())
}

func TestOrgRepositorySQLite_GetByID(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	orgID := seedOrg(t, q, "Acme Corp", "acme")

	got, err := repo.GetByID(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, orgID, got.ID)
	require.Equal(t, "acme", got.Slug)

	_, err = repo.GetByID(ctx, snow.ID(9999))
	requireRecordNotFound(t, err)
}

func TestOrgRepositorySQLite_GetBySlug_SeededDefault(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	got, err := repo.GetBySlug(ctx, "default")
	require.NoError(t, err)
	require.Equal(t, snow.ID(1), got.ID)
	require.Equal(t, "default", got.Slug)
	require.Equal(t, "Default Organization", got.Name)
	require.Equal(t, snow.ID(1), got.CreatedByUserID, "the seeded default org is owned by the seeded super admin")
}

func TestOrgRepositorySQLite_GetBySlug_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	_, err := repo.GetBySlug(ctx, "does-not-exist")
	requireRecordNotFound(t, err)
}

func TestOrgRepositorySQLite_GetBySlug_DeletedOrg(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	orgID := seedOrg(t, q, "Acme Corp", "acme")
	require.NoError(t, q.DeleteOrganization(ctx, orgID.Int64()))

	_, err := repo.GetBySlug(ctx, "acme")
	requireRecordNotFound(t, err)
}

func TestOrgRepositorySQLite_Membership(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	role, err := repo.MemberRole(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, domain.OrgRoleOwner, role, "migration seeds the super admin as the default org owner")

	userID := seedPBACUser(t, db, 2)
	require.NoError(t, repo.UpsertMember(ctx, 1, userID, domain.OrgRoleMember))

	members, err := repo.ListMembers(ctx, 1)
	require.NoError(t, err)
	require.Len(t, members, 2)
	found := false
	for _, member := range members {
		if member.User.ID == userID {
			found = true
			require.Equal(t, "user2", member.User.Name)
			require.Equal(t, "user2@example.com", member.User.Email)
			require.Equal(t, domain.OrgRoleMember, member.Role)
			require.NotZero(t, member.JoinedAt)
		}
	}
	require.True(t, found)

	memberships, err := repo.ListForUser(ctx, userID)
	require.NoError(t, err)
	require.Len(t, memberships, 1)
	require.Equal(t, snow.ID(1), memberships[0].Org.ID)
	require.Equal(t, "default", memberships[0].Org.Slug)
	require.Equal(t, domain.OrgRoleMember, memberships[0].Role)

	require.NoError(t, repo.UpsertMember(ctx, 1, userID, domain.OrgRoleOwner))
	role, err = repo.MemberRole(ctx, 1, userID)
	require.NoError(t, err)
	require.Equal(t, domain.OrgRoleOwner, role)

	count, err := repo.CountMembersByRole(ctx, 1, domain.OrgRoleOwner)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	require.NoError(t, repo.RemoveMember(ctx, 1, userID))
	_, err = repo.MemberRole(ctx, 1, userID)
	requireRecordNotFound(t, err)
}

func TestOrgRepositorySQLite_MemberRole_NotAMember(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)

	_, err := NewOrgRepository(db).MemberRole(ctx, 1, 999)
	requireRecordNotFound(t, err)
}

func TestOrgRepositorySQLite_CreateWithOwner(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	orgID := newTestNode(t).Generate()
	ownerID := snow.ID(1)

	created, err := repo.CreateWithOwner(ctx, domain.Organization{ID: orgID, Name: "Acme Corp", Slug: "acme"}, ownerID)
	require.NoError(t, err)
	require.Equal(t, orgID, created.ID)
	require.Equal(t, "acme", created.Slug)
	require.Equal(t, ownerID, created.CreatedByUserID)

	role, err := repo.MemberRole(ctx, orgID, ownerID)
	require.NoError(t, err)
	require.Equal(t, domain.OrgRoleOwner, role)

	stored, err := repo.GetBySlug(ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, orgID, stored.ID)
	require.Equal(t, ownerID, stored.CreatedByUserID)
}

func TestOrgRepositorySQLite_CreateWithOwner_DuplicateSlug(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)
	node := newTestNode(t)

	_, err := repo.CreateWithOwner(ctx, domain.Organization{ID: node.Generate(), Name: "Acme", Slug: "acme"}, 1)
	require.NoError(t, err)

	secondID := node.Generate()
	_, err = repo.CreateWithOwner(ctx, domain.Organization{ID: secondID, Name: "Other", Slug: "acme"}, 1)
	require.True(t, domain.IsErrorConflict(err))

	_, err = repo.MemberRole(ctx, secondID, 1)
	requireRecordNotFound(t, err)
}

func TestOrgRepositorySQLite_ListMembers_ExcludesDeletedUsers(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	userID := seedPBACUser(t, db, 3)
	require.NoError(t, repo.UpsertMember(ctx, 1, userID, domain.OrgRoleMember))
	_, err := db.ExecContext(ctx, `UPDATE users SET deleted = true WHERE id = ?`, userID.Int64())
	require.NoError(t, err)

	members, err := repo.ListMembers(ctx, 1)
	require.NoError(t, err)
	for _, member := range members {
		require.NotEqual(t, userID, member.User.ID)
	}
}
