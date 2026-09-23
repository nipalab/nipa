package diff

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestCompare(t *testing.T) {
	same := Entry{Path: "same.txt", Hash: chunker.Sum([]byte("same"))}
	oldFile := Entry{Path: "mod.txt", Hash: chunker.Sum([]byte("old"))}
	newFile := Entry{Path: "mod.txt", Hash: chunker.Sum([]byte("new"))}
	modeOld := Entry{Path: "mode.txt", Hash: chunker.Sum([]byte("m")), Mode: 2}
	modeNew := Entry{Path: "mode.txt", Hash: chunker.Sum([]byte("m")), Mode: 3}
	gone := Entry{Path: "gone.txt", Hash: chunker.Sum([]byte("gone"))}
	fresh := Entry{Path: "fresh.txt", Hash: chunker.Sum([]byte("fresh"))}

	changes := Compare(
		map[string]Entry{"same.txt": same, "mod.txt": oldFile, "mode.txt": modeOld, "gone.txt": gone},
		map[string]Entry{"same.txt": same, "mod.txt": newFile, "mode.txt": modeNew, "fresh.txt": fresh},
	)
	require.Equal(t, []Change{
		{Path: "fresh.txt", Status: Added, New: fresh},
		{Path: "gone.txt", Status: Deleted, Old: gone},
		{Path: "mod.txt", Status: Modified, Old: oldFile, New: newFile},
		{Path: "mode.txt", Status: Modified, Old: modeOld, New: modeNew},
	}, changes)
}

func TestCompare_NoChanges(t *testing.T) {
	entry := Entry{Path: "same.txt", Hash: chunker.Sum([]byte("same"))}
	require.Empty(t, Compare(map[string]Entry{"same.txt": entry}, map[string]Entry{"same.txt": entry}))
}

func TestFromTree(t *testing.T) {
	rootChunk := chunker.Sum([]byte("root"))
	nestedChunk := chunker.Sum([]byte("nested"))
	root := &serverDomain.TreeNode{
		FileChildren: []*serverDomain.File{{
			Name:      "root.txt",
			Mode:      2,
			SizeBytes: 4,
			Chunks:    []serverDomain.Chunk{{Hash: rootChunk, SizeBytes: 4}},
		}},
		TreeChildren: []*serverDomain.TreeNode{{
			Name: "dir",
			FileChildren: []*serverDomain.File{{
				Name:      "nested.bin",
				Mode:      3,
				SizeBytes: 6,
				IsBinary:  true,
				Chunks:    []serverDomain.Chunk{{Hash: nestedChunk, SizeBytes: 6}},
			}},
		}},
	}

	entries := FromTree(root)
	require.Len(t, entries, 2)

	got := entries["root.txt"]
	require.Equal(t, 2, got.Mode)
	require.Equal(t, int64(4), got.SizeBytes)
	require.Equal(t, []serverDomain.Hash{rootChunk}, got.ChunkHashes)
	require.Equal(t, []int64{4}, got.ChunkSizes)
	require.Equal(t, chunker.FileHash([]serverDomain.Hash{rootChunk}), got.Hash)

	nested := entries["dir/nested.bin"]
	require.True(t, nested.IsBinary)
	require.Equal(t, 3, nested.Mode)
	require.Equal(t, chunker.FileHash([]serverDomain.Hash{nestedChunk}), nested.Hash)
}

func TestFromTree_Nil(t *testing.T) {
	require.Empty(t, FromTree(nil))
}

func TestLoadContent(t *testing.T) {
	a := chunker.Sum([]byte("aa"))
	b := chunker.Sum([]byte("bb"))
	load := func(h serverDomain.Hash) ([]byte, error) {
		if h == a {
			return []byte("aa"), nil
		}
		return []byte("bb"), nil
	}
	content, err := LoadContent(load, Entry{Path: "f", ChunkHashes: []serverDomain.Hash{a, b}})
	require.NoError(t, err)
	require.Equal(t, "aabb", string(content))
}

func TestLoadContent_DecodesZstd(t *testing.T) {
	data := []byte("compressed text payload")
	encoding, chunks, err := chunker.EncodeBytes(data, "a.txt", false, "")
	require.NoError(t, err)
	require.Equal(t, chunker.EncodingZstd, encoding)

	load := func(h serverDomain.Hash) ([]byte, error) {
		for _, c := range chunks {
			if c.Hash == h {
				return c.Data, nil
			}
		}
		return nil, assertErr{}
	}
	content, err := LoadContent(load, Entry{Path: "a.txt", Encoding: encoding, ChunkHashes: []serverDomain.Hash{chunks[0].Hash}})
	require.NoError(t, err)
	require.Equal(t, data, content)
}

func TestLoadContent_MissingChunk(t *testing.T) {
	load := func(serverDomain.Hash) ([]byte, error) {
		return nil, assertErr{}
	}
	_, err := LoadContent(load, Entry{Path: "f", ChunkHashes: []serverDomain.Hash{chunker.Sum([]byte("x"))}})
	require.Error(t, err)
}

type assertErr struct{}

func (assertErr) Error() string { return "missing" }
