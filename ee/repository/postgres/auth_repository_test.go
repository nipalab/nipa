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

func (s *AuthRepositorySuite) TestSSHLoginChallengeLifecycle() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	expiresAt := time.Now().Add(time.Minute)
	s.Require().NoError(repo.CreateSSHLoginChallenge(ctx, "alice@example.com", "nonce-1", expiresAt))

	got, err := repo.ConsumeSSHLoginChallenge(ctx, "nonce-1")
	s.Require().NoError(err)
	s.Equal("alice@example.com", got.Email)
	s.Equal("nonce-1", got.Nonce)
	s.WithinDuration(expiresAt, got.ExpiresAt, time.Second)
	s.False(got.CreatedAt.IsZero())

	_, err = repo.ConsumeSSHLoginChallenge(ctx, "nonce-1")
	requireRecordNotFound(s.T(), err)
}

func (s *AuthRepositorySuite) TestSSHLoginChallengeUnknown() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	_, err := repo.ConsumeSSHLoginChallenge(ctx, "missing")
	requireRecordNotFound(s.T(), err)
}

func (s *AuthRepositorySuite) TestSSHLoginChallengeDeleteExpired() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	s.Require().NoError(repo.CreateSSHLoginChallenge(ctx, "alice@example.com", "expired", time.Now().Add(-time.Minute)))
	s.Require().NoError(repo.CreateSSHLoginChallenge(ctx, "alice@example.com", "live", time.Now().Add(time.Minute)))

	s.Require().NoError(repo.DeleteExpiredSSHLoginChallenges(ctx, time.Now()))

	_, err := repo.ConsumeSSHLoginChallenge(ctx, "expired")
	requireRecordNotFound(s.T(), err)

	got, err := repo.ConsumeSSHLoginChallenge(ctx, "live")
	s.Require().NoError(err)
	s.Equal("live", got.Nonce)
}
