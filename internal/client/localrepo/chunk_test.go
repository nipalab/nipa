package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestStoreChunk_LoadChunk(t *testing.T) {
	lr := newTestLocalRepo(t)

	data := []byte("content cached on disk")
	hash := chunker.Sum(data)
	require.NoError(t, lr.StoreChunk(hash, data))

	got, err := lr.LoadChunk(hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestStoreChunk_Overwrites(t *testing.T) {
	lr := newTestLocalRepo(t)

	hash := chunker.Sum([]byte("first"))
	require.NoError(t, lr.StoreChunk(hash, []byte("first")))
	require.NoError(t, lr.StoreChunk(hash, []byte("second")))

	got, err := lr.LoadChunk(hash)
	require.NoError(t, err)
	require.Equal(t, []byte("second"), got)
}

func TestStoreChunk_SaveTreeKeepsContent(t *testing.T) {
	lr := newTestLocalRepo(t)

	data := []byte("kept across metadata-only upserts")
	hash := chunker.Sum(data)
	require.NoError(t, lr.StoreChunk(hash, data))

	root := &serverDomain.TreeNode{
		Hash: serverDomain.Hash{0x01},
		Name: "root",
		FileChildren: []*serverDomain.File{
			{
				Name:      "a.txt",
				Mode:      0o644,
				SizeBytes: int64(len(data)),
				Hash:      hash,
				Chunks:    []serverDomain.Chunk{{Hash: hash, SizeBytes: int64(len(data))}},
			},
		},
	}
	require.NoError(t, lr.SaveTree(root), "metadata-only upsert must not clear cached content")

	got, err := lr.LoadChunk(hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestLoadChunk_NotFound(t *testing.T) {
	lr := newTestLocalRepo(t)
	_, err := lr.LoadChunk(chunker.Sum([]byte("absent")))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestStoreChunk_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	require.Error(t, lr.StoreChunk(serverDomain.Hash{0x01}, []byte("x")))
	_, err := lr.LoadChunk(serverDomain.Hash{0x01})
	require.Error(t, err)
}
