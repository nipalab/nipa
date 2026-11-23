package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/repository/pushbatch"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// ProjectChunkRecords returns the row of every hash the project has a chunk
// row for: its size, its location and, for a packed row, its pack's state.
// Other hashes are absent. It joins the transaction the context carries, if
// any.
func (p *PushRepository) ProjectChunkRecords(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]usecase.ChunkRecord, error) {
	q := sqlcSqlite.New(dbtx.New(p.db))
	records := make(map[domain.Hash]usecase.ChunkRecord, len(hashes))
	err := pushbatch.LookupBatches(hashes, func(batch [][]byte) error {
		rows, err := q.ProjectChunkRecords(ctx, sqlcSqlite.ProjectChunkRecordsParams{
			ProjectID: projectID.Int64(),
			Hashes:    batch,
		})
		if err != nil {
			return handleError(err)
		}
		for _, row := range rows {
			hash, err := domain.HashFromBytes(row.Hash)
			if err != nil {
				return domain.NewErrorDatabase(err.Error())
			}
			record := usecase.ChunkRecord{SizeBytes: row.SizeBytes}
			if row.PackID.Valid {
				record.Location = usecase.ChunkLocation{PackID: snow.ID(row.PackID.Int64), Offset: row.PackOffset.Int64}
				record.PackState = usecase.ChunkPackState(row.PackState.String)
			}
			records[hash] = record
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// RelocateChunks points each row at its move's new location, only while the
// row still points at the move's old one (a compare-and-set, so a concurrent
// move wins and is kept). It joins the transaction the context carries, if
// any.
func (p *PushRepository) RelocateChunks(ctx context.Context, projectID snow.ID, moves []usecase.ChunkRelocation) error {
	q := sqlcSqlite.New(dbtx.New(p.db))
	for _, move := range moves {
		newPack, newOffset := locationColumns(move.To)
		oldPack, oldOffset := locationColumns(move.From)
		if _, err := q.ChunkRelocate(ctx, sqlcSqlite.ChunkRelocateParams{
			NewPackID:     newPack,
			NewPackOffset: newOffset,
			ProjectID:     projectID.Int64(),
			Hash:          move.Hash.Bytes(),
			OldPackID:     oldPack,
			OldPackOffset: oldOffset,
		}); err != nil {
			return handleError(err)
		}
	}
	return nil
}

// locationColumns maps a location to chunks.pack_id and pack_offset (both
// NULL for a loose row).
func locationColumns(location usecase.ChunkLocation) (sql.NullInt64, sql.NullInt64) {
	if !location.Packed() {
		return sql.NullInt64{}, sql.NullInt64{}
	}
	return sql.NullInt64{Int64: location.PackID.Int64(), Valid: true}, sql.NullInt64{Int64: location.Offset, Valid: true}
}

// InsertChunkPack records a pack in the pending state, before its object is
// written. It joins the transaction the context carries, if any.
func (p *PushRepository) InsertChunkPack(ctx context.Context, pack usecase.ChunkPack) error {
	q := sqlcSqlite.New(dbtx.New(p.db))
	return handleError(q.ChunkPackInsert(ctx, sqlcSqlite.ChunkPackInsertParams{
		ID:         pack.ID.Int64(),
		ProjectID:  pack.ProjectID.Int64(),
		SizeBytes:  pack.SizeBytes,
		ChunkCount: int64(pack.ChunkCount),
		State:      string(usecase.ChunkPackPending),
	}))
}

// SetChunkPackState moves a pack to state; an unknown pack is an error. It
// joins the transaction the context carries, if any.
func (p *PushRepository) SetChunkPackState(ctx context.Context, packID snow.ID, state usecase.ChunkPackState) error {
	q := sqlcSqlite.New(dbtx.New(p.db))
	n, err := q.ChunkPackSetState(ctx, sqlcSqlite.ChunkPackSetStateParams{State: string(state), ID: packID.Int64()})
	if err != nil {
		return handleError(err)
	}
	if n == 0 {
		return domain.NewErrorDatabase(fmt.Sprintf("chunk pack %s not found", packID.Base36()))
	}
	return nil
}
