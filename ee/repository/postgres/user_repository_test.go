package postgres

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
)

type UserRepositorySuite struct {
	baseSuite
}

func TestUserRepositorySuite(t *testing.T) {
	suite.Run(t, new(UserRepositorySuite))
}

func (s *UserRepositorySuite) TestGetByEmail() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)

	seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{String: "https://example.com/alice.png", Valid: true})

	got, err := repo.GetByEmail(ctx, "alice@example.com")
	s.Require().NoError(err)
	s.NotZero(got.ID)
	s.Equal("alice", got.Name)
	s.Equal("alice@example.com", got.Email)
	s.Equal("hashed-password", got.Password)
	s.Equal("https://example.com/alice.png", got.PhotoUrl)
	s.False(got.IsSuperAdmin)
	s.True(got.IsAdmin)
	s.False(got.Deleted)
	s.Nil(got.DeletedAt)
	s.False(got.CreatedAt.IsZero())
	s.False(got.UpdatedAt.IsZero())
}

func (s *UserRepositorySuite) TestGetByEmail_NullPhotoURL() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)

	seedUser(s.T(), s.q, "bob", "bob@example.com", sql.NullString{})

	got, err := repo.GetByEmail(ctx, "bob@example.com")
	s.Require().NoError(err)
	s.Empty(got.PhotoUrl)
}

func (s *UserRepositorySuite) TestGetByID() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)

	id := seedUser(s.T(), s.q, "carol", "carol@example.com", sql.NullString{})

	got, err := repo.GetByID(ctx, id)
	s.Require().NoError(err)
	s.Equal(id, got.ID)
	s.Equal("carol", got.Name)
	s.Equal("carol@example.com", got.Email)
}

func (s *UserRepositorySuite) TestNotFound() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)

	_, err := repo.GetByEmail(ctx, "missing@example.com")
	requireRecordNotFound(s.T(), err)

	_, err = repo.GetByID(ctx, 999999)
	requireRecordNotFound(s.T(), err)
}

func (s *UserRepositorySuite) TestDeletedUserIsNotReturned() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)

	id := seedUser(s.T(), s.q, "dave", "dave@example.com", sql.NullString{})
	s.Require().NoError(s.q.UserDeleteByID(ctx, id.Int64()))

	_, err := repo.GetByEmail(ctx, "dave@example.com")
	requireRecordNotFound(s.T(), err)

	_, err = repo.GetByID(ctx, id)
	requireRecordNotFound(s.T(), err)
}

func (s *UserRepositorySuite) TestCRUD() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)

	node := newTestNode(s.T())
	created, err := repo.Create(ctx, domain.User{
		ID: node.Generate(), Name: "bob", Email: "bob@example.com", Password: "hash",
	})
	s.Require().NoError(err)
	s.Equal("bob", created.Name)
	s.Equal("bob@example.com", created.Email)
	s.False(created.Deleted)

	users, err := repo.List(ctx)
	s.Require().NoError(err)
	s.Len(users, 2, "seeded super admin plus the created user")

	s.Require().NoError(repo.UpdateProfile(ctx, created.ID, "bobby", "https://example.com/b.png", nil))
	got, err := repo.GetByID(ctx, created.ID)
	s.Require().NoError(err)
	s.Equal("bobby", got.Name)
	s.Equal("https://example.com/b.png", got.PhotoUrl)
	s.True(got.NotifyEmail, "notifications default to enabled")

	disabled := false
	s.Require().NoError(repo.UpdateProfile(ctx, created.ID, "bobby", "https://example.com/b.png", &disabled))
	got, err = repo.GetByID(ctx, created.ID)
	s.Require().NoError(err)
	s.False(got.NotifyEmail)

	s.Require().NoError(repo.UpdateProfile(ctx, created.ID, "bobby", "https://example.com/b.png", nil))
	got, err = repo.GetByID(ctx, created.ID)
	s.Require().NoError(err)
	s.False(got.NotifyEmail, "a nil notify_email keeps the stored value")

	s.Require().NoError(repo.UpdateEmail(ctx, created.ID, "robert@example.com"))
	s.Require().NoError(repo.UpdatePassword(ctx, created.ID, "new-hash"))
	s.Require().NoError(repo.UpdateAdminFlags(ctx, created.ID, true, false))

	got, err = repo.GetByID(ctx, created.ID)
	s.Require().NoError(err)
	s.Equal("robert@example.com", got.Email)
	s.Equal("new-hash", got.Password)
	s.True(got.IsAdmin)

	s.Require().NoError(repo.Deactivate(ctx, created.ID))
	_, err = repo.GetByID(ctx, created.ID)
	requireRecordNotFound(s.T(), err)
}

func (s *UserRepositorySuite) TestCreate_DuplicateEmail() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)

	seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	node := newTestNode(s.T())
	_, err := repo.Create(ctx, domain.User{
		ID: node.Generate(), Name: "alice2", Email: "alice@example.com", Password: "hash",
	})
	s.Require().Error(err)
}
