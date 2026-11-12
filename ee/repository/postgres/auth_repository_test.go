package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type AuthRepositorySuite struct {
	baseSuite
}

func TestAuthRepositorySuite(t *testing.T) {
	suite.Run(t, new(AuthRepositorySuite))
}

func (s *baseSuite) createSession(auth *Auth, userID snow.ID, hash string, expiresAt time.Time) int64 {
	id, err := auth.CreateRefreshSession(context.Background(), userID, hash, time.Now(), expiresAt)
	s.Require().NoError(err)
	return id
}

func (s *baseSuite) insertLegacyRefreshToken(userID snow.ID, token string, expiresAt time.Time) int64 {
	var id int64
	err := s.db.QueryRowContext(context.Background(),
		"INSERT INTO refresh_tokens (user_id, token, expires_at) VALUES ($1, $2, $3) RETURNING id",
		userID.Int64(), token, expiresAt).Scan(&id)
	s.Require().NoError(err)
	return id
}

func takeSession(ctx context.Context, auth *Auth, id int64) (*domain.RefreshToken, error) {
	got, err := auth.GetRefreshSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return got, auth.DeleteRefreshSession(ctx, id)
}

func (s *AuthRepositorySuite) TestRefreshSessionLifecycle() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	started := time.Now().Add(-time.Minute)
	expiresAt := started.Add(60 * 24 * time.Hour)

	id, err := repo.CreateRefreshSession(ctx, userID, "hash-1", started, expiresAt)
	s.Require().NoError(err)
	s.NotZero(id)

	got, err := repo.GetRefreshSession(ctx, id)
	s.Require().NoError(err)
	s.Equal(userID, got.UserID)
	s.Equal("hash-1", got.TokenHash)
	s.Empty(got.Token)
	s.Empty(got.PreviousHash)
	s.Nil(got.RotatedAt)
	s.Require().NotNil(got.SessionStartedAt)
	s.WithinDuration(started, *got.SessionStartedAt, time.Second)
	s.WithinDuration(expiresAt, got.ExpiresAt, time.Second)

	_, err = repo.GetLegacyRefreshToken(ctx, "hash-1")
	requireRecordNotFound(s.T(), err)

	rotatedAt := time.Now()
	ok, err := repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: id, MatchHash: "hash-1", NewHash: "hash-2", RotatedAt: rotatedAt, ExpiresAt: expiresAt.Add(time.Hour)})
	s.Require().NoError(err)
	s.True(ok)

	ok, err = repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: id, MatchHash: "hash-1", NewHash: "hash-3", RotatedAt: rotatedAt, ExpiresAt: expiresAt})
	s.Require().NoError(err)
	s.False(ok)

	got, err = repo.GetRefreshSession(ctx, id)
	s.Require().NoError(err)
	s.Equal("hash-2", got.TokenHash)
	s.Equal("hash-1", got.PreviousHash)
	s.Require().NotNil(got.RotatedAt)
	s.WithinDuration(rotatedAt, *got.RotatedAt, time.Second)
	s.WithinDuration(started, *got.SessionStartedAt, time.Second)
	s.WithinDuration(expiresAt.Add(time.Hour), got.ExpiresAt, time.Second)

	s.Require().NoError(repo.DeleteRefreshSession(ctx, id))
	_, err = repo.GetRefreshSession(ctx, id)
	requireRecordNotFound(s.T(), err)
	s.Require().NoError(repo.DeleteRefreshSession(ctx, id))
}

func (s *AuthRepositorySuite) TestLegacyRefreshTokenUpgrade() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	userID := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	id := s.insertLegacyRefreshToken(userID, "legacy-token", time.Now().Add(time.Hour))

	got, err := repo.GetLegacyRefreshToken(ctx, "legacy-token")
	s.Require().NoError(err)
	s.Equal(id, got.ID)
	s.Equal("legacy-token", got.Token)
	s.Empty(got.TokenHash)
	s.Nil(got.SessionStartedAt)

	upgrade := domain.RefreshRotation{ID: id, MatchLegacyToken: "legacy-token", NewHash: "hash-1", SessionStartedAt: got.CreatedAt, RotatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	ok, err := repo.RotateRefreshSession(ctx, upgrade)
	s.Require().NoError(err)
	s.True(ok)

	ok, err = repo.RotateRefreshSession(ctx, upgrade)
	s.Require().NoError(err)
	s.False(ok)

	_, err = repo.GetLegacyRefreshToken(ctx, "legacy-token")
	requireRecordNotFound(s.T(), err)

	got, err = repo.GetRefreshSession(ctx, id)
	s.Require().NoError(err)
	s.Equal("hash-1", got.TokenHash)
	s.Empty(got.Token)
	s.NotNil(got.SessionStartedAt)

	var token string
	s.Require().NoError(s.db.QueryRowContext(ctx, "SELECT token FROM refresh_tokens WHERE id = $1", id).Scan(&token))
	s.Equal("hash-1", token)
}

func (s *AuthRepositorySuite) TestRefreshSessionUnknown() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	_, err := repo.GetRefreshSession(ctx, 999)
	requireRecordNotFound(s.T(), err)
	_, err = repo.GetLegacyRefreshToken(ctx, "unknown-token")
	requireRecordNotFound(s.T(), err)
	ok, err := repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: 999, MatchHash: "x", NewHash: "y"})
	s.Require().NoError(err)
	s.False(ok)
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

func (s *AuthRepositorySuite) TestCreateRefreshSessionPrunesExpired() {
	ctx := context.Background()
	repo := NewAuthRepository(s.db)

	alice := seedUser(s.T(), s.q, "alice", "alice@example.com", sql.NullString{})
	bob := seedPBACUser(s.T(), s.db, 43)
	past := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	s.insertLegacyRefreshToken(alice, "alice-old", past)
	s.insertLegacyRefreshToken(bob, "bob-old", past)
	s.insertLegacyRefreshToken(alice, "alice-recent", recent)
	now := time.Now()
	_, err := repo.CreateRefreshSession(ctx, alice, "alice-live", now, now.Add(time.Hour))
	s.Require().NoError(err)
	_, err = repo.CreateRefreshSession(ctx, alice, "alice-new", now, now.Add(2*time.Hour))
	s.Require().NoError(err)

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

	_, err = repo.CreateRefreshSession(ctx, alice, "alice-live", now, now.Add(time.Hour))
	s.Error(err)
	s.Require().NoError(s.db.Close())
	_, err = repo.CreateRefreshSession(ctx, alice, "after-close", now, now.Add(time.Hour))
	s.Error(err)
	_, err = repo.GetRefreshSession(ctx, 1)
	s.Error(err)
	_, err = repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: 1, MatchLegacyToken: "x"})
	s.Error(err)
	s.Error(repo.DeleteRefreshSession(ctx, 1))
}
