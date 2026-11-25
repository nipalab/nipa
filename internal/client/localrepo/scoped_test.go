package localrepo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// scopedRepo is a repository tracking the given paths; file i gets spec(i+1).
func scopedRepo(t *testing.T, paths ...string) *LocalRepo {
	t.Helper()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	t.Cleanup(func() { _ = lr.Close() })
	files := make(map[string]pushSpec, len(paths))
	for i, p := range paths {
		files[p] = spec(byte(i + 1))
	}
	require.NoError(t, lr.SaveSyncedTree(manifestTree(files), "c1", nil))
	return lr
}

func snapshotByPath(t *testing.T, lr *LocalRepo) map[string]domain.SnapshotFile {
	t.Helper()
	snapshot, err := lr.Snapshot()
	require.NoError(t, err)
	out := make(map[string]domain.SnapshotFile, len(snapshot.Files))
	for _, f := range snapshot.Files {
		out[f.Path] = f
	}
	return out
}

func TestSnapshotFiles_ReturnsTheRowsSnapshotHolds(t *testing.T) {
	lr := scopedRepo(t, "a.txt", "src/b.txt", "src/deep/c.txt")
	all := snapshotByPath(t, lr)

	got, err := lr.SnapshotFiles([]string{"src/b.txt", "missing.txt", "", "a.txt", "src", "src/b.txt"})
	require.NoError(t, err)
	require.Equal(t, map[string]domain.SnapshotFile{"a.txt": all["a.txt"], "src/b.txt": all["src/b.txt"]}, got)
	require.NotEmpty(t, got["a.txt"].Chunks)

	for _, empty := range [][]string{nil, {}, {""}} {
		got, err = lr.SnapshotFiles(empty)
		require.NoError(t, err)
		require.Empty(t, got)
	}
}

func TestSnapshotFiles_MorePathsThanOneBatch(t *testing.T) {
	var paths []string
	for i := 0; i < 2*pathBatch+7; i++ {
		paths = append(paths, fmt.Sprintf("d%02d/f%04d.txt", i%13, i))
	}
	lr := scopedRepo(t, paths...)
	all := snapshotByPath(t, lr)

	asked := append([]string{"nope/x.txt"}, paths...)
	asked = append(asked, "nope/y.txt")
	got, err := lr.SnapshotFiles(asked)
	require.NoError(t, err)
	require.Equal(t, all, got)
}

func TestSnapshotFilesUnder_IsThePrefixOfTheSnapshot(t *testing.T) {
	lr := scopedRepo(t,
		"a/x.txt", "a/sub/y.txt", "ab/z.txt", "a.txt", "a0/w.txt", "a-b/v.txt",
		"a%/p.txt", "a_/q.txt", "axx/r.txt", "a%b/s.txt", "top.txt", "ünï/c.txt", "ünïx/d.txt",
	)
	snapshot, err := lr.Snapshot()
	require.NoError(t, err)

	for _, dir := range []string{"", "a", "ab", "a/sub", "a%", "a_", "a.txt", "missing", "ünï", "a/su"} {
		var want []domain.SnapshotFile
		for _, f := range snapshot.Files {
			if dir == "" || strings.HasPrefix(f.Path, dir+"/") {
				want = append(want, f)
			}
		}
		got, err := lr.SnapshotFilesUnder(dir)
		require.NoError(t, err)
		require.ElementsMatch(t, want, got, "dir %q", dir)
		require.Len(t, got, len(want), "dir %q", dir)

		holds, err := lr.TracksUnder(dir)
		require.NoError(t, err)
		require.Equal(t, len(want) > 0, holds, "dir %q", dir)
	}

	under, err := lr.SnapshotFilesUnder("a")
	require.NoError(t, err)
	require.Equal(t, []string{"a/sub/y.txt", "a/x.txt"}, []string{under[0].Path, under[1].Path}, "sorted by path")
}

func TestSnapshotTreeHash_MatchesSnapshot(t *testing.T) {
	empty := NewLocalRepo()
	require.NoError(t, empty.Init(t.TempDir()))
	t.Cleanup(func() { _ = empty.Close() })
	hash, err := empty.SnapshotTreeHash()
	require.NoError(t, err)
	require.Empty(t, hash)

	lr := scopedRepo(t, "a.txt")
	snapshot, err := lr.Snapshot()
	require.NoError(t, err)
	require.NotEmpty(t, snapshot.TreeHash)
	hash, err = lr.SnapshotTreeHash()
	require.NoError(t, err)
	require.Equal(t, snapshot.TreeHash, hash)
}

func TestStatEntries_ReadsByPathInBatches(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	t.Cleanup(func() { _ = lr.Close() })

	want := map[string]domain.StatEntry{}
	var paths []string
	for i := 0; i < 2*pathBatch+3; i++ {
		p := fmt.Sprintf("d/f%04d.txt", i)
		want[p] = statEntry(byte(i), int64(i))
		paths = append(paths, p)
	}
	require.NoError(t, lr.SaveStatEntries(want))
	require.NoError(t, lr.SaveStatEntries(map[string]domain.StatEntry{"other.txt": statEntry(1, 1)}))

	got, err := lr.StatEntries(append([]string{"missing.txt"}, paths...))
	require.NoError(t, err)
	require.Equal(t, want, got)

	got, err = lr.StatEntries([]string{"d/f0001.txt", "missing.txt"})
	require.NoError(t, err)
	require.Equal(t, map[string]domain.StatEntry{"d/f0001.txt": want["d/f0001.txt"]}, got)

	got, err = lr.StatEntries(nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestFilesWithChunk_FindsEveryFileListingTheChunk(t *testing.T) {
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	t.Cleanup(func() { _ = lr.Close() })
	shared := pushSpec{seed: 7, chunks: 3, perm: 0o644, encoding: "zstd"}
	require.NoError(t, lr.SaveSyncedTree(manifestTree(map[string]pushSpec{
		"a.txt":     shared,
		"dir/b.txt": shared,
		"c.txt":     spec(9),
	}), "c1", nil))

	users, err := lr.FilesWithChunk(serverDomain.Hash{7, 2})
	require.NoError(t, err)
	var paths []string
	for _, f := range users {
		paths = append(paths, f.Path)
		require.Len(t, f.Chunks, 3)
	}
	require.ElementsMatch(t, []string{"a.txt", "dir/b.txt"}, paths)

	users, err = lr.FilesWithChunk(serverDomain.Hash{9, 1})
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "c.txt", users[0].Path)

	users, err = lr.FilesWithChunk(serverDomain.Hash{0xee})
	require.NoError(t, err)
	require.Empty(t, users)

	// Bytes that only appear across the boundary of two neighbouring chunk
	// hashes are not a chunk of the file.
	first, second := serverDomain.Hash{7, 1}, serverDomain.Hash{7, 2}
	var straddle serverDomain.Hash
	copy(straddle[:], append(append([]byte{}, first[16:]...), second[:16]...))
	users, err = lr.FilesWithChunk(straddle)
	require.NoError(t, err)
	require.Empty(t, users)
}

func TestRemoveObjects_DeletesOnlyTheNamedObjects(t *testing.T) {
	lr := NewLocalRepo()
	target := t.TempDir()
	require.NoError(t, lr.Init(target))
	t.Cleanup(func() { _ = lr.Close() })
	a, b, c := serverDomain.Hash{0xa1, 1}, serverDomain.Hash{0xa1, 2}, serverDomain.Hash{0xc3}
	require.NoError(t, lr.StoreChunk(a, []byte("aaaa")))
	require.NoError(t, lr.StoreChunk(b, []byte("bb")))
	require.NoError(t, lr.StoreChunk(c, []byte("c")))

	files, freed, err := lr.RemoveObjects(nil)
	require.NoError(t, err)
	require.Zero(t, files)
	require.Zero(t, freed)

	files, freed, err = lr.RemoveObjects([]serverDomain.Hash{a, c, {0xdd}, {0xa1, 9}, a})
	require.NoError(t, err)
	require.Equal(t, 2, files)
	require.Equal(t, int64(5), freed)

	missing, err := lr.MissingChunks([]serverDomain.Hash{a, b, c})
	require.NoError(t, err)
	require.ElementsMatch(t, []serverDomain.Hash{a, c}, missing)
}

func TestRemoveObjects_LeavesLinkedFanoutAlone(t *testing.T) {
	lr := NewLocalRepo()
	target := t.TempDir()
	require.NoError(t, lr.Init(target))
	t.Cleanup(func() { _ = lr.Close() })

	h := serverDomain.Hash{0xab, 1}
	name := h.String()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, name[2:]), []byte("keep"), 0o644))
	if err := os.Symlink(outside, filepath.Join(target, ConfigDir, ObjectsDir, name[:2])); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A folder where an object would be is not an object either.
	d := serverDomain.Hash{0xcd, 1}
	require.NoError(t, os.MkdirAll(filepath.Join(target, ConfigDir, ObjectsDir, d.String()[:2], d.String()[2:]), 0o755))

	files, _, err := lr.RemoveObjects([]serverDomain.Hash{h, d})
	require.NoError(t, err)
	require.Zero(t, files)
	_, err = os.Stat(filepath.Join(outside, name[2:]))
	require.NoError(t, err, "nothing outside the object cache is deleted")
}

func TestRemoveObjects_MissingOrLinkedObjectsFolder(t *testing.T) {
	lr := NewLocalRepo()
	target := t.TempDir()
	require.NoError(t, lr.Init(target))
	t.Cleanup(func() { _ = lr.Close() })
	objects := filepath.Join(target, ConfigDir, ObjectsDir)
	require.NoError(t, os.RemoveAll(objects))

	files, _, err := lr.RemoveObjects([]serverDomain.Hash{{1}})
	require.NoError(t, err)
	require.Zero(t, files)

	if err := os.Symlink(t.TempDir(), objects); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, _, err = lr.RemoveObjects([]serverDomain.Hash{{1}})
	require.Error(t, err)
}

func TestScopedReads_FailWithoutAnOpenRepository(t *testing.T) {
	lr := NewLocalRepo()
	_, err := lr.SnapshotFiles([]string{"a"})
	require.Error(t, err)
	_, err = lr.SnapshotFilesUnder("a")
	require.Error(t, err)
	_, err = lr.TracksUnder("a")
	require.Error(t, err)
	_, err = lr.SnapshotTreeHash()
	require.Error(t, err)
	_, err = lr.FilesWithChunk(serverDomain.Hash{1})
	require.Error(t, err)
	_, _, err = lr.RemoveObjects([]serverDomain.Hash{{1}})
	require.Error(t, err)
	_, err = lr.StatEntries([]string{"a"})
	require.Error(t, err)
}

func TestScopedReads_FailOnBrokenDatabase(t *testing.T) {
	broken := func(t *testing.T, table string) *LocalRepo {
		lr := scopedRepo(t, "a.txt")
		_, err := lr.db.Exec("DROP TABLE " + table)
		require.NoError(t, err)
		return lr
	}
	lr := broken(t, "files")
	_, err := lr.SnapshotFiles([]string{"a.txt"})
	require.Error(t, err)
	_, err = lr.SnapshotFilesUnder("")
	require.Error(t, err)
	_, err = lr.TracksUnder("")
	require.Error(t, err)
	_, err = lr.FilesWithChunk(serverDomain.Hash{1})
	require.Error(t, err)

	lr = broken(t, "meta")
	_, err = lr.SnapshotTreeHash()
	require.Error(t, err)

	lr = broken(t, "stat_cache")
	_, err = lr.StatEntries([]string{"a.txt"})
	require.Error(t, err)

	// A chunk blob that is not a whole number of hashes is refused.
	lr = scopedRepo(t, "a.txt", "d/b.txt")
	_, err = lr.db.Exec("UPDATE files SET chunks = X'0102'")
	require.NoError(t, err)
	_, err = lr.SnapshotFiles([]string{"a.txt"})
	require.Error(t, err)
	_, err = lr.SnapshotFilesUnder("d")
	require.Error(t, err)
	_, err = lr.FilesWithChunk(serverDomain.Hash{1, 2})
	require.NoError(t, err, "no row matches the hash, so none is decoded")
	var whole serverDomain.Hash
	_, err = lr.db.Exec("UPDATE files SET chunks = ? || X'01'", whole[:])
	require.NoError(t, err)
	_, err = lr.FilesWithChunk(whole)
	require.Error(t, err)
}
