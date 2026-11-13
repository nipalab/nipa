package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
)

type MFARepositorySuite struct {
	baseSuite
}

func TestMFARepositorySuite(t *testing.T) {
	suite.Run(t, new(MFARepositorySuite))
}

func (s *MFARepositorySuite) TestTOTPLifecycle() {
	t := s.T()
	ctx := context.Background()
	repo := NewMFARepository(s.db)
	alice := seedUser(t, s.q, "alice", "alice@example.com", sql.NullString{})
	now := time.Now().UTC()

	_, err := repo.GetTOTP(ctx, alice)
	require.True(t, domain.IsErrorNotFound(err))

	ok, err := repo.SavePendingTOTP(ctx, alice, []byte("secret-1"), now)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.SavePendingTOTP(ctx, alice, []byte("secret-2"), now)
	require.NoError(t, err)
	require.True(t, ok, "an unconfirmed row is replaced")

	got, err := repo.GetTOTP(ctx, alice)
	require.NoError(t, err)
	require.Equal(t, []byte("secret-2"), got.SecretEnc)
	require.False(t, got.Confirmed())

	ok, err = repo.AdvanceTOTPStep(ctx, alice, 10)
	require.NoError(t, err)
	require.False(t, ok, "an unconfirmed row has no login steps")

	ok, err = repo.ConfirmTOTP(ctx, alice, []byte("secret-1"), 5, now)
	require.NoError(t, err)
	require.False(t, ok, "confirm is bound to the secret that was checked")
	ok, err = repo.ConfirmTOTP(ctx, alice, []byte("secret-2"), 5, now)
	require.NoError(t, err)
	require.True(t, ok)

	n, err := repo.CountConfirmedTOTP(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	ok, err = repo.SavePendingTOTP(ctx, alice, []byte("secret-3"), now)
	require.NoError(t, err)
	require.False(t, ok, "a confirmed row is never replaced")

	ok, err = repo.AdvanceTOTPStep(ctx, alice, 5)
	require.NoError(t, err)
	require.False(t, ok, "replayed step")
	ok, err = repo.AdvanceTOTPStep(ctx, alice, 6)
	require.NoError(t, err)
	require.True(t, ok)
	got, err = repo.GetTOTP(ctx, alice)
	require.NoError(t, err)
	require.True(t, got.Confirmed())
	require.EqualValues(t, 6, got.LastStep)

	ok, err = repo.DeleteTOTP(ctx, alice)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.DeleteTOTP(ctx, alice)
	require.NoError(t, err)
	require.False(t, ok)
}

func (s *MFARepositorySuite) TestRecoveryCodes() {
	t := s.T()
	ctx := context.Background()
	repo := NewMFARepository(s.db)
	alice := seedUser(t, s.q, "alice", "alice@example.com", sql.NullString{})
	bob := seedPBACUser(t, s.db, 43)
	now := time.Now().UTC()

	require.NoError(t, repo.ReplaceRecoveryCodes(ctx, alice, []string{"h1", "h2", "h3"}))
	require.NoError(t, repo.ReplaceRecoveryCodes(ctx, bob, []string{"h1"}))
	n, err := repo.CountRecoveryCodes(ctx, alice)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)

	ok, err := repo.UseRecoveryCode(ctx, alice, "h2", now)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.UseRecoveryCode(ctx, alice, "h2", now)
	require.NoError(t, err)
	require.False(t, ok, "single use")
	ok, err = repo.UseRecoveryCode(ctx, alice, "nope", now)
	require.NoError(t, err)
	require.False(t, ok)
	n, err = repo.CountRecoveryCodes(ctx, alice)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	require.NoError(t, repo.ReplaceRecoveryCodes(ctx, alice, []string{"h2", "h4"}))
	n, err = repo.CountRecoveryCodes(ctx, alice)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)

	require.NoError(t, repo.DeleteRecoveryCodes(ctx, alice))
	n, err = repo.CountRecoveryCodes(ctx, alice)
	require.NoError(t, err)
	require.Zero(t, n)
	n, err = repo.CountRecoveryCodes(ctx, bob)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

func (s *MFARepositorySuite) TestChallenges() {
	t := s.T()
	ctx := context.Background()
	repo := NewMFARepository(s.db)
	alice := seedUser(t, s.q, "alice", "alice@example.com", sql.NullString{})
	now := time.Now().UTC()

	require.NoError(t, repo.CreateChallenge(ctx, domain.MFAChallenge{TokenHash: "live", UserID: alice, Method: "password", ExpiresAt: now.Add(time.Minute)}))
	require.NoError(t, repo.CreateChallenge(ctx, domain.MFAChallenge{TokenHash: "old", UserID: alice, Method: "oauth", ExpiresAt: now.Add(-time.Minute)}))

	got, err := repo.GetChallenge(ctx, "live")
	require.NoError(t, err)
	require.Equal(t, alice, got.UserID)
	require.Equal(t, "password", got.Method)
	require.Zero(t, got.Attempts)

	n, err := repo.AddChallengeAttempt(ctx, "live")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = repo.AddChallengeAttempt(ctx, "live")
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	_, err = repo.AddChallengeAttempt(ctx, "missing")
	require.True(t, domain.IsErrorNotFound(err))

	require.NoError(t, repo.DeleteExpiredChallenges(ctx, now))
	_, err = repo.GetChallenge(ctx, "old")
	require.True(t, domain.IsErrorNotFound(err))

	ok, err := repo.DeleteChallenge(ctx, "live")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.DeleteChallenge(ctx, "live")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, repo.CreateChallenge(ctx, domain.MFAChallenge{TokenHash: "again", UserID: alice, Method: "password", ExpiresAt: now.Add(time.Minute)}))
	require.NoError(t, repo.DeleteUserChallenges(ctx, alice))
	_, err = repo.GetChallenge(ctx, "again")
	require.True(t, domain.IsErrorNotFound(err))
}

func (s *MFARepositorySuite) TestSessionRecordsAuthMethod() {
	t := s.T()
	ctx := context.Background()
	repo := NewAuthRepository(s.db)
	alice := seedUser(t, s.q, "alice", "alice@example.com", sql.NullString{})
	now := time.Now().UTC()

	id, err := repo.CreateRefreshSession(ctx, domain.NewRefreshSession{UserID: alice, TokenHash: "h", StartedAt: now, ExpiresAt: now.Add(time.Hour), AuthMethod: "password", MFA: domain.MFATOTP})
	require.NoError(t, err)
	got, err := repo.GetRefreshSession(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "password", got.AuthMethod)
	require.Equal(t, domain.MFATOTP, got.MFA)

	id, err = repo.CreateRefreshSession(ctx, domain.NewRefreshSession{UserID: alice, TokenHash: "h2", StartedAt: now, ExpiresAt: now.Add(time.Hour)})
	require.NoError(t, err)
	got, err = repo.GetRefreshSession(ctx, id)
	require.NoError(t, err)
	require.Empty(t, got.AuthMethod)
	require.Empty(t, got.MFA)
}
