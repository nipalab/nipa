package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
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

func TestAuthRepositorySQLite_SaveRefreshTokenPrunesExpired(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	alice := seedUser(t, q, "alice", "alice@example.com", sql.NullString{})
	bob := seedPBACUser(t, db, 43)
	past := time.Now().Add(-time.Hour)
	for _, row := range []sqlcSqlite.RefreshTokenCreateParams{
		{UserID: alice.Int64(), Token: "alice-old", ExpiresAt: past},
		{UserID: bob.Int64(), Token: "bob-old", ExpiresAt: past},
	} {
		_, err := q.RefreshTokenCreate(ctx, row)
		require.NoError(t, err)
	}
	require.NoError(t, repo.SaveRefreshToken(ctx, alice, "alice-live", time.Now().Add(time.Hour)))
	require.NoError(t, repo.SaveRefreshToken(ctx, alice, "alice-new", time.Now().Add(2*time.Hour)))

	rows, err := db.QueryContext(ctx, "SELECT token FROM refresh_tokens ORDER BY token")
	require.NoError(t, err)
	defer rows.Close()
	var tokens []string
	for rows.Next() {
		var token string
		require.NoError(t, rows.Scan(&token))
		tokens = append(tokens, token)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"alice-live", "alice-new", "bob-old"}, tokens)

	require.Error(t, repo.SaveRefreshToken(ctx, alice, "alice-live", time.Now().Add(time.Hour)))
	require.NoError(t, db.Close())
	require.Error(t, repo.SaveRefreshToken(ctx, alice, "after-close", time.Now().Add(time.Hour)))
}
