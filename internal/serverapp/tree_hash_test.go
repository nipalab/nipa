package serverapp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/db/dbtest"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/treehash"
)

func TestBackfillTreeHashes(t *testing.T) {
	ctx := context.Background()
	conn := dbtest.SQLite(t)
	repo := sqlite.NewTreeHashRepository(conn)

	// Nothing stored yet: the pass has nothing to cover.
	require.NoError(t, BackfillTreeHashes(ctx, repo))

	tool := treehash.FileEntry{Name: "tool.sh", Hash: domain.Hash{0x01}, Mode: 0o755}
	stale := treehash.TreeHash([]treehash.FileEntry{{Name: tool.Name, Hash: tool.Hash, Mode: 0o644}}, nil)
	res, err := conn.ExecContext(ctx, "INSERT INTO tree_nodes (hash, name, mode) VALUES (?, 'root', 292)", stale.Bytes())
	require.NoError(t, err)
	rootID, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, "INSERT INTO files (name, mode, tree_id, hash, size_bytes) VALUES ('tool.sh', 493, ?, ?, 0)", rootID, tool.Hash.Bytes())
	require.NoError(t, err)

	require.NoError(t, BackfillTreeHashes(ctx, repo))
	var hash []byte
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT hash FROM tree_nodes WHERE id = ?", rootID).Scan(&hash))
	require.Equal(t, treehash.TreeHash([]treehash.FileEntry{tool}, nil).Bytes(), hash)
	legacy, err := repo.LegacyTreeHash(ctx, rootID)
	require.NoError(t, err)
	require.Equal(t, stale, *legacy)

	require.NoError(t, conn.Close())
	require.ErrorContains(t, BackfillTreeHashes(ctx, repo), "tree hash backfill")
}
