package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func newSQLiteTestDB(t *testing.T) (*sql.DB, *sqlcSqlite.Queries) {
	t.Helper()

	db, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	db.SetMaxOpenConns(1)

	require.NoError(t, database.MigrateUp(db, "sqlite3"))
	return db, sqlcSqlite.New(db)
}

func seedUser(t *testing.T, q *sqlcSqlite.Queries, name, email string, photoUrl sql.NullString) snow.ID {
	t.Helper()

	node, err := snow.NewNode(1)
	require.NoError(t, err)
	id, err := q.UserCreate(context.Background(), sqlcSqlite.UserCreateParams{
		ID:       node.Generate().Int64(),
		Name:     name,
		Email:    email,
		Password: "hashed-password",
		PhotoUrl: photoUrl,
		IsAdmin:  true,
	})
	require.NoError(t, err)
	return snow.ID(id)
}

func requireRecordNotFound(t *testing.T, err error) {
	t.Helper()

	require.Error(t, err)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, "record not found", domErr.Message)
}

func TestUserRepositorySQLite_GetByEmail(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewUserRepository(db)

	seedUser(t, q, "alice", "alice@example.com", sql.NullString{String: "https://example.com/alice.png", Valid: true})

	got, err := repo.GetByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	require.NotZero(t, got.ID)
	require.Equal(t, "alice", got.Name)
	require.Equal(t, "alice@example.com", got.Email)
	require.Equal(t, "hashed-password", got.Password)
	require.Equal(t, "https://example.com/alice.png", got.PhotoUrl)
	require.False(t, got.IsSuperAdmin)
	require.True(t, got.IsAdmin)
	require.False(t, got.Deleted)
	require.Nil(t, got.DeletedAt)
	require.False(t, got.CreatedAt.IsZero())
	require.False(t, got.UpdatedAt.IsZero())
}

func TestUserRepositorySQLite_GetByEmail_NullPhotoURL(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewUserRepository(db)

	seedUser(t, q, "bob", "bob@example.com", sql.NullString{})

	got, err := repo.GetByEmail(ctx, "bob@example.com")
	require.NoError(t, err)
	require.Empty(t, got.PhotoUrl)
}

func TestUserRepositorySQLite_GetByID(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewUserRepository(db)

	id := seedUser(t, q, "carol", "carol@example.com", sql.NullString{})

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	require.Equal(t, "carol", got.Name)
	require.Equal(t, "carol@example.com", got.Email)
}

func TestUserRepositorySQLite_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewUserRepository(db)

	_, err := repo.GetByEmail(ctx, "missing@example.com")
	requireRecordNotFound(t, err)

	_, err = repo.GetByID(ctx, 999999)
	requireRecordNotFound(t, err)
}

func TestUserRepositorySQLite_DeletedUserIsNotReturned(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewUserRepository(db)

	id := seedUser(t, q, "dave", "dave@example.com", sql.NullString{})
	require.NoError(t, q.UserDeleteByID(ctx, id.Int64()))

	_, err := repo.GetByEmail(ctx, "dave@example.com")
	requireRecordNotFound(t, err)

	_, err = repo.GetByID(ctx, id)
	requireRecordNotFound(t, err)
}

func TestUserRepositorySQLite_CRUD(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewUserRepository(db)

	node := newTestNode(t)
	created, err := repo.Create(ctx, domain.User{
		ID: node.Generate(), Name: "bob", Email: "bob@example.com", Password: "hash",
	})
	require.NoError(t, err)
	require.Equal(t, "bob", created.Name)
	require.Equal(t, "bob@example.com", created.Email)
	require.False(t, created.Deleted)

	users, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2, "seeded super admin plus the created user")

	require.NoError(t, repo.UpdateProfile(ctx, created.ID, "bobby", "https://example.com/b.png"))
	got, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "bobby", got.Name)
	require.Equal(t, "https://example.com/b.png", got.PhotoUrl)

	require.NoError(t, repo.UpdateEmail(ctx, created.ID, "robert@example.com"))
	require.NoError(t, repo.UpdatePassword(ctx, created.ID, "new-hash"))
	require.NoError(t, repo.UpdateAdminFlags(ctx, created.ID, true, false))

	got, err = repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "robert@example.com", got.Email)
	require.Equal(t, "new-hash", got.Password)
	require.True(t, got.IsAdmin)

	require.NoError(t, repo.Deactivate(ctx, created.ID))
	_, err = repo.GetByID(ctx, created.ID)
	requireRecordNotFound(t, err)
}

func TestUserRepositorySQLite_Create_DuplicateEmail(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewUserRepository(db)

	seedUser(t, q, "alice", "alice@example.com", sql.NullString{})
	node := newTestNode(t)
	_, err := repo.Create(ctx, domain.User{
		ID: node.Generate(), Name: "alice2", Email: "alice@example.com", Password: "hash",
	})
	require.Error(t, err)
}
