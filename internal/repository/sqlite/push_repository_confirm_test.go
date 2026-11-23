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
	"github.com/nipalab/nipa/internal/snow"
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

func chunkSizes(t *testing.T, db *sql.DB, projectID snow.ID) map[domain.Hash]int64 {
	t.Helper()
	rows, err := db.Query(`SELECT hash, size_bytes FROM chunks WHERE project_id = ?`, projectID.Int64())
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
	repo, _, db, projectID, _ := newPushTestEnv(t)

	require.NoError(t, repo.InsertChunksIfNotExist(ctx, projectID, nil), "an empty batch is a no-op")

	existing, existingSize, _ := chunkRow(t, "already recorded")
	require.NoError(t, repo.InsertChunksIfNotExist(ctx, projectID, []usecase.ChunkRef{{Hash: existing, SizeBytes: existingSize}}))

	rows := confirmRows(2500, "confirm")
	batch := append([]usecase.ChunkRef{}, rows...)
	batch = append(batch, rows[0], rows[1])                                               // duplicates within the call
	batch = append(batch, usecase.ChunkRef{Hash: existing, SizeBytes: existingSize + 99}) // an existing row keeps its size
	require.NoError(t, repo.InsertChunksIfNotExist(ctx, projectID, batch))
	require.NoError(t, repo.InsertChunksIfNotExist(ctx, projectID, rows[:10]), "inserting again is a no-op")

	got := chunkSizes(t, db, projectID)
	require.Len(t, got, len(rows)+1)
	require.Equal(t, existingSize, got[existing])
	for _, row := range rows {
		require.Equal(t, row.SizeBytes, got[row.Hash])
	}

	var markers int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM project_chunk_uploads WHERE project_id = ?`, projectID.Int64()).Scan(&markers))
	require.Equal(t, len(rows)+1, markers, "one upload marker per row")
}

func TestPushRepositorySQLite_ConfirmWritesJoinTheContextTransaction(t *testing.T) {
	ctx := context.Background()
	repo, _, db, projectID, _ := newPushTestEnv(t)
	rows := confirmRows(3, "tx")

	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	txCtx := dbtx.WithTx(ctx, tx)
	require.NoError(t, repo.InsertChunksIfNotExist(txCtx, projectID, rows))
	sizes, err := repo.ProjectChunkSizes(txCtx, projectID, []domain.Hash{rows[0].Hash})
	require.NoError(t, err)
	require.Len(t, sizes, 1, "the transaction sees its own rows")
	require.NoError(t, tx.Rollback())

	require.Empty(t, chunkSizes(t, db, projectID), "rolled back with the caller's transaction")
	sizes, err = repo.ProjectChunkSizes(ctx, projectID, []domain.Hash{rows[0].Hash})
	require.NoError(t, err)
	require.Empty(t, sizes)
	var markers int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM project_chunk_uploads`).Scan(&markers))
	require.Zero(t, markers)
}

func TestPushRepositorySQLite_ConfirmWritesReportErrors(t *testing.T) {
	ctx := context.Background()
	repo, _, db, projectID, _ := newPushTestEnv(t)
	require.NoError(t, db.Close())

	require.Error(t, repo.InsertChunksIfNotExist(ctx, projectID, confirmRows(1, "closed")))
	_, err := repo.ProjectChunkSizes(ctx, projectID, []domain.Hash{chunker.Sum([]byte("closed"))})
	require.Error(t, err)
}
