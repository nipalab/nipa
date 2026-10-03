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

func TestMergeRequestAndFileLockTx_RollsBackTogether(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	mrRepo := NewMergeRequestRepository(db)
	lockRepo := NewFileLockRepository(db)

	projectID := seedProject(t, q, 1, "game")
	userID := seedPBACUser(t, db, 42)
	sourceID := seedBranch(t, db, projectID, "feature", sql.NullInt64{})
	targetID := seedBranch(t, db, projectID, "main", sql.NullInt64{})

	err := dbtx.NewTransactor(db).WithinTx(ctx, func(ctx context.Context) error {
		if _, err := mrRepo.Create(ctx, domain.MergeRequest{
			ID:             5001,
			ProjectID:      projectID,
			SourceBranchID: sourceID,
			TargetBranchID: targetID,
			SourceBranch:   "feature",
			TargetBranch:   "main",
			Title:          "tx",
			CreatedBy:      userID,
		}); err != nil {
			return err
		}
		if _, err := lockRepo.Create(ctx, domain.FileLock{
			ID:        6001,
			ProjectID: projectID,
			Path:      "tex.png",
			HeldBy:    userID,
		}); err != nil {
			return err
		}
		return errors.New("lock conflict")
	})
	require.Error(t, err)

	_, err = mrRepo.Get(ctx, projectID, 1)
	requireRecordNotFound(t, err)

	locks, err := lockRepo.ListProject(ctx, projectID)
	require.NoError(t, err)
	require.Empty(t, locks)
}
