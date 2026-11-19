package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

func TestImportRefWritesJoinTheCallersTransaction(t *testing.T) {
	ctx := context.Background()
	db, q := newTestDB(t)
	branches := NewBranchRepository(db)
	tags := NewTagRepository(db)
	projectID := seedProject(t, q, 1, "import")
	commitID := seedCommit(t, db, q, projectID, seedTreeNode(t, db, "root", sql.NullInt64{}))
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
		_, err := tags.CreateTag(ctx, domain.Tag{ID: snow.ID(5), ProjectID: projectID, Name: "v1", CommitID: commitID, UserID: seedUser(t, q, "tagger", "tagger@example.com", sql.NullString{})})
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

func TestPushRepositoryPostgres_LockProjectSerializes(t *testing.T) {
	ctx := context.Background()
	db, q := newTestDB(t)
	projectID := seedProject(t, q, 1, "locked")
	repo := NewPushRepository(db)
	tx := dbtx.NewTransactor(db)

	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- tx.WithinTx(ctx, func(ctx context.Context) error {
			if err := repo.LockProject(ctx, projectID); err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	err := tx.WithinTx(waitCtx, func(ctx context.Context) error { return repo.LockProject(ctx, projectID) })
	require.Error(t, err, "a second holder waits for the first")
	close(release)
	require.NoError(t, <-done)
	require.NoError(t, tx.WithinTx(ctx, func(ctx context.Context) error { return repo.LockProject(ctx, projectID) }))
}
