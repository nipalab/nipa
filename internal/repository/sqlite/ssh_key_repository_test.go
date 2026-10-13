package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func seedSSHKeyUser(t *testing.T, q *sqlcSqlite.Queries, id int64, name, email string) snow.ID {
	t.Helper()
	created, err := q.UserCreate(context.Background(), sqlcSqlite.UserCreateParams{
		ID:       id,
		Name:     name,
		Email:    email,
		Password: "hashed-password",
		IsAdmin:  true,
	})
	require.NoError(t, err)
	return snow.ID(created)
}

func seedSSHKey(t *testing.T, repo *SSHKeyRepository, userID, id snow.ID, fingerprint string) *domain.SSHPublicKey {
	t.Helper()
	key, err := repo.Create(context.Background(), domain.SSHPublicKey{
		ID:          id,
		UserID:      userID,
		Name:        "key " + fingerprint,
		KeyType:     "ssh-ed25519",
		KeyBlob:     "blob-" + fingerprint,
		Fingerprint: fingerprint,
		Comment:     "test key",
	})
	require.NoError(t, err)
	return key
}

func TestSSHKeyRepositorySQLite_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewSSHKeyRepository(db)
	userID := seedSSHKeyUser(t, q, 1001, "alice", "alice@example.com")

	created, err := repo.Create(ctx, domain.SSHPublicKey{
		ID:          42,
		UserID:      userID,
		Name:        "work laptop",
		KeyType:     "ssh-ed25519",
		KeyBlob:     "AAAA",
		Fingerprint: "SHA256:abc",
		Comment:     "alice@laptop",
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(42), created.ID)
	require.Equal(t, userID, created.UserID)
	require.Equal(t, "work laptop", created.Name)
	require.Equal(t, "ssh-ed25519", created.KeyType)
	require.Equal(t, "AAAA", created.KeyBlob)
	require.Equal(t, "SHA256:abc", created.Fingerprint)
	require.Equal(t, "alice@laptop", created.Comment)
	require.False(t, created.CreatedAt.IsZero())

	got, err := repo.GetByID(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.Fingerprint, got.Fingerprint)

	byFP, err := repo.GetByFingerprint(ctx, "SHA256:abc")
	require.NoError(t, err)
	require.Equal(t, snow.ID(42), byFP.ID)
}

func TestSSHKeyRepositorySQLite_GetByID_NotFound(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewSSHKeyRepository(db)

	_, err := repo.GetByID(context.Background(), 99)
	requireRecordNotFound(t, err)
}

func TestSSHKeyRepositorySQLite_GetByFingerprint_NotFound(t *testing.T) {
	db, _ := newSQLiteTestDB(t)
	repo := NewSSHKeyRepository(db)

	_, err := repo.GetByFingerprint(context.Background(), "SHA256:missing")
	requireRecordNotFound(t, err)
}

func TestSSHKeyRepositorySQLite_ListAndCount(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewSSHKeyRepository(db)
	alice := seedSSHKeyUser(t, q, 1001, "alice", "alice@example.com")
	bob := seedSSHKeyUser(t, q, 1002, "bob", "bob@example.com")

	seedSSHKey(t, repo, alice, 101, "SHA256:a1")
	seedSSHKey(t, repo, alice, 102, "SHA256:a2")
	seedSSHKey(t, repo, bob, 103, "SHA256:b1")

	keys, err := repo.ListByUser(ctx, alice)
	require.NoError(t, err)
	require.Len(t, keys, 2)

	count, err := repo.CountByUser(ctx, alice)
	require.NoError(t, err)
	require.Equal(t, 2, count)

	count, err = repo.CountByUser(ctx, bob)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	count, err = repo.CountByUser(ctx, snow.ID(12345))
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestSSHKeyRepositorySQLite_Delete(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewSSHKeyRepository(db)
	alice := seedSSHKeyUser(t, q, 1001, "alice", "alice@example.com")
	bob := seedSSHKeyUser(t, q, 1002, "bob", "bob@example.com")

	key := seedSSHKey(t, repo, alice, 101, "SHA256:a1")

	requireRecordNotFound(t, repo.Delete(ctx, bob, key.ID))
	_, err := repo.GetByID(ctx, key.ID)
	require.NoError(t, err)

	require.NoError(t, repo.Delete(ctx, alice, key.ID))
	_, err = repo.GetByID(ctx, key.ID)
	requireRecordNotFound(t, err)

	requireRecordNotFound(t, repo.Delete(ctx, alice, key.ID))
}

func TestSSHKeyRepositorySQLite_DuplicateFingerprint(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewSSHKeyRepository(db)
	alice := seedSSHKeyUser(t, q, 1001, "alice", "alice@example.com")
	bob := seedSSHKeyUser(t, q, 1002, "bob", "bob@example.com")

	seedSSHKey(t, repo, alice, 101, "SHA256:shared")

	_, err := repo.Create(ctx, domain.SSHPublicKey{
		ID:          77,
		UserID:      bob,
		KeyType:     "ssh-ed25519",
		KeyBlob:     "AAAA",
		Fingerprint: "SHA256:shared",
	})
	require.Error(t, err)
}
