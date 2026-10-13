package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuthRepositorySQLite_SaveAndGetAndDeleteRefreshToken(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	userID := seedUser(t, q, "alice", "alice@example.com", sql.NullString{})
	expiresAt := time.Now().Add(60 * 24 * time.Hour)

	require.NoError(t, repo.SaveRefreshToken(ctx, userID, "token-1", expiresAt))

	got, err := repo.GetAndDeleteRefreshToken(ctx, "token-1")
	require.NoError(t, err)
	require.NotZero(t, got.ID)
	require.Equal(t, userID, got.UserID)
	require.Equal(t, "token-1", got.Token)
	require.WithinDuration(t, expiresAt, got.ExpiresAt, time.Second)
	require.False(t, got.CreatedAt.IsZero())
}

func TestAuthRepositorySQLite_RefreshTokenIsSingleUse(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	userID := seedUser(t, q, "bob", "bob@example.com", sql.NullString{})
	require.NoError(t, repo.SaveRefreshToken(ctx, userID, "token-2", time.Now().Add(time.Hour)))

	_, err := repo.GetAndDeleteRefreshToken(ctx, "token-2")
	require.NoError(t, err)

	_, err = repo.GetAndDeleteRefreshToken(ctx, "token-2")
	requireRecordNotFound(t, err)
}

func TestAuthRepositorySQLite_GetAndDeleteUnknownToken(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	_, err := repo.GetAndDeleteRefreshToken(ctx, "unknown-token")
	requireRecordNotFound(t, err)
}

func TestAuthRepositorySQLite_SSHLoginChallengeLifecycle(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	expiresAt := time.Now().Add(time.Minute)
	require.NoError(t, repo.CreateSSHLoginChallenge(ctx, "alice@example.com", "nonce-1", expiresAt))

	got, err := repo.ConsumeSSHLoginChallenge(ctx, "nonce-1")
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", got.Email)
	require.Equal(t, "nonce-1", got.Nonce)
	require.WithinDuration(t, expiresAt, got.ExpiresAt, time.Second)
	require.False(t, got.CreatedAt.IsZero())

	_, err = repo.ConsumeSSHLoginChallenge(ctx, "nonce-1")
	requireRecordNotFound(t, err)
}

func TestAuthRepositorySQLite_SSHLoginChallengeUnknown(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	_, err := repo.ConsumeSSHLoginChallenge(context.Background(), "missing")
	requireRecordNotFound(t, err)
}

func TestAuthRepositorySQLite_SSHLoginChallengeDeleteExpired(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	require.NoError(t, repo.CreateSSHLoginChallenge(ctx, "alice@example.com", "expired", time.Now().Add(-time.Minute)))
	require.NoError(t, repo.CreateSSHLoginChallenge(ctx, "alice@example.com", "live", time.Now().Add(time.Minute)))

	require.NoError(t, repo.DeleteExpiredSSHLoginChallenges(ctx, time.Now()))

	_, err := repo.ConsumeSSHLoginChallenge(ctx, "expired")
	requireRecordNotFound(t, err)

	got, err := repo.ConsumeSSHLoginChallenge(ctx, "live")
	require.NoError(t, err)
	require.Equal(t, "live", got.Nonce)
}
