package diff

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// manifest builds a recursive tree manifest plus the chunk contents, the way
// the server stores them.
func manifest(t *testing.T, files map[string]string) (*serverDomain.TreeNode, map[serverDomain.Hash][]byte) {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	chunks := make(map[serverDomain.Hash][]byte)
	root := &serverDomain.TreeNode{}
	for _, name := range names {
		content := []byte(files[name])
		split, err := chunker.ChunkAll(content)
		require.NoError(t, err)
		hashes := make([]serverDomain.Hash, len(split))
		fileChunks := make([]serverDomain.Chunk, len(split))
		for i, c := range split {
			hashes[i] = c.Hash
			fileChunks[i] = serverDomain.Chunk{Hash: c.Hash, SizeBytes: int64(len(c.Data))}
			chunks[c.Hash] = c.Data
		}
		root.FileChildren = append(root.FileChildren, &serverDomain.File{
			Name:      name,
			Mode:      2,
			SizeBytes: int64(len(content)),
			IsBinary:  chunker.IsBinary(content),
			Hash:      chunker.FileHash(hashes),
			Chunks:    fileChunks,
		})
	}
	return root, chunks
}

func mergeChunks(maps ...map[serverDomain.Hash][]byte) map[serverDomain.Hash][]byte {
	out := make(map[serverDomain.Hash][]byte)
	for _, m := range maps {
		for h, data := range m {
			out[h] = data
		}
	}
	return out
}

// TestTreeDiff simulates a server-side merge-request diff: two tree manifests
// and a chunk store loader, no client involved.
func TestTreeDiff(t *testing.T) {
	const (
		oldMod = "one\ntwo\n"
		newMod = "one\nTWO\n"
		same   = "same\n"
		binOld = "old\x00binary"
		binNew = "new\x00binary"
	)

	base, baseChunks := manifest(t, map[string]string{
		"mod.txt":  oldMod,
		"old.txt":  same,
		"img.bin":  binOld,
		"gone.txt": "bye\n",
	})
	head, headChunks := manifest(t, map[string]string{
		"mod.txt":   newMod,
		"new.txt":   same,
		"img.bin":   binNew,
		"added.txt": "hello\n",
	})
	chunks := mergeChunks(baseChunks, headChunks)

	loads := make(map[serverDomain.Hash]int)
	loadChunk := func(h serverDomain.Hash) ([]byte, error) {
		loads[h]++
		return chunks[h], nil
	}

	files := TreeDiff(base, head, loadChunk)
	byPath := make(map[string]FileDiff, len(files))
	for _, f := range files {
		byPath[f.Change.Path] = f
	}
	require.Len(t, files, 5)

	mod := byPath["mod.txt"]
	require.Equal(t, Modified, mod.Change.Status)
	require.Equal(t, []byte(oldMod), mod.Old)
	require.Equal(t, []byte(newMod), mod.New)

	ren := byPath["new.txt"]
	require.Equal(t, Renamed, ren.Change.Status)
	require.Equal(t, "old.txt", ren.Change.Old.Path)
	require.Equal(t, 100, ren.Change.Similarity)
	require.Equal(t, []byte(same), ren.Old)
	require.Equal(t, []byte(same), ren.New)

	img := byPath["img.bin"]
	require.True(t, img.Change.New.IsBinary)
	require.Nil(t, img.Old, "binary content must not be loaded")
	require.Nil(t, img.New)
	for _, c := range head.FileChildren {
		if c.Name != "img.bin" {
			continue
		}
		for _, chunk := range c.Chunks {
			require.Zero(t, loads[chunk.Hash], "binary chunks must never be fetched")
		}
	}

	require.Equal(t, Added, byPath["added.txt"].Change.Status)
	require.Equal(t, Deleted, byPath["gone.txt"].Change.Status)

	patch := Patch(files, Options{Context: 3})
	require.Contains(t, patch, "-two")
	require.Contains(t, patch, "+TWO")
	require.Contains(t, patch, "rename from old.txt")
	require.Contains(t, patch, "Binary files a/img.bin and b/img.bin differ")

	require.Contains(t, NameStatus(files), "R100\told.txt\tnew.txt")
	require.NotEmpty(t, Stat(files))
}

func TestTreeDiff_NilLoader(t *testing.T) {
	base, _ := manifest(t, map[string]string{"a.txt": "a\n"})
	head, _ := manifest(t, map[string]string{"a.txt": "b\n"})

	files := TreeDiff(base, head, nil)
	require.Len(t, files, 1)
	require.True(t, files[0].OldUnavailable)
	require.True(t, files[0].NewUnavailable)
	require.Equal(t, []string{"diff --nipa a/a.txt b/a.txt", contentUnavailable}, FilePatch(files[0], Options{Context: 3}))
}

func TestAttachContents_Unavailable(t *testing.T) {
	changes := []Change{{Path: "a.txt", Status: Added, New: Entry{Path: "a.txt"}}}
	files := AttachContents(changes, func(Entry, bool) ([]byte, bool) { return nil, false })
	require.Len(t, files, 1)
	require.True(t, files[0].NewUnavailable)
	require.False(t, files[0].OldUnavailable)
}
