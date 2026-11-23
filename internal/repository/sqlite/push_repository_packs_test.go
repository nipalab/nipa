package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func TestPushRepositorySQLite_ChunkPacksAndLocations(t *testing.T) {
	ctx := context.Background()
	repo, _, db, projectID, _ := newPushTestEnv(t)
	q := sqlcSqlite.New(db)
	other := seedProject(t, q, 1, "other-project")

	packID := snow.ID(4242)
	require.NoError(t, repo.InsertChunkPack(ctx, usecase.ChunkPack{ID: packID, ProjectID: projectID, SizeBytes: 100, ChunkCount: 2}))
	looseHash, looseSize, _ := chunkRow(t, "loose chunk")
	packedHash, packedSize, _ := chunkRow(t, "packed chunk")
	missing, _, _ := chunkRow(t, "never recorded")
	packed := usecase.ChunkLocation{PackID: packID, Offset: 4}
	require.NoError(t, repo.InsertChunkRows(ctx, projectID, []usecase.ChunkRow{
		{Hash: looseHash, SizeBytes: looseSize},
		{Hash: packedHash, SizeBytes: packedSize, Location: packed},
	}))

	records, err := repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash, packedHash, missing})
	require.NoError(t, err)
	require.Equal(t, map[domain.Hash]usecase.ChunkRecord{
		looseHash:  {SizeBytes: looseSize},
		packedHash: {SizeBytes: packedSize, Location: packed, PackState: usecase.ChunkPackPending},
	}, records)

	require.NoError(t, repo.SetChunkPackState(ctx, packID, usecase.ChunkPackStored))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{packedHash})
	require.NoError(t, err)
	require.Equal(t, usecase.ChunkPackStored, records[packedHash].PackState)
	require.Error(t, repo.SetChunkPackState(ctx, snow.ID(999), usecase.ChunkPackStored), "an unknown pack")

	// Inserting again with another location keeps the row's location.
	require.NoError(t, repo.InsertChunkRows(ctx, projectID, []usecase.ChunkRow{{Hash: looseHash, SizeBytes: looseSize, Location: packed}}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash})
	require.NoError(t, err)
	require.False(t, records[looseHash].Location.Packed())

	// Other projects see nothing.
	records, err = repo.ProjectChunkRecords(ctx, other, []domain.Hash{looseHash, packedHash})
	require.NoError(t, err)
	require.Empty(t, records)

	// A move applies only while the row still points at its From location.
	newPack := snow.ID(4343)
	require.NoError(t, repo.InsertChunkPack(ctx, usecase.ChunkPack{ID: newPack, ProjectID: projectID, SizeBytes: 50, ChunkCount: 1}))
	moved := usecase.ChunkLocation{PackID: newPack, Offset: 4}
	require.NoError(t, repo.RelocateChunks(ctx, projectID, []usecase.ChunkRelocation{
		{Hash: looseHash, From: usecase.ChunkLocation{PackID: packID, Offset: 4}, To: moved}, // stale From: no-op
		{Hash: packedHash, From: packed, To: moved},
	}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash, packedHash})
	require.NoError(t, err)
	require.False(t, records[looseHash].Location.Packed(), "a stale move is ignored")
	require.Equal(t, moved, records[packedHash].Location)
	require.NoError(t, repo.RelocateChunks(ctx, projectID, []usecase.ChunkRelocation{{Hash: looseHash, From: usecase.ChunkLocation{}, To: moved}}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash})
	require.NoError(t, err)
	require.Equal(t, moved, records[looseHash].Location, "a lost loose row moves into a pack")
	require.NoError(t, repo.RelocateChunks(ctx, projectID, []usecase.ChunkRelocation{{Hash: looseHash, From: moved, To: usecase.ChunkLocation{}}}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash})
	require.NoError(t, err)
	require.False(t, records[looseHash].Location.Packed(), "and back to a loose object")

	// The pack writes join the caller's transaction.
	tx := dbtx.NewTransactor(db)
	rollback := errors.New("rollback")
	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		require.NoError(t, repo.InsertChunkPack(ctx, usecase.ChunkPack{ID: 4444, ProjectID: projectID, SizeBytes: 1, ChunkCount: 1}))
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	require.Error(t, repo.SetChunkPackState(ctx, snow.ID(4444), usecase.ChunkPackStored), "the rolled back pack is gone")
}
