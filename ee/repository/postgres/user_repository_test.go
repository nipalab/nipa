package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

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

func (s *UserRepositorySuite) TestDeleteRefreshTokens() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)
	auth := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	otherID := seedUser(s.T(), s.q, "bob", "bob@example.com", sql.NullString{})
	expiresAt := time.Now().Add(time.Hour)
	s.Require().NoError(auth.SaveRefreshToken(ctx, userID, "token-1", expiresAt))
	s.Require().NoError(auth.SaveRefreshToken(ctx, userID, "token-2", expiresAt))
	s.Require().NoError(auth.SaveRefreshToken(ctx, otherID, "other-token", expiresAt))

	s.Require().NoError(repo.DeleteRefreshTokens(ctx, userID))

	_, err := auth.GetAndDeleteRefreshToken(ctx, "token-1")
	requireRecordNotFound(s.T(), err)
	_, err = auth.GetAndDeleteRefreshToken(ctx, "token-2")
	requireRecordNotFound(s.T(), err)
	got, err := auth.GetAndDeleteRefreshToken(ctx, "other-token")
	s.Require().NoError(err)
	s.Equal(otherID, got.UserID)
}

func (s *UserRepositorySuite) TestUpdateEmailAndRevokeTokens() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)
	auth := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	expiresAt := time.Now().Add(time.Hour)
	s.Require().NoError(auth.SaveRefreshToken(ctx, userID, "token-1", expiresAt))

	s.Require().NoError(repo.UpdateEmailAndRevokeTokens(ctx, userID, "Alice2@Example.com"))

	got, err := repo.GetByID(ctx, userID)
	s.Require().NoError(err)
	s.Equal("alice2@example.com", got.Email)

	_, err = auth.GetAndDeleteRefreshToken(ctx, "token-1")
	requireRecordNotFound(s.T(), err)
}

func (s *UserRepositorySuite) TestUpdateEmailAndRevokeTokens_RollsBackOnConflict() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)
	auth := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	seedUser(s.T(), s.q, "bob", "bob@example.com", sql.NullString{})
	s.Require().NoError(auth.SaveRefreshToken(ctx, userID, "token-1", time.Now().Add(time.Hour)))

	s.Require().Error(repo.UpdateEmailAndRevokeTokens(ctx, userID, "bob@example.com"))

	got, err := repo.GetByID(ctx, userID)
	s.Require().NoError(err)
	s.Equal("alice@example.com", got.Email)
	token, err := auth.GetAndDeleteRefreshToken(ctx, "token-1")
	s.Require().NoError(err)
	s.Equal(userID, token.UserID)
}

func (s *UserRepositorySuite) TestUpdatePasswordAndRevokeTokens() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)
	auth := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	expiresAt := time.Now().Add(time.Hour)
	s.Require().NoError(auth.SaveRefreshToken(ctx, userID, "token-1", expiresAt))

	s.Require().NoError(repo.UpdatePasswordAndRevokeTokens(ctx, userID, "new-hash"))

	got, err := repo.GetByID(ctx, userID)
	s.Require().NoError(err)
	s.Equal("new-hash", got.Password)

	_, err = auth.GetAndDeleteRefreshToken(ctx, "token-1")
	requireRecordNotFound(s.T(), err)
}

func (s *UserRepositorySuite) TestDeactivateAndRevokeTokens() {
	ctx := context.Background()
	repo := NewUserRepository(s.db)
	auth := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	expiresAt := time.Now().Add(time.Hour)
	s.Require().NoError(auth.SaveRefreshToken(ctx, userID, "token-1", expiresAt))

	s.Require().NoError(repo.DeactivateAndRevokeTokens(ctx, userID))

	_, err := repo.GetByID(ctx, userID)
	requireRecordNotFound(s.T(), err)

	_, err = auth.GetAndDeleteRefreshToken(ctx, "token-1")
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

func (s *UserRepositorySuite) TestRevocationsBumpTokenVersion() {
	ctx := context.Background()
	users := NewUserRepository(s.db)
	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})

	version := func() int64 {
		var v int64
		s.Require().NoError(s.db.QueryRowContext(ctx, "SELECT token_version FROM users WHERE id = $1", userID.Int64()).Scan(&v))
		return v
	}

	got, err := users.GetByID(ctx, userID)
	s.Require().NoError(err)
	s.Equal(int64(0), got.TokenVersion)

	s.Require().NoError(users.UpdatePasswordAndRevokeTokens(ctx, userID, "new-hash"))
	s.Equal(int64(1), version())
	s.Require().NoError(users.UpdateEmailAndRevokeTokens(ctx, userID, "alice2@example.com"))
	s.Equal(int64(2), version())
	s.Require().NoError(users.UpdateAdminFlags(ctx, userID, got.IsAdmin, got.IsSuperAdmin))
	s.Equal(int64(2), version(), "unchanged flags keep sessions")
	s.Require().NoError(users.UpdateAdminFlags(ctx, userID, !got.IsAdmin, got.IsSuperAdmin))
	s.Equal(int64(3), version())
	s.Require().NoError(users.UpdateAdminFlags(ctx, userID, !got.IsAdmin, !got.IsSuperAdmin))
	s.Equal(int64(4), version())

	got, err = users.GetByID(ctx, userID)
	s.Require().NoError(err)
	s.Equal(int64(4), got.TokenVersion)

	s.Require().NoError(users.DeactivateAndRevokeTokens(ctx, userID))
	s.Equal(int64(5), version())
}
