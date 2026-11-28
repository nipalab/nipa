package sqlite

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/pushbatch"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
	"gopkg.in/typ.v4/slices"
)

// PathChangeLog returns the commits of the first-parent walk q describes that
// changed q.Path, plus the walk's root commit (usecase.PathChangeQuery).
func (b *BranchRepository) PathChangeLog(ctx context.Context, q usecase.PathChangeQuery) ([]*domain.CommitLogEntry, error) {
	from, to := q.PrefixRange()
	rows, err := b.queries.PathChangeLog(ctx, sqlcSqlite.PathChangeLogParams{
		ProjectID:     q.ProjectID.Int64(),
		AnyPath:       boolInt(q.AnyPath()),
		Path:          q.Path,
		PrefixFrom:    from,
		PrefixTo:      to,
		RowLimit:      int64(q.Limit),
		StartCommitID: q.StartID.Int64(),
		ScanLimit:     int64(q.ScanLimit),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(row sqlcSqlite.PathChangeLogRow) *domain.CommitLogEntry {
		return commitLogEntryToDomain(sqlcSqlite.CommitLogRow(row))
	}), nil
}

// PathChangeRows returns the stored changes of the given commits at or below
// q.Path.
func (b *BranchRepository) PathChangeRows(ctx context.Context, q usecase.PathChangeQuery, commitIDs []snow.ID) ([]usecase.PathChangeRow, error) {
	if len(commitIDs) == 0 {
		return nil, nil
	}
	from, to := q.PrefixRange()
	rows, err := b.queries.PathChangeRows(ctx, sqlcSqlite.PathChangeRowsParams{
		CommitIds:  slices.Map(commitIDs, func(id snow.ID) int64 { return id.Int64() }),
		AnyPath:    boolInt(q.AnyPath()),
		Path:       q.Path,
		PrefixFrom: from,
		PrefixTo:   to,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(row sqlcSqlite.CommitPathChange) usecase.PathChangeRow {
		return usecase.PathChangeRow{CommitID: snow.ID(row.CommitID), Path: row.Path, Kind: usecase.PathChangeKind(row.Kind)}
	}), nil
}

// UnindexedPathChangeCommits lists the commits with a first parent in the
// walk from startCommitID that have no path change index marker.
func (b *BranchRepository) UnindexedPathChangeCommits(ctx context.Context, projectID, startCommitID snow.ID, scanLimit int) ([]usecase.UnindexedCommit, error) {
	rows, err := b.queries.PathChangeUnindexed(ctx, sqlcSqlite.PathChangeUnindexedParams{
		ProjectID:     projectID.Int64(),
		StartCommitID: startCommitID.Int64(),
		ScanLimit:     int64(scanLimit),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(row sqlcSqlite.PathChangeUnindexedRow) usecase.UnindexedCommit {
		return usecase.UnindexedCommit{ID: snow.ID(row.ID), TreeID: row.TreeID, ParentTreeID: row.ParentTreeID}
	}), nil
}

// RecordPathChanges stores a commit's path change rows and its marker in one
// transaction (joining the one the context carries, if any). Rows and marker
// that already exist are kept, so two reads indexing the same commit agree.
func (b *BranchRepository) RecordPathChanges(ctx context.Context, commitID snow.ID, changes []usecase.PathChange) error {
	return handleError(b.tx.WithinTx(ctx, func(ctx context.Context) error {
		return writePathIndex(ctx, b.queries, commitID, changes)
	}))
}

// writePathIndex writes a commit's path change rows and its marker with q.
func writePathIndex(ctx context.Context, q *sqlcSqlite.Queries, commitID snow.ID, changes []usecase.PathChange) error {
	err := pushbatch.InsertPathChanges(ctx, changes, func(ctx context.Context, payload []byte) error {
		return handleError(q.CommitPathChangeInsertMany(ctx, sqlcSqlite.CommitPathChangeInsertManyParams{
			CommitID: commitID.Int64(),
			Changes:  string(payload),
		}))
	})
	if err != nil {
		return err
	}
	return handleError(q.CommitPathIndexMark(ctx, commitID.Int64()))
}
