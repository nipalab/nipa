package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestStageAdd_ListStaged(t *testing.T) {
	lr := newTestLocalRepo(t)

	require.NoError(t, lr.StageAdd("assets/logo.png"))
	require.NoError(t, lr.StageAdd("readme.md"))

	got, err := lr.ListStaged()
	require.NoError(t, err)
	require.Equal(t, []string{"assets/logo.png", "readme.md"}, got)
}

func TestStageAdd_Idempotent(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.StageAdd("a.txt"))
	require.NoError(t, lr.StageAdd("a.txt"))

	got, err := lr.ListStaged()
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, got)
}

func TestStageRemove(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.StageAdd("a.txt"))
	require.NoError(t, lr.StageAdd("b.txt"))

	require.NoError(t, lr.StageRemove([]string{"a.txt", "nope.txt"}))
	got, err := lr.ListStaged()
	require.NoError(t, err)
	require.Equal(t, []string{"b.txt"}, got)
}

func TestStageAdd_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	require.Error(t, lr.StageAdd("a.txt"))
	require.Error(t, lr.StageRemove([]string{"a.txt"}))
	_, err := lr.ListStaged()
	require.Error(t, err)
	_, err = lr.Snapshot()
	require.Error(t, err)
}

func TestSnapshot_AfterSaveTree(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(treeFixture()))

	snap, err := lr.Snapshot()
	require.NoError(t, err)
	require.Equal(t, treeFixture().Hash.String(), snap.TreeHash)
	require.Len(t, snap.Files, 1)

	f := snap.Files[0]
	require.Equal(t, "assets/logo.png", f.Path, "snapshot paths must not carry a leading slash")
	require.Equal(t, chunker.FileHash([]serverDomain.Hash{{0x04}}), f.Hash)
	require.Len(t, f.Chunks, 1)
	require.Equal(t, serverDomain.Hash{0x04}, f.Chunks[0].Hash)
	require.Equal(t, int64(10), f.Chunks[0].SizeBytes)
	require.Equal(t, int64(10), f.SizeBytes)
	require.True(t, f.IsBinary)
	require.Equal(t, 0o644, f.Mode)
}

func TestSnapshot_NewRepo(t *testing.T) {
	lr := newTestLocalRepo(t)

	snap, err := lr.Snapshot()
	require.NoError(t, err)
	require.Equal(t, "", snap.TreeHash)
	require.Empty(t, snap.Files)
}

func TestSnapshot_EmptyTree(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(nil))

	snap, err := lr.Snapshot()
	require.NoError(t, err)
	require.Equal(t, "", snap.TreeHash)
	require.Empty(t, snap.Files)
}

func TestSnapshot_MultipleFilesWithChunks(t *testing.T) {
	lr := newTestLocalRepo(t)
	root := &serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x01},
		Name: "root",
		FileChildren: []*serverDomain.File{
			{
				Hash:      serverDomain.Hash{0x02},
				Name:      "root.txt",
				SizeBytes: 5,
				Chunks:    []serverDomain.Chunk{{Hash: serverDomain.Hash{0x03}, SizeBytes: 5}},
			},
			{
				Hash:      serverDomain.Hash{0x04},
				Name:      "zero.txt",
				SizeBytes: 0,
			},
		},
	}
	require.NoError(t, lr.SaveTree(root))

	snap, err := lr.Snapshot()
	require.NoError(t, err)
	require.Len(t, snap.Files, 2)
	require.Equal(t, "root.txt", snap.Files[0].Path)
	require.Equal(t, "zero.txt", snap.Files[1].Path)
	require.Equal(t, chunker.FileHash([]serverDomain.Hash{{0x03}}), snap.Files[0].Hash)
	require.Equal(t, chunker.FileHash(nil), snap.Files[1].Hash, "a zero-length file has no chunks")
}

func TestClearStaged(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.StageAdd("a.txt"))
	require.NoError(t, lr.StageAdd("b.txt"))

	require.NoError(t, lr.ClearStaged())
	got, err := lr.ListStaged()
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestClearStaged_Idempotent(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.ClearStaged())
	require.NoError(t, lr.ClearStaged())
}

func TestClearStaged_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	require.Error(t, lr.ClearStaged())
}

func TestMissingChunks(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(treeFixture()))

	cached := serverDomain.Hash{0x04}
	missingA := serverDomain.Hash{0xa1}
	missingB := serverDomain.Hash{0xa2}

	got, err := lr.MissingChunks([]serverDomain.Hash{cached, missingA, missingB, cached, missingA})
	require.NoError(t, err)
	require.Equal(t, []serverDomain.Hash{missingA, missingB}, got, "known chunks are skipped, input is deduped")
}

func TestMissingChunks_AllKnown(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(treeFixture()))

	got, err := lr.MissingChunks([]serverDomain.Hash{{0x04}})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestMissingChunks_Empty(t *testing.T) {
	lr := newTestLocalRepo(t)
	got, err := lr.MissingChunks(nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestMissingChunks_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	_, err := lr.MissingChunks([]serverDomain.Hash{{0x01}})
	require.Error(t, err)
}

func newTestLocalRepo(t *testing.T) *LocalRepo {
	t.Helper()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	t.Cleanup(func() { _ = lr.Close() })
	return lr
}
