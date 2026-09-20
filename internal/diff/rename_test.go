package diff

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func entryOf(path, content string) Entry {
	chunks, _ := chunker.ChunkAll([]byte(content))
	hashes := make([]serverDomain.Hash, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
	}
	return Entry{
		Path:        path,
		Mode:        2,
		SizeBytes:   int64(len(content)),
		IsBinary:    chunker.IsBinary([]byte(content)),
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
	}
}

func contentLoader(contents map[string]string) func(Entry) ([]byte, bool) {
	return func(e Entry) ([]byte, bool) {
		content, ok := contents[e.Path]
		if !ok {
			return nil, false
		}
		return []byte(content), true
	}
}

func TestDetectRenames_ExactHash(t *testing.T) {
	old := entryOf("old.txt", "same content\n")
	newEntry := entryOf("new.txt", "same content\n")
	changes := []Change{
		{Path: "new.txt", Status: Added, New: newEntry},
		{Path: "old.txt", Status: Deleted, Old: old},
	}

	got := DetectRenames(changes, nil)
	require.Len(t, got, 1)
	require.Equal(t, Renamed, got[0].Status)
	require.Equal(t, "old.txt", got[0].Old.Path)
	require.Equal(t, "new.txt", got[0].Path)
	require.Equal(t, 100, got[0].Similarity)
}

func TestDetectRenames_Similarity(t *testing.T) {
	old := entryOf("old.txt", "one\ntwo\nthree\nfour\n")
	newEntry := entryOf("new.txt", "one\ntwo\nthree\n")
	changes := []Change{
		{Path: "old.txt", Status: Deleted, Old: old},
		{Path: "new.txt", Status: Added, New: newEntry},
	}
	load := contentLoader(map[string]string{
		"old.txt": "one\ntwo\nthree\nfour\n",
		"new.txt": "one\ntwo\nthree\n",
	})

	got := DetectRenames(changes, load)
	require.Len(t, got, 1)
	require.Equal(t, Renamed, got[0].Status)
	require.Equal(t, 75, got[0].Similarity)
}

func TestDetectRenames_BelowThreshold(t *testing.T) {
	old := entryOf("old.txt", "one\ntwo\nthree\nfour\n")
	newEntry := entryOf("new.txt", "one\n")
	changes := []Change{
		{Path: "old.txt", Status: Deleted, Old: old},
		{Path: "new.txt", Status: Added, New: newEntry},
	}
	load := contentLoader(map[string]string{
		"old.txt": "one\ntwo\nthree\nfour\n",
		"new.txt": "one\n",
	})

	got := DetectRenames(changes, load)
	require.Len(t, got, 2)
	require.Equal(t, Added, got[0].Status)
	require.Equal(t, Deleted, got[1].Status)
}

func TestDetectRenames_ChunkOverlapForBinary(t *testing.T) {
	old := entryOf("old.bin", "binary\x00data")
	newEntry := entryOf("new.bin", "binary\x00data")
	changes := []Change{
		{Path: "old.bin", Status: Deleted, Old: old},
		{Path: "new.bin", Status: Added, New: newEntry},
	}

	got := DetectRenames(changes, nil)
	require.Len(t, got, 1)
	require.Equal(t, Renamed, got[0].Status)
}
