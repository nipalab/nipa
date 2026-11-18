package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
)

func TestImportRefWritesJoinTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	branches := NewBranchRepository(db)
	tags := NewTagRepository(db)
	projectID := seedProject(t, q, 1, "import")
	node := newTestNode(t)
	commitID := node.Generate()
	_, err := db.ExecContext(ctx,
		`INSERT INTO commits (id, hash, project_id, tree_id, user_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
		commitID.Int64(), domain.Hash{1}.Bytes(), projectID.Int64(), 1, 1, "imported",
	)
	require.NoError(t, err)
	mainID := seedBranch(t, db, projectID, "main", sql.NullInt64{})
	otherID := seedBranch(t, db, projectID, "other", sql.NullInt64{})
	require.NoError(t, branches.SetDefaultBranch(ctx, projectID, mainID))

	require.NoError(t, branches.UpdateCommitIf(ctx, mainID, nil, &commitID), "a branch without commits moves from NULL")
	main, err := branches.GetByProjectIDAndID(ctx, projectID, mainID)
	require.NoError(t, err)
	require.Equal(t, commitID, *main.CommitID)
	require.True(t, domain.IsErrorConflict(branches.UpdateCommitIf(ctx, mainID, nil, &commitID)))

	rollback := errors.New("rollback")
	err = dbtx.NewTransactor(db).WithinTx(ctx, func(ctx context.Context) error {
		require.NoError(t, NewPushRepository(db).LockProject(ctx, projectID))
		require.NoError(t, branches.SetDefaultBranch(ctx, projectID, otherID))
		_, err := tags.CreateTag(ctx, domain.Tag{ID: node.Generate(), ProjectID: projectID, Name: "v1", CommitID: commitID, UserID: 1})
		require.NoError(t, err)
		_, err = tags.GetTagByName(ctx, projectID, "v1")
		require.NoError(t, err)
		return rollback
	})
	require.ErrorIs(t, err, rollback)

	def, err := branches.GetDefaultBranch(ctx, projectID)
	require.NoError(t, err)
	require.Equal(t, mainID, def.ID)
	_, err = tags.GetTagByName(ctx, projectID, "v1")
	require.True(t, domain.IsErrorNotFound(err))
}
