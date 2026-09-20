package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestProjectRepositorySQLite(t *testing.T) {
	db, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()

	require.NoError(t, database.MigrateUp(db, "sqlite3"))

	repo := NewProjectRepository(db)
	ctx := context.Background()

	snowNode, err := snow.NewNode(1)
	require.NoError(t, err)
	created, err := repo.Create(ctx, domain.Project{
		ID:          snowNode.Generate(),
		OrgID:       1,
		Name:        "project-a",
		Description: "first project",
	})
	require.NoError(t, err)
	require.NotZero(t, created.ID)
	require.Equal(t, "project-a", created.Name)
	require.Equal(t, "first project", created.Description)

	got, err := repo.Get(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.Name, got.Name)
	require.Equal(t, created.Description, got.Description)

	_, err = repo.Get(ctx, 999999)
	require.Error(t, err)

	_, err = repo.Create(ctx, domain.Project{ID: snowNode.Generate(), OrgID: 1, Name: "project-b", Description: "second"})
	require.NoError(t, err)

	projects, err := repo.ListByOrgID(ctx, 1)
	require.NoError(t, err)
	require.Len(t, projects, 3)

	created2, err := repo.Create(ctx, domain.Project{ID: snowNode.Generate(), OrgID: 1, Name: "project-c", Description: "third"})
	require.NoError(t, err)
	require.NoError(t, repo.Delete(ctx, created2.ID))

	_, err = repo.Get(ctx, created2.ID)
	require.Error(t, err)
}

func TestProjectRepositorySQLite_GetByOrgIDAndSlug_Success(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewProjectRepository(db)

	projectID := seedProject(t, q, 1, "project-a")

	got, err := repo.GetByOrgIDAndSlug(ctx, 1, "project-a")
	require.NoError(t, err)
	require.Equal(t, projectID, got.ID)
	require.Equal(t, snow.ID(1), got.OrgID)
	require.Equal(t, "project-a", got.Slug)
	require.Equal(t, "project-a", got.Name)
}

func TestProjectRepositorySQLite_GetByOrgIDAndSlug_NotFound(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewProjectRepository(db)

	seedProject(t, q, 1, "project-a")

	_, err := repo.GetByOrgIDAndSlug(ctx, 1, "does-not-exist")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestProjectRepositorySQLite_GetByOrgIDAndSlug_WrongOrg(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewProjectRepository(db)

	seedProject(t, q, 1, "project-a")

	_, err := repo.GetByOrgIDAndSlug(ctx, 2, "project-a")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestProjectRepositorySQLite_GetByOrgIDAndSlug_DeletedProject(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewProjectRepository(db)

	projectID := seedProject(t, q, 1, "project-a")
	require.NoError(t, repo.Delete(ctx, projectID))

	_, err := repo.GetByOrgIDAndSlug(ctx, 1, "project-a")
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestProjectRepositorySQLite_GetByOrgIDAndSlug_SameSlugDifferentOrg(t *testing.T) {
	ctx := context.Background()
	db, _ := newSQLiteTestDB(t)
	repo := NewProjectRepository(db)

	node, err := snow.NewNode(1)
	require.NoError(t, err)

	org1ID := node.Generate()
	org2ID := node.Generate()

	_, err = repo.Create(ctx, domain.Project{ID: org1ID, OrgID: 1, Slug: "shared", Name: "org1-project"})
	require.NoError(t, err)
	_, err = repo.Create(ctx, domain.Project{ID: org2ID, OrgID: 2, Slug: "shared", Name: "org2-project"})
	require.NoError(t, err)

	got1, err := repo.GetByOrgIDAndSlug(ctx, 1, "shared")
	require.NoError(t, err)
	require.Equal(t, org1ID, got1.ID)

	got2, err := repo.GetByOrgIDAndSlug(ctx, 2, "shared")
	require.NoError(t, err)
	require.Equal(t, org2ID, got2.ID)
}

func TestProjectRepositorySQLite_Update(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewProjectRepository(db)

	projectID := seedProject(t, q, 1, "game")

	updated, err := repo.Update(ctx, domain.Project{
		ID: projectID, Name: "Game 2", Description: "the sequel",
	})
	require.NoError(t, err)
	require.Equal(t, "Game 2", updated.Name)
	require.Equal(t, "the sequel", updated.Description)
	require.Equal(t, "game", updated.Slug, "slug is immutable")

	got, err := repo.Get(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, "Game 2", got.Name)
	require.Equal(t, "the sequel", got.Description)
}
