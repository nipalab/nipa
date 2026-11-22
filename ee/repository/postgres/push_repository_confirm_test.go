package postgres

import (
	"context"
	"fmt"
	"sync"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/usecase"
)

// confirmRows builds n distinct chunks; more than one insert batch (1000).
func confirmRows(n int, prefix string) []usecase.ChunkRef {
	rows := make([]usecase.ChunkRef, 0, n)
	for i := 0; i < n; i++ {
		data := fmt.Sprintf("%s-%d", prefix, i)
		rows = append(rows, usecase.ChunkRef{Hash: chunker.Sum([]byte(data)), SizeBytes: int64(len(data))})
	}
	return rows
}

func (s *PushRepositorySuite) chunkSizes() map[domain.Hash]int64 {
	rows, err := s.db.Query(`SELECT hash, size_bytes FROM chunks`)
	s.Require().NoError(err)
	defer func() { _ = rows.Close() }()
	out := map[domain.Hash]int64{}
	for rows.Next() {
		var raw []byte
		var size int64
		s.Require().NoError(rows.Scan(&raw, &size))
		hash, err := domain.HashFromBytes(raw)
		s.Require().NoError(err)
		out[hash] = size
	}
	s.Require().NoError(rows.Err())
	return out
}

func (s *PushRepositorySuite) TestInsertChunksIfNotExist() {
	ctx := context.Background()
	repo, _, _, _ := s.newPushTestEnv()

	s.Require().NoError(repo.InsertChunksIfNotExist(ctx, nil))
	existing, existingSize, _ := chunkRow(s.T(), "already recorded")
	s.Require().NoError(repo.InsertChunkIfNotExists(ctx, existing, existingSize))

	rows := confirmRows(2500, "confirm")
	batch := append([]usecase.ChunkRef{}, rows...)
	batch = append(batch, rows[0], rows[1], usecase.ChunkRef{Hash: existing, SizeBytes: existingSize + 99})
	s.Require().NoError(repo.InsertChunksIfNotExist(ctx, batch))
	s.Require().NoError(repo.InsertChunksIfNotExist(ctx, rows[:10]))

	got := s.chunkSizes()
	s.Len(got, len(rows)+1)
	s.Equal(existingSize, got[existing], "an existing row keeps its size")
	for _, row := range rows {
		s.Equal(row.SizeBytes, got[row.Hash])
	}
}

// TestConcurrentOverlappingInserts runs confirms whose rows overlap in
// opposite orders; sorted batches keep them from deadlocking.
func (s *PushRepositorySuite) TestConcurrentOverlappingInserts() {
	ctx := context.Background()
	repo, _, projectID, _ := s.newPushTestEnv()
	rows := confirmRows(1500, "overlap")
	hashes := make([]domain.Hash, 0, len(rows))
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 4; i++ {
		reversed := make([]usecase.ChunkRef, len(rows))
		for j := range rows {
			if i%2 == 0 {
				reversed[j] = rows[j]
			} else {
				reversed[j] = rows[len(rows)-1-j]
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.InsertChunksIfNotExist(ctx, reversed)
			errs <- repo.LinkProjectChunks(ctx, projectID, hashes)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}
	s.Len(s.chunkSizes(), len(rows))
	linked, err := repo.LinkedProjectChunkSizes(ctx, projectID, hashes)
	s.Require().NoError(err)
	s.Len(linked, len(rows))
}

func (s *PushRepositorySuite) TestLinkProjectChunksManyBatches() {
	ctx := context.Background()
	repo, _, projectID, _ := s.newPushTestEnv()

	rows := confirmRows(2300, "link")
	s.Require().NoError(repo.InsertChunksIfNotExist(ctx, rows))
	hashes := make([]domain.Hash, 0, len(rows)+2)
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}
	s.Require().NoError(repo.LinkProjectChunk(ctx, projectID, hashes[5]))
	s.Require().NoError(repo.LinkProjectChunks(ctx, projectID, append(hashes, hashes[0], hashes[1])))

	var links int
	s.Require().NoError(s.db.QueryRow(`SELECT COUNT(*) FROM project_chunk_uploads WHERE project_id = $1`, projectID.Int64()).Scan(&links))
	s.Equal(len(rows), links)
}

func (s *PushRepositorySuite) TestKnownChunks() {
	ctx := context.Background()
	repo, _, projectID, _ := s.newPushTestEnv()

	rows := confirmRows(1200, "known")
	s.Require().NoError(repo.InsertChunksIfNotExist(ctx, rows[:700]))
	s.Require().NoError(repo.LinkProjectChunks(ctx, projectID, []domain.Hash{rows[0].Hash}))
	hashes := make([]domain.Hash, 0, len(rows))
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}

	known, err := repo.KnownChunks(ctx, hashes)
	s.Require().NoError(err)
	s.Len(known, 700)
	for _, row := range rows[:700] {
		s.Contains(known, row.Hash)
	}
	empty, err := repo.KnownChunks(ctx, nil)
	s.Require().NoError(err)
	s.Empty(empty)
}

func (s *PushRepositorySuite) TestConfirmWritesJoinTheContextTransaction() {
	ctx := context.Background()
	repo, _, projectID, _ := s.newPushTestEnv()
	rows := confirmRows(3, "tx")

	tx, err := s.db.BeginTx(ctx, nil)
	s.Require().NoError(err)
	txCtx := dbtx.WithTx(ctx, tx)
	s.Require().NoError(repo.InsertChunksIfNotExist(txCtx, rows))
	s.Require().NoError(repo.LinkProjectChunks(txCtx, projectID, []domain.Hash{rows[0].Hash}))
	known, err := repo.KnownChunks(txCtx, []domain.Hash{rows[0].Hash})
	s.Require().NoError(err)
	s.Len(known, 1)
	s.Require().NoError(tx.Rollback())

	s.Empty(s.chunkSizes())
	var links int
	s.Require().NoError(s.db.QueryRow(`SELECT COUNT(*) FROM project_chunk_uploads WHERE project_id = $1`, projectID.Int64()).Scan(&links))
	s.Zero(links)
}

func (s *PushRepositorySuite) TestConfirmWritesReportErrors() {
	ctx := context.Background()
	repo, _, projectID, _ := s.newPushTestEnv()
	_, err := s.db.Exec(`DROP TABLE project_chunk_uploads CASCADE`)
	s.Require().NoError(err)
	s.Error(repo.LinkProjectChunks(ctx, projectID, []domain.Hash{chunker.Sum([]byte("x"))}))
	_, err = s.db.Exec(`DROP TABLE chunks CASCADE`)
	s.Require().NoError(err)
	s.Error(repo.InsertChunksIfNotExist(ctx, confirmRows(1, "x")))
	_, err = repo.KnownChunks(ctx, []domain.Hash{chunker.Sum([]byte("x"))})
	s.Error(err)
}
