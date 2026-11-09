package diff

import (
	"fmt"
	"strings"
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

func TestDetectRenames_LoadsEachFileOnce(t *testing.T) {
	contents := map[string]string{}
	var changes []Change
	for i := 0; i < 4; i++ {
		oldPath, newPath := fmt.Sprintf("old%d.txt", i), fmt.Sprintf("new%d.txt", i)
		body := strings.Repeat(fmt.Sprintf("line of file %d\n", i), 10)
		contents[oldPath] = body + "old tail\n"
		contents[newPath] = body + "new tail\n"
		changes = append(changes,
			Change{Path: oldPath, Status: Deleted, Old: entryOf(oldPath, contents[oldPath])},
			Change{Path: newPath, Status: Added, New: entryOf(newPath, contents[newPath])})
	}
	loads := map[string]int{}
	load := func(e Entry) ([]byte, bool) {
		loads[e.Path]++
		return []byte(contents[e.Path]), true
	}

	got := DetectRenames(changes, load)
	require.Len(t, got, 4)
	for _, c := range got {
		require.Equal(t, Renamed, c.Status)
		require.Equal(t, strings.TrimPrefix(c.Path, "new"), strings.TrimPrefix(c.Old.Path, "old"))
	}
	for path, n := range loads {
		require.Equal(t, 1, n, path)
	}
}

func TestDetectRenames_SkipsPairsThatCannotReachThreshold(t *testing.T) {
	short, long := "one\n", strings.Repeat("one\n", 10)
	changes := []Change{
		{Path: "old.txt", Status: Deleted, Old: entryOf("old.txt", short)},
		{Path: "new.txt", Status: Added, New: entryOf("new.txt", long)},
	}
	got := DetectRenames(changes, contentLoader(map[string]string{"old.txt": short, "new.txt": long}))
	require.Len(t, got, 2)
}

func TestDetectRenames_OverLimitKeepsExactRenamesOnly(t *testing.T) {
	side := 230
	require.Greater(t, side*side, maxRenameComparisons)
	var changes []Change
	for i := 0; i < side; i++ {
		changes = append(changes,
			Change{Path: fmt.Sprintf("a/%d.txt", i), Status: Deleted, Old: entryOf(fmt.Sprintf("a/%d.txt", i), fmt.Sprintf("old %d\nshared\n", i))},
			Change{Path: fmt.Sprintf("b/%d.txt", i), Status: Added, New: entryOf(fmt.Sprintf("b/%d.txt", i), fmt.Sprintf("new %d\nshared\n", i))})
	}
	moved := entryOf("moved.txt", "exact\n")
	changes = append(changes,
		Change{Path: "src/moved.txt", Status: Deleted, Old: Entry{Path: "src/moved.txt", Hash: moved.Hash, ChunkHashes: moved.ChunkHashes}},
		Change{Path: "dst/moved.txt", Status: Added, New: Entry{Path: "dst/moved.txt", Hash: moved.Hash, ChunkHashes: moved.ChunkHashes}})
	loaded := false
	got := DetectRenames(changes, func(Entry) ([]byte, bool) { loaded = true; return nil, false })

	require.False(t, loaded)
	renamed := 0
	for _, c := range got {
		if c.Status == Renamed {
			renamed++
			require.Equal(t, "dst/moved.txt", c.Path)
		}
	}
	require.Equal(t, 1, renamed)
}

func TestCanReach(t *testing.T) {
	require.True(t, canReach(5, 10, 50))
	require.False(t, canReach(4, 10, 50))
	require.False(t, canReach(0, 0, 50))
	require.True(t, canReach(0, 0, 0))
}

func TestDetectRenames_IdenticalFilesPairInPathOrderWithoutCrossProduct(t *testing.T) {
	const n = 3000
	empty := entryOf("x", "")
	var changes []Change
	for i := 0; i < n; i++ {
		oldPath, newPath := fmt.Sprintf("old/%05d.keep", i), fmt.Sprintf("new/%05d.keep", n-1-i)
		changes = append(changes,
			Change{Path: oldPath, Status: Deleted, Old: Entry{Path: oldPath, Hash: empty.Hash}},
			Change{Path: newPath, Status: Added, New: Entry{Path: newPath, Hash: empty.Hash}})
	}
	changes = append(changes, Change{Path: "new/extra.keep", Status: Added, New: Entry{Path: "new/extra.keep", Hash: empty.Hash}})

	require.Len(t, exactRenamePairs(changes, indexesOf(changes, Deleted), indexesOf(changes, Added)), n)

	got := DetectRenames(changes, nil)
	renamed := 0
	for _, c := range got {
		if c.Status == Renamed {
			renamed++
			require.Equal(t, strings.TrimPrefix(c.Old.Path, "old/"), strings.TrimPrefix(c.Path, "new/"))
		}
	}
	require.Equal(t, n, renamed)
	require.Equal(t, Added, got[len(got)-1].Status)
	require.Equal(t, "new/extra.keep", got[len(got)-1].Path)
}

func indexesOf(changes []Change, status Status) []int {
	var out []int
	for i, c := range changes {
		if c.Status == status {
			out = append(out, i)
		}
	}
	return out
}
