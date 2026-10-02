package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type AuthRepositorySuite struct {
	baseSuite
}

func TestAuthRepositorySuite(t *testing.T) {
	suite.Run(t, new(AuthRepositorySuite))
}

func (s *AuthRepositorySuite) TestSaveAndGetAndDeleteRefreshToken() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	expiresAt := time.Now().Add(60 * 24 * time.Hour)

	s.Require().NoError(repo.SaveRefreshToken(ctx, userID, "token-1", expiresAt))

	got, err := repo.GetAndDeleteRefreshToken(ctx, "token-1")
	s.Require().NoError(err)
	s.NotZero(got.ID)
	s.Equal(userID, got.UserID)
	s.Equal("token-1", got.Token)
	s.WithinDuration(expiresAt, got.ExpiresAt, time.Second)
	s.False(got.CreatedAt.IsZero())
}

func (s *AuthRepositorySuite) TestRefreshTokenIsSingleUse() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "bob", "bob@example.com", sql.NullString{})
	s.Require().NoError(repo.SaveRefreshToken(ctx, userID, "token-2", time.Now().Add(time.Hour)))

	_, err := repo.GetAndDeleteRefreshToken(ctx, "token-2")
	s.Require().NoError(err)

	_, err = repo.GetAndDeleteRefreshToken(ctx, "token-2")
	requireRecordNotFound(s.T(), err)
}

func (s *AuthRepositorySuite) TestGetAndDeleteUnknownToken() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	_, err := repo.GetAndDeleteRefreshToken(ctx, "unknown-token")
	requireRecordNotFound(s.T(), err)
}
