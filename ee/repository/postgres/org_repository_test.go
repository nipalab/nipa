package postgres

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type OrgRepositorySuite struct {
	baseSuite
}

func TestOrgRepositorySuite(t *testing.T) {
	suite.Run(t, new(OrgRepositorySuite))
}

func seedOrg(t *testing.T, q *sqlcPostgres.Queries, name, slug string) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id := node.Generate()

	_, err := q.CreateOrganization(context.Background(), sqlcPostgres.CreateOrganizationParams{
		ID:              id.Int64(),
		Name:            name,
		Slug:            slug,
		CreatedByUserID: 1,
	})
	require.NoError(t, err)
	return id
}

func (s *OrgRepositorySuite) TestGetBySlug_Success() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	orgID := seedOrg(s.T(), s.q, "Acme Corp", "acme")

	got, err := repo.GetBySlug(ctx, "acme")
	s.Require().NoError(err)
	s.Equal(orgID, got.ID)
	s.Equal("acme", got.Slug)
	s.Equal("Acme Corp", got.Name)
	s.False(got.CreatedAt.IsZero())
	s.False(got.UpdatedAt.IsZero())
}

func (s *OrgRepositorySuite) TestGetByID() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	orgID := seedOrg(s.T(), s.q, "Acme Corp", "acme")

	got, err := repo.GetByID(ctx, orgID)
	s.Require().NoError(err)
	s.Equal(orgID, got.ID)
	s.Equal("acme", got.Slug)

	_, err = repo.GetByID(ctx, snow.ID(9999))
	requireRecordNotFound(s.T(), err)
}

func (s *OrgRepositorySuite) TestGetBySlug_SeededDefault() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	got, err := repo.GetBySlug(ctx, "default")
	s.Require().NoError(err)
	s.Equal(snow.ID(1), got.ID)
	s.Equal("default", got.Slug)
	s.Equal("Default Organization", got.Name)
	s.Equal(snow.ID(1), got.CreatedByUserID, "the seeded default org is owned by the seeded super admin")
}

func (s *OrgRepositorySuite) TestGetBySlug_NotFound() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	_, err := repo.GetBySlug(ctx, "does-not-exist")
	requireRecordNotFound(s.T(), err)
}

func (s *OrgRepositorySuite) TestGetBySlug_DeletedOrg() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	orgID := seedOrg(s.T(), s.q, "Acme Corp", "acme")
	s.Require().NoError(s.q.DeleteOrganization(ctx, orgID.Int64()))

	_, err := repo.GetBySlug(ctx, "acme")
	requireRecordNotFound(s.T(), err)
}

func (s *OrgRepositorySuite) TestMembership() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	role, err := repo.MemberRole(ctx, 1, 1)
	s.Require().NoError(err)
	s.Equal(domain.OrgRoleOwner, role, "migration seeds the super admin as the default org owner")

	userID := seedPBACUser(s.T(), s.db, 2)
	s.Require().NoError(repo.UpsertMember(ctx, 1, userID, domain.OrgRoleMember))

	members, err := repo.ListMembers(ctx, 1)
	s.Require().NoError(err)
	s.Len(members, 2)
	found := false
	for _, member := range members {
		if member.User.ID == userID {
			found = true
			s.Equal("user2", member.User.Name)
			s.Equal("user2@example.com", member.User.Email)
			s.Equal(domain.OrgRoleMember, member.Role)
			s.NotZero(member.JoinedAt)
		}
	}
	s.True(found)

	memberships, err := repo.ListForUser(ctx, userID)
	s.Require().NoError(err)
	s.Len(memberships, 1)
	s.Equal(snow.ID(1), memberships[0].Org.ID)
	s.Equal("default", memberships[0].Org.Slug)
	s.Equal(domain.OrgRoleMember, memberships[0].Role)

	s.Require().NoError(repo.UpsertMember(ctx, 1, userID, domain.OrgRoleOwner))
	role, err = repo.MemberRole(ctx, 1, userID)
	s.Require().NoError(err)
	s.Equal(domain.OrgRoleOwner, role)

	count, err := repo.CountMembersByRole(ctx, 1, domain.OrgRoleOwner)
	s.Require().NoError(err)
	s.Equal(2, count)

	s.Require().NoError(repo.RemoveMember(ctx, 1, userID))
	_, err = repo.MemberRole(ctx, 1, userID)
	requireRecordNotFound(s.T(), err)
}

func (s *OrgRepositorySuite) TestMemberRole_NotAMember() {
	ctx := context.Background()

	_, err := NewOrgRepository(s.db).MemberRole(ctx, 1, 999)
	requireRecordNotFound(s.T(), err)
}

func (s *OrgRepositorySuite) TestCreateWithOwner() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	orgID := newTestNode(s.T()).Generate()
	ownerID := snow.ID(1)

	created, err := repo.CreateWithOwner(ctx, domain.Organization{ID: orgID, Name: "Acme Corp", Slug: "acme"}, ownerID)
	s.Require().NoError(err)
	s.Equal(orgID, created.ID)
	s.Equal("acme", created.Slug)
	s.Equal(ownerID, created.CreatedByUserID)

	role, err := repo.MemberRole(ctx, orgID, ownerID)
	s.Require().NoError(err)
	s.Equal(domain.OrgRoleOwner, role)

	stored, err := repo.GetBySlug(ctx, "acme")
	s.Require().NoError(err)
	s.Equal(orgID, stored.ID)
	s.Equal(ownerID, stored.CreatedByUserID)
}

func (s *OrgRepositorySuite) TestCreateWithOwner_DuplicateSlug() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)
	node := newTestNode(s.T())

	_, err := repo.CreateWithOwner(ctx, domain.Organization{ID: node.Generate(), Name: "Acme", Slug: "acme"}, 1)
	s.Require().NoError(err)

	secondID := node.Generate()
	_, err = repo.CreateWithOwner(ctx, domain.Organization{ID: secondID, Name: "Other", Slug: "acme"}, 1)
	s.True(domain.IsErrorConflict(err))

	_, err = repo.MemberRole(ctx, secondID, 1)
	requireRecordNotFound(s.T(), err)
}

func (s *OrgRepositorySuite) TestListMembers_ExcludesDeletedUsers() {
	ctx := context.Background()
	repo := NewOrgRepository(s.db)

	userID := seedPBACUser(s.T(), s.db, 3)
	s.Require().NoError(repo.UpsertMember(ctx, 1, userID, domain.OrgRoleMember))
	_, err := s.db.ExecContext(ctx, `UPDATE users SET deleted = true WHERE id = $1`, userID.Int64())
	s.Require().NoError(err)

	members, err := repo.ListMembers(ctx, 1)
	s.Require().NoError(err)
	for _, member := range members {
		s.NotEqual(userID, member.User.ID)
	}
}
