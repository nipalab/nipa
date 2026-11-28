package localrepo

import (
	"database/sql/driver"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func chunkSetRepo(t *testing.T) *LocalRepo {
	t.Helper()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	t.Cleanup(func() { _ = lr.Close() })
	shared := pushSpec{seed: 7, chunks: 3, perm: 0o644, encoding: "zstd"}
	require.NoError(t, lr.SaveSyncedTree(manifestTree(map[string]pushSpec{
		"a.txt":       shared,
		"dir/b.txt":   shared,
		"c.txt":       spec(9),
		"d/e/f.bin":   {seed: 11, chunks: 2, perm: 0o644, binary: true},
		"d/empty.txt": {seed: 13, chunks: 0, perm: 0o644},
	}), "c1", nil))
	return lr
}

// unionOfLookups is what FilesWithChunk answers for every hash, merged: the
// reference FilesWithAnyChunk must equal.
func unionOfLookups(t *testing.T, lr *LocalRepo, hashes []serverDomain.Hash) []domain.SnapshotFile {
	t.Helper()
	seen := map[string]bool{}
	var out []domain.SnapshotFile
	for _, h := range hashes {
		users, err := lr.FilesWithChunk(h)
		require.NoError(t, err)
		for _, f := range users {
			if !seen[f.Path] {
				seen[f.Path] = true
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func TestFilesWithAnyChunk_EqualsOneLookupPerHash(t *testing.T) {
	lr := chunkSetRepo(t)
	first, second := serverDomain.Hash{7, 1}, serverDomain.Hash{7, 2}
	var straddle serverDomain.Hash
	copy(straddle[:], append(append([]byte{}, first[16:]...), second[:16]...))
	sets := map[string][]serverDomain.Hash{
		"one shared chunk":     {second},
		"chunks of two files":  {{7, 0}, {9, 1}},
		"binary":               {{11, 1}},
		"same file twice":      {{7, 0}, {7, 2}, {7, 0}},
		"unknown":              {{0xee}},
		"straddling":           {straddle},
		"unknown and known":    {{0xee}, straddle, {11, 0}, {9, 0}},
		"every chunk and more": {{7, 0}, {7, 1}, {7, 2}, {9, 0}, {11, 0}, {11, 1}, {0xee}},
	}
	for name, hashes := range sets {
		got, err := lr.FilesWithAnyChunk(hashes)
		require.NoError(t, err)
		want := unionOfLookups(t, lr, hashes)
		if len(want) == 0 {
			require.Empty(t, got, name)
			continue
		}
		require.Equal(t, want, got, name)
	}
	got, err := lr.FilesWithAnyChunk([]serverDomain.Hash{{7, 1}})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "a.txt", got[0].Path)
	require.Equal(t, "dir/b.txt", got[1].Path)
	require.Len(t, got[0].Chunks, 3)

	none, err := lr.FilesWithAnyChunk(nil)
	require.NoError(t, err)
	require.Empty(t, none)

	// Every query drops the set it registered.
	count := 0
	chunkSets.Range(func(any, any) bool { count++; return true })
	require.Zero(t, count)
}

func TestFilesWithAnyChunk_NotInitialized(t *testing.T) {
	_, err := NewLocalRepo().FilesWithAnyChunk([]serverDomain.Hash{{1}})
	require.Error(t, err)
}

func TestListsAnyChunkSQL(t *testing.T) {
	h := serverDomain.Hash{3, 4}
	id := chunkSetIDs.Add(1)
	chunkSets.Store(id, map[serverDomain.Hash]struct{}{h: {}})
	defer chunkSets.Delete(id)

	call := func(args ...driver.Value) (driver.Value, error) { return listsAnyChunkSQL(nil, args) }
	got, err := call(append(make([]byte, 32), h[:]...), id)
	require.NoError(t, err)
	require.Equal(t, int64(1), got)
	got, err = call(string(h[:]), id)
	require.NoError(t, err)
	require.Equal(t, int64(1), got)
	got, err = call(make([]byte, 64), id)
	require.NoError(t, err)
	require.Equal(t, int64(0), got)
	got, err = call(nil, id)
	require.NoError(t, err)
	require.Equal(t, int64(0), got)
	got, err = call(h[:31], id)
	require.NoError(t, err)
	require.Equal(t, int64(0), got, "a partial hash never matches")

	_, err = call(h[:], id+1000)
	require.ErrorContains(t, err, "unknown chunk set", "a set that is not registered is an error, never a no")
	_, err = call(h[:], "1")
	require.ErrorContains(t, err, "integer")
	_, err = call(int64(5), id)
	require.ErrorContains(t, err, "blob")
}
