package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
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

func chunkSizes(t *testing.T, db *sql.DB) map[domain.Hash]int64 {
	t.Helper()
	rows, err := db.Query(`SELECT hash, size_bytes FROM chunks`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[domain.Hash]int64{}
	for rows.Next() {
		var raw []byte
		var size int64
		require.NoError(t, rows.Scan(&raw, &size))
		hash, err := domain.HashFromBytes(raw)
		require.NoError(t, err)
		out[hash] = size
	}
	require.NoError(t, rows.Err())
	return out
}

func TestPushRepositorySQLite_InsertChunksIfNotExist(t *testing.T) {
	ctx := context.Background()
	repo, _, db, _, _ := newPushTestEnv(t)

	require.NoError(t, repo.InsertChunksIfNotExist(ctx, nil), "an empty batch is a no-op")

	existing, existingSize, _ := chunkRow(t, "already recorded")
	require.NoError(t, repo.InsertChunkIfNotExists(ctx, existing, existingSize))

	rows := confirmRows(2500, "confirm")
	batch := append([]usecase.ChunkRef{}, rows...)
	batch = append(batch, rows[0], rows[1])                                               // duplicates within the call
	batch = append(batch, usecase.ChunkRef{Hash: existing, SizeBytes: existingSize + 99}) // an existing row keeps its size
	require.NoError(t, repo.InsertChunksIfNotExist(ctx, batch))
	require.NoError(t, repo.InsertChunksIfNotExist(ctx, rows[:10]), "inserting again is a no-op")

	got := chunkSizes(t, db)
	require.Len(t, got, len(rows)+1)
	require.Equal(t, existingSize, got[existing])
	for _, row := range rows {
		require.Equal(t, row.SizeBytes, got[row.Hash])
	}
}

func TestPushRepositorySQLite_LinkProjectChunksManyBatches(t *testing.T) {
	ctx := context.Background()
	repo, _, db, projectID, _ := newPushTestEnv(t)

	rows := confirmRows(2300, "link")
	require.NoError(t, repo.InsertChunksIfNotExist(ctx, rows))
	hashes := make([]domain.Hash, 0, len(rows)+2)
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}
	require.NoError(t, repo.LinkProjectChunk(ctx, projectID, hashes[5]))
	hashes = append(hashes, hashes[0], hashes[1])
	require.NoError(t, repo.LinkProjectChunks(ctx, projectID, hashes), "duplicates and existing links are skipped")

	var links int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM project_chunk_uploads WHERE project_id = ?`, projectID.Int64()).Scan(&links))
	require.Equal(t, len(rows), links)

	sizes, err := repo.LinkedProjectChunkSizes(ctx, projectID, hashes[:len(rows)])
	require.NoError(t, err)
	require.Len(t, sizes, len(rows))
}

func TestPushRepositorySQLite_KnownChunks(t *testing.T) {
	ctx := context.Background()
	repo, _, _, projectID, _ := newPushTestEnv(t)

	rows := confirmRows(1200, "known")
	require.NoError(t, repo.InsertChunksIfNotExist(ctx, rows[:700]))
	require.NoError(t, repo.LinkProjectChunks(ctx, projectID, []domain.Hash{rows[0].Hash}))

	hashes := make([]domain.Hash, 0, len(rows))
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}
	known, err := repo.KnownChunks(ctx, hashes)
	require.NoError(t, err)
	require.Len(t, known, 700, "rows count whatever project they are linked to")
	for _, row := range rows[:700] {
		require.Contains(t, known, row.Hash)
	}

	empty, err := repo.KnownChunks(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, empty)
}

func TestPushRepositorySQLite_ConfirmWritesJoinTheContextTransaction(t *testing.T) {
	ctx := context.Background()
	repo, _, db, projectID, _ := newPushTestEnv(t)
	rows := confirmRows(3, "tx")

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	txCtx := dbtx.WithTx(ctx, tx)
	require.NoError(t, repo.InsertChunksIfNotExist(txCtx, rows))
	require.NoError(t, repo.LinkProjectChunks(txCtx, projectID, []domain.Hash{rows[0].Hash}))
	known, err := repo.KnownChunks(txCtx, []domain.Hash{rows[0].Hash})
	require.NoError(t, err)
	require.Len(t, known, 1, "the transaction sees its own rows")
	require.NoError(t, tx.Rollback())

	require.Empty(t, chunkSizes(t, db), "rolled back with the caller's transaction")
	links, err := sqlcSqlite.New(db).ProjectChunkLinkedSizes(ctx, sqlcSqlite.ProjectChunkLinkedSizesParams{
		ProjectID: projectID.Int64(),
		Hashes:    [][]byte{rows[0].Hash.Bytes()},
	})
	require.NoError(t, err)
	require.Empty(t, links)
}

func TestPushRepositorySQLite_ConfirmWritesReportErrors(t *testing.T) {
	ctx := context.Background()
	repo, _, db, projectID, _ := newPushTestEnv(t)
	require.NoError(t, db.Close())

	require.Error(t, repo.InsertChunksIfNotExist(ctx, confirmRows(1, "closed")))
	require.Error(t, repo.LinkProjectChunks(ctx, projectID, []domain.Hash{chunker.Sum([]byte("closed"))}))
	_, err := repo.KnownChunks(ctx, []domain.Hash{chunker.Sum([]byte("closed"))})
	require.Error(t, err)
}
