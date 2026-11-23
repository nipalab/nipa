package postgres

import (
	"context"
	"errors"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

func (s *PushRepositorySuite) TestChunkPacksAndLocations() {
	ctx := context.Background()
	repo, _, projectID, _ := s.newPushTestEnv()
	other := seedProject(s.T(), s.q, 1, "other-project")

	packID := snow.ID(4242)
	s.Require().NoError(repo.InsertChunkPack(ctx, usecase.ChunkPack{ID: packID, ProjectID: projectID, SizeBytes: 100, ChunkCount: 2}))
	looseHash, looseSize, _ := chunkRow(s.T(), "loose chunk")
	packedHash, packedSize, _ := chunkRow(s.T(), "packed chunk")
	missing, _, _ := chunkRow(s.T(), "never recorded")
	packed := usecase.ChunkLocation{PackID: packID, Offset: 4}
	s.Require().NoError(repo.InsertChunkRows(ctx, projectID, []usecase.ChunkRow{
		{Hash: looseHash, SizeBytes: looseSize},
		{Hash: packedHash, SizeBytes: packedSize, Location: packed},
	}))

	records, err := repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash, packedHash, missing})
	s.Require().NoError(err)
	s.Require().Equal(map[domain.Hash]usecase.ChunkRecord{
		looseHash:  {SizeBytes: looseSize},
		packedHash: {SizeBytes: packedSize, Location: packed, PackState: usecase.ChunkPackPending},
	}, records)

	s.Require().NoError(repo.SetChunkPackState(ctx, packID, usecase.ChunkPackStored))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{packedHash})
	s.Require().NoError(err)
	s.Require().Equal(usecase.ChunkPackStored, records[packedHash].PackState)
	s.Require().Error(repo.SetChunkPackState(ctx, snow.ID(999), usecase.ChunkPackStored), "an unknown pack")

	// Inserting again with another location keeps the row's location.
	s.Require().NoError(repo.InsertChunkRows(ctx, projectID, []usecase.ChunkRow{{Hash: looseHash, SizeBytes: looseSize, Location: packed}}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash})
	s.Require().NoError(err)
	s.Require().False(records[looseHash].Location.Packed())

	// Other projects see nothing.
	records, err = repo.ProjectChunkRecords(ctx, other, []domain.Hash{looseHash, packedHash})
	s.Require().NoError(err)
	s.Require().Empty(records)

	// A move applies only while the row still points at its From location.
	newPack := snow.ID(4343)
	s.Require().NoError(repo.InsertChunkPack(ctx, usecase.ChunkPack{ID: newPack, ProjectID: projectID, SizeBytes: 50, ChunkCount: 1}))
	moved := usecase.ChunkLocation{PackID: newPack, Offset: 4}
	s.Require().NoError(repo.RelocateChunks(ctx, projectID, []usecase.ChunkRelocation{
		{Hash: looseHash, From: usecase.ChunkLocation{PackID: packID, Offset: 4}, To: moved}, // stale From: no-op
		{Hash: packedHash, From: packed, To: moved},
	}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash, packedHash})
	s.Require().NoError(err)
	s.Require().False(records[looseHash].Location.Packed(), "a stale move is ignored")
	s.Require().Equal(moved, records[packedHash].Location)
	s.Require().NoError(repo.RelocateChunks(ctx, projectID, []usecase.ChunkRelocation{{Hash: looseHash, From: usecase.ChunkLocation{}, To: moved}}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash})
	s.Require().NoError(err)
	s.Require().Equal(moved, records[looseHash].Location, "a lost loose row moves into a pack")
	s.Require().NoError(repo.RelocateChunks(ctx, projectID, []usecase.ChunkRelocation{{Hash: looseHash, From: moved, To: usecase.ChunkLocation{}}}))
	records, err = repo.ProjectChunkRecords(ctx, projectID, []domain.Hash{looseHash})
	s.Require().NoError(err)
	s.Require().False(records[looseHash].Location.Packed(), "and back to a loose object")

	// The pack writes join the caller's transaction.
	tx := dbtx.NewTransactor(s.db)
	rollback := errors.New("rollback")
	err = tx.WithinTx(ctx, func(ctx context.Context) error {
		s.Require().NoError(repo.InsertChunkPack(ctx, usecase.ChunkPack{ID: 4444, ProjectID: projectID, SizeBytes: 1, ChunkCount: 1}))
		return rollback
	})
	s.Require().ErrorIs(err, rollback)
	s.Require().Error(repo.SetChunkPackState(ctx, snow.ID(4444), usecase.ChunkPackStored), "the rolled back pack is gone")
}
