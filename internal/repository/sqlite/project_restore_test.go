package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	database "github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestProjectRepositorySQLite_DeletedAndRestore(t *testing.T) {
	db, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	require.NoError(t, database.MigrateUp(db, "sqlite3"))

	repo := NewProjectRepository(db)
	ctx := context.Background()
	node, err := snow.NewNode(1)
	require.NoError(t, err)

	live, err := repo.Create(ctx, domain.Project{ID: node.Generate(), OrgID: 1, Name: "live"})
	require.NoError(t, err)
	gone, err := repo.Create(ctx, domain.Project{ID: node.Generate(), OrgID: 1, Name: "gone"})
	require.NoError(t, err)
	require.NoError(t, repo.Delete(ctx, gone.ID))

	_, err = repo.GetDeleted(ctx, 1, live.ID)
	require.True(t, domain.IsErrorNotFound(err), "a live project is not a deleted one")
	_, err = repo.GetDeleted(ctx, 2, gone.ID)
	require.True(t, domain.IsErrorNotFound(err), "another org's project")
	deleted, err := repo.GetDeleted(ctx, 1, gone.ID)
	require.NoError(t, err)
	require.True(t, deleted.Deleted)
	require.NotNil(t, deleted.DeletedAt)

	listed, err := repo.ListDeletedByOrgID(ctx, 1)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, gone.ID, listed[0].ID)

	future := time.Now().Add(time.Hour)
	restored, err := repo.Restore(ctx, gone.ID, &future)
	require.NoError(t, err)
	require.False(t, restored, "deleted before the window start")

	past := time.Now().Add(-time.Hour)
	restored, err = repo.Restore(ctx, gone.ID, &past)
	require.NoError(t, err)
	require.True(t, restored)
	back, err := repo.Get(ctx, gone.ID)
	require.NoError(t, err)
	require.False(t, back.Deleted)
	require.Nil(t, back.DeletedAt)

	restored, err = repo.Restore(ctx, gone.ID, nil)
	require.NoError(t, err)
	require.False(t, restored, "a live project is not restored again")

	require.NoError(t, repo.Delete(ctx, gone.ID))
	restored, err = repo.Restore(ctx, gone.ID, nil)
	require.NoError(t, err)
	require.True(t, restored, "no window restores any deleted project")

	listed, err = repo.ListDeletedByOrgID(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, listed)
}

func TestProjectRepositorySQLite_DeletedErrors(t *testing.T) {
	db, err := database.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	require.NoError(t, database.MigrateUp(db, "sqlite3"))
	repo := NewProjectRepository(db)
	require.NoError(t, db.Close())

	ctx := context.Background()
	_, err = repo.ListDeletedByOrgID(ctx, 1)
	require.Error(t, err)
	_, err = repo.Restore(ctx, 1, nil)
	require.Error(t, err)
}
