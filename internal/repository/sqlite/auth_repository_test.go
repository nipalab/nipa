package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func insertLegacyRefreshToken(t *testing.T, db *sql.DB, userID snow.ID, token string, expiresAt time.Time) int64 {
	t.Helper()
	var id int64
	err := db.QueryRowContext(context.Background(),
		"INSERT INTO refresh_tokens (user_id, token, expires_at) VALUES (?, ?, ?) RETURNING id",
		userID.Int64(), token, expiresAt).Scan(&id)
	require.NoError(t, err)
	return id
}

func TestAuthRepositorySQLite_RefreshSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	userID := seedUser(t, q, "alice", "alice@example.com", sql.NullString{})
	started := time.Now().Add(-time.Minute).UTC()
	expiresAt := started.Add(60 * 24 * time.Hour)

	id, err := repo.CreateRefreshSession(ctx, userID, "hash-1", started, expiresAt)
	require.NoError(t, err)
	require.NotZero(t, id)

	got, err := repo.GetRefreshSession(ctx, id)
	require.NoError(t, err)
	require.Equal(t, userID, got.UserID)
	require.Equal(t, "hash-1", got.TokenHash)
	require.Empty(t, got.Token)
	require.Empty(t, got.PreviousHash)
	require.Nil(t, got.RotatedAt)
	require.NotNil(t, got.SessionStartedAt)
	require.WithinDuration(t, started, *got.SessionStartedAt, time.Second)
	require.WithinDuration(t, expiresAt, got.ExpiresAt, time.Second)

	_, err = repo.GetLegacyRefreshToken(ctx, "hash-1")
	requireRecordNotFound(t, err)

	rotatedAt := time.Now().UTC()
	ok, err := repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: id, MatchHash: "hash-1", NewHash: "hash-2", RotatedAt: rotatedAt, ExpiresAt: expiresAt.Add(time.Hour)})
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: id, MatchHash: "hash-1", NewHash: "hash-3", RotatedAt: rotatedAt, ExpiresAt: expiresAt})
	require.NoError(t, err)
	require.False(t, ok, "rotation is conditional on the current hash")

	got, err = repo.GetRefreshSession(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "hash-2", got.TokenHash)
	require.Equal(t, "hash-1", got.PreviousHash)
	require.NotNil(t, got.RotatedAt)
	require.WithinDuration(t, rotatedAt, *got.RotatedAt, time.Second)
	require.WithinDuration(t, started, *got.SessionStartedAt, time.Second)
	require.WithinDuration(t, expiresAt.Add(time.Hour), got.ExpiresAt, time.Second)

	require.NoError(t, repo.DeleteRefreshSession(ctx, id))
	_, err = repo.GetRefreshSession(ctx, id)
	requireRecordNotFound(t, err)
	require.NoError(t, repo.DeleteRefreshSession(ctx, id))
}

func TestAuthRepositorySQLite_LegacyRefreshTokenUpgrade(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	userID := seedUser(t, q, "alice", "alice@example.com", sql.NullString{})
	id := insertLegacyRefreshToken(t, db, userID, "legacy-token", time.Now().Add(time.Hour))

	got, err := repo.GetLegacyRefreshToken(ctx, "legacy-token")
	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	require.Equal(t, "legacy-token", got.Token)
	require.Empty(t, got.TokenHash)
	require.Nil(t, got.SessionStartedAt)

	upgrade := domain.RefreshRotation{ID: id, MatchLegacyToken: "legacy-token", NewHash: "hash-1", SessionStartedAt: got.CreatedAt, RotatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	ok, err := repo.RotateRefreshSession(ctx, upgrade)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = repo.RotateRefreshSession(ctx, upgrade)
	require.NoError(t, err)
	require.False(t, ok, "a legacy token upgrades once")

	_, err = repo.GetLegacyRefreshToken(ctx, "legacy-token")
	requireRecordNotFound(t, err)

	got, err = repo.GetRefreshSession(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "hash-1", got.TokenHash)
	require.Empty(t, got.Token)
	require.Empty(t, got.PreviousHash)
	require.NotNil(t, got.SessionStartedAt)

	var token string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT token FROM refresh_tokens WHERE id = ?", id).Scan(&token))
	require.Equal(t, "hash-1", token, "the plaintext column only keeps the hash")
}

func TestAuthRepositorySQLite_RefreshSessionUnknown(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	_, err := repo.GetRefreshSession(ctx, 999)
	requireRecordNotFound(t, err)
	_, err = repo.GetLegacyRefreshToken(ctx, "unknown-token")
	requireRecordNotFound(t, err)
	ok, err := repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: 999, MatchHash: "x", NewHash: "y"})
	require.NoError(t, err)
	require.False(t, ok)
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

func TestAuthRepositorySQLite_CreateRefreshSessionPrunesExpired(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewAuthRepository(db)

	alice := seedUser(t, q, "alice", "alice@example.com", sql.NullString{})
	bob := seedPBACUser(t, db, 43)
	past := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	insertLegacyRefreshToken(t, db, alice, "alice-old", past)
	insertLegacyRefreshToken(t, db, bob, "bob-old", past)
	insertLegacyRefreshToken(t, db, alice, "alice-recent", recent)
	now := time.Now()
	_, err := repo.CreateRefreshSession(ctx, alice, "alice-live", now, now.Add(time.Hour))
	require.NoError(t, err)
	_, err = repo.CreateRefreshSession(ctx, alice, "alice-new", now, now.Add(2*time.Hour))
	require.NoError(t, err)

	rows, err := db.QueryContext(ctx, "SELECT token FROM refresh_tokens ORDER BY token")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var tokens []string
	for rows.Next() {
		var token string
		require.NoError(t, rows.Scan(&token))
		tokens = append(tokens, token)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"alice-live", "alice-new", "alice-recent", "bob-old"}, tokens)

	_, err = repo.CreateRefreshSession(ctx, alice, "alice-live", now, now.Add(time.Hour))
	require.Error(t, err)
	require.NoError(t, db.Close())
	_, err = repo.CreateRefreshSession(ctx, alice, "after-close", now, now.Add(time.Hour))
	require.Error(t, err)
	_, err = repo.GetRefreshSession(ctx, 1)
	require.Error(t, err)
	_, err = repo.RotateRefreshSession(ctx, domain.RefreshRotation{ID: 1, MatchLegacyToken: "x"})
	require.Error(t, err)
	require.Error(t, repo.DeleteRefreshSession(ctx, 1))
}
