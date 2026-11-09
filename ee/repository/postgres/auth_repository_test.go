package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
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

func (s *AuthRepositorySuite) TestSaveRefreshTokenPrunesExpired() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	alice := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	bob := seedPBACUser(s.T(), s.db, 43)
	past := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	for _, row := range []sqlcPostgres.RefreshTokenCreateParams{
		{UserID: alice.Int64(), Token: "alice-old", ExpiresAt: past},
		{UserID: bob.Int64(), Token: "bob-old", ExpiresAt: past},
		{UserID: alice.Int64(), Token: "alice-recent", ExpiresAt: recent},
	} {
		_, err := s.q.RefreshTokenCreate(ctx, row)
		s.Require().NoError(err)
	}
	s.Require().NoError(repo.SaveRefreshToken(ctx, alice, "alice-live", time.Now().Add(time.Hour)))
	s.Require().NoError(repo.SaveRefreshToken(ctx, alice, "alice-new", time.Now().Add(2*time.Hour)))

	rows, err := s.db.QueryContext(ctx, "SELECT token FROM refresh_tokens ORDER BY token")
	s.Require().NoError(err)
	defer func() { _ = rows.Close() }()
	var tokens []string
	for rows.Next() {
		var token string
		s.Require().NoError(rows.Scan(&token))
		tokens = append(tokens, token)
	}
	s.Require().NoError(rows.Err())
	s.Equal([]string{"alice-live", "alice-new", "alice-recent", "bob-old"}, tokens)

	s.Error(repo.SaveRefreshToken(ctx, alice, "alice-live", time.Now().Add(time.Hour)))
	s.Require().NoError(s.db.Close())
	s.Error(repo.SaveRefreshToken(ctx, alice, "after-close", time.Now().Add(time.Hour)))
}
