package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestTagRepositorySQLite_CRUD(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewTagRepository(db)

	projectID := seedProject(t, q, 1, "game")
	otherProjectID := seedProject(t, q, 1, "other")
	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})
	commitID := seedCommit(t, db, q, projectID, treeID)
	userID := seedPBACUser(t, db, 42)

	created, err := repo.CreateTag(ctx, domain.Tag{
		ID:        1001,
		ProjectID: projectID,
		Name:      "v1.0.0",
		CommitID:  commitID,
		Message:   "first release",
		UserID:    userID,
	})
	require.NoError(t, err)
	require.Equal(t, snow.ID(1001), created.ID)
	require.Equal(t, projectID, created.ProjectID)
	require.Equal(t, "v1.0.0", created.Name)
	require.Equal(t, commitID, created.CommitID)
	require.Equal(t, "first release", created.Message)
	require.Equal(t, userID, created.UserID)
	require.False(t, created.CreatedAt.IsZero())

	got, err := repo.GetTagByName(ctx, projectID, "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, created.CommitID, got.CommitID)
	require.Equal(t, created.Message, got.Message)

	_, err = repo.GetTagByName(ctx, projectID, "missing")
	requireRecordNotFound(t, err)

	_, err = repo.CreateTag(ctx, domain.Tag{
		ID:        1002,
		ProjectID: projectID,
		Name:      "v1.0.0",
		CommitID:  commitID,
		UserID:    userID,
	})
	require.Error(t, err, "a duplicate tag name in the same project must be rejected")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)

	other, err := repo.CreateTag(ctx, domain.Tag{
		ID:        1003,
		ProjectID: otherProjectID,
		Name:      "v1.0.0",
		CommitID:  commitID,
		UserID:    userID,
	})
	require.NoError(t, err, "the same tag name in another project must be allowed")
	require.Equal(t, otherProjectID, other.ProjectID)

	requireRecordNotFound(t, repo.DeleteTag(ctx, otherProjectID, created.ID))
	_, err = repo.GetTagByName(ctx, projectID, "v1.0.0")
	require.NoError(t, err, "a mismatched project delete must not remove the tag")

	require.NoError(t, repo.DeleteTag(ctx, projectID, created.ID))
	_, err = repo.GetTagByName(ctx, projectID, "v1.0.0")
	requireRecordNotFound(t, err)
	requireRecordNotFound(t, repo.DeleteTag(ctx, projectID, created.ID))

	_, err = repo.GetTagByName(ctx, otherProjectID, "v1.0.0")
	require.NoError(t, err, "deleting one project's tag must not touch another project's tag")
}

func TestTagRepositorySQLite_ListTags(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	repo := NewTagRepository(db)

	projectID := seedProject(t, q, 1, "game")
	otherProjectID := seedProject(t, q, 1, "other")
	treeID := seedTreeNode(t, db, "root", sql.NullInt64{})
	commitID := seedCommit(t, db, q, projectID, treeID)
	userID := seedPBACUser(t, db, 42)

	list, err := repo.ListTags(ctx, projectID, 10, nil, 0)
	require.NoError(t, err)
	require.Empty(t, list)

	now := time.Now().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		tag, err := repo.CreateTag(ctx, domain.Tag{
			ID:        snow.ID(2000 + i),
			ProjectID: projectID,
			Name:      fmt.Sprintf("v1.0.%d", i),
			CommitID:  commitID,
			UserID:    userID,
		})
		require.NoError(t, err)
		ts := now.Add(time.Duration(i) * time.Minute)
		_, err = db.ExecContext(ctx, `UPDATE tags SET created_at = ?, updated_at = ? WHERE id = ?`, ts, ts, tag.ID.Int64())
		require.NoError(t, err)
	}

	_, err = repo.CreateTag(ctx, domain.Tag{
		ID:        3000,
		ProjectID: otherProjectID,
		Name:      "yanked",
		CommitID:  commitID,
		UserID:    userID,
	})
	require.NoError(t, err)

	all, err := repo.ListTags(ctx, projectID, 10, nil, 0)
	require.NoError(t, err)
	require.Len(t, all, 5, "tags must be isolated per project")
	require.Equal(t, "v1.0.4", all[0].Name, "newest tag first")

	firstPage, err := repo.ListTags(ctx, projectID, 2, nil, 0)
	require.NoError(t, err)
	require.Len(t, firstPage, 2)
	require.Equal(t, "v1.0.4", firstPage[0].Name)
	require.Equal(t, "v1.0.3", firstPage[1].Name)

	last := firstPage[len(firstPage)-1]
	secondPage, err := repo.ListTags(ctx, projectID, 2, &last.CreatedAt, last.ID)
	require.NoError(t, err)
	require.Len(t, secondPage, 2)
	require.Equal(t, "v1.0.2", secondPage[0].Name)
	require.Equal(t, "v1.0.1", secondPage[1].Name)

	last = secondPage[len(secondPage)-1]
	thirdPage, err := repo.ListTags(ctx, projectID, 2, &last.CreatedAt, last.ID)
	require.NoError(t, err)
	require.Len(t, thirdPage, 1)
	require.Equal(t, "v1.0.0", thirdPage[0].Name)
}
