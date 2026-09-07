package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

func seedOrg(t *testing.T, q *sqlite.Queries, name, slug string) snow.ID {
	t.Helper()

	node := newTestNode(t)
	id := node.Generate()

	_, err := q.CreateOrganization(context.Background(), sqlite.CreateOrganizationParams{
		ID:   id.Int64(),
		Name: name,
		Slug: slug,
	})
	require.NoError(t, err)
	return id
}

func TestOrgRepositorySQLite_GetBySlug_Success(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	orgID := seedOrg(t, q, "Acme Corp", "acme")

	got, err := repo.GetBySlug(ctx, "acme")
	require.NoError(t, err)
	require.Equal(t, orgID, got.ID)
	require.Equal(t, "acme", got.Slug)
	require.Equal(t, "Acme Corp", got.Name)
	require.False(t, got.CreatedAt.IsZero())
	require.False(t, got.UpdatedAt.IsZero())
}

func TestOrgRepositorySQLite_GetBySlug_SeededDefault(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	got, err := repo.GetBySlug(ctx, "default")
	require.NoError(t, err)
	require.Equal(t, snow.ID(1), got.ID)
	require.Equal(t, "default", got.Slug)
	require.Equal(t, "Default Organization", got.Name)
}

func TestOrgRepositorySQLite_GetBySlug_NotFound(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	_, err := repo.GetBySlug(ctx, "does-not-exist")
	requireRecordNotFound(t, err)
}

func TestOrgRepositorySQLite_GetBySlug_DeletedOrg(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewOrgRepository(db)

	orgID := seedOrg(t, q, "Acme Corp", "acme")
	require.NoError(t, q.DeleteOrganization(ctx, orgID.Int64()))

	_, err := repo.GetBySlug(ctx, "acme")
	requireRecordNotFound(t, err)
}
