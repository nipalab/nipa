package localrepo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestPruneObjects_KeepsAndDeletes(t *testing.T) {
	lr := newTestLocalRepo(t)

	kept := []byte("kept chunk")
	dropped := []byte("dropped chunk!")
	keptHash := chunker.Sum(kept)
	droppedHash := chunker.Sum(dropped)
	require.NoError(t, lr.StoreChunk(keptHash, kept))
	require.NoError(t, lr.StoreChunk(droppedHash, dropped))

	files, freed, err := lr.PruneObjects(map[serverDomain.Hash]struct{}{keptHash: {}})
	require.NoError(t, err)
	require.Equal(t, 1, files)
	require.Equal(t, int64(len(dropped)), freed)

	got, err := lr.LoadChunk(keptHash)
	require.NoError(t, err)
	require.Equal(t, kept, got)
	_, err = lr.LoadChunk(droppedHash)
	require.Error(t, err)

	files, freed, err = lr.PruneObjects(map[serverDomain.Hash]struct{}{keptHash: {}})
	require.NoError(t, err)
	require.Zero(t, files)
	require.Zero(t, freed)
}

func TestPruneObjects_SweepsStaleTempFiles(t *testing.T) {
	lr := newTestLocalRepo(t)

	data := []byte("chunk")
	hash := chunker.Sum(data)
	require.NoError(t, lr.StoreChunk(hash, data))
	dir := filepath.Dir(lr.objectPath(hash))

	stale := filepath.Join(dir, tempFilePrefix+"stale")
	fresh := filepath.Join(dir, tempFilePrefix+"fresh")
	foreign := filepath.Join(dir, "not-a-hash")
	require.NoError(t, os.WriteFile(stale, []byte("abc"), 0o644))
	require.NoError(t, os.WriteFile(fresh, []byte("abcd"), 0o644))
	require.NoError(t, os.WriteFile(foreign, []byte("x"), 0o644))
	old := time.Now().Add(-2 * staleTempAge)
	require.NoError(t, os.Chtimes(stale, old, old))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lr.target, ConfigDir, ObjectsDir, "stray"), nil, 0o644))

	files, freed, err := lr.PruneObjects(map[serverDomain.Hash]struct{}{hash: {}})
	require.NoError(t, err)
	require.Equal(t, 1, files)
	require.Equal(t, int64(3), freed)

	require.NoFileExists(t, stale)
	require.FileExists(t, fresh)
	require.FileExists(t, foreign)
	require.FileExists(t, lr.objectPath(hash))
}

func TestPruneObjects_EmptyKeepDeletesAll(t *testing.T) {
	lr := newTestLocalRepo(t)
	for _, s := range []string{"a", "bb", "ccc"} {
		require.NoError(t, lr.StoreChunk(chunker.Sum([]byte(s)), []byte(s)))
	}
	files, freed, err := lr.PruneObjects(nil)
	require.NoError(t, err)
	require.Equal(t, 3, files)
	require.Equal(t, int64(6), freed)
}

func TestPruneObjects_MissingObjectsDir(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, os.RemoveAll(filepath.Join(lr.target, ConfigDir, ObjectsDir)))
	files, freed, err := lr.PruneObjects(nil)
	require.NoError(t, err)
	require.Zero(t, files)
	require.Zero(t, freed)
}

func TestPruneObjects_NotInitialized(t *testing.T) {
	_, _, err := NewLocalRepo().PruneObjects(nil)
	require.Error(t, err)
}

func TestSchema_PinnedPathsWithoutSnapshotID(t *testing.T) {
	lr := newTestLocalRepo(t)
	_, err := lr.db.Exec(`INSERT INTO pinned_paths (path, pinned_at) VALUES ('a/b', 1)`)
	require.NoError(t, err)
	for _, table := range []string{"files", "tree_nodes"} {
		var n int
		require.NoError(t, lr.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'snapshot_id'`, table).Scan(&n))
		require.Zero(t, n, table)
	}
}

func TestPruneObjects_FilesystemErrors(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced here")
	}
	lr := newTestLocalRepo(t)
	data := []byte("locked away")
	hash := chunker.Sum(data)
	require.NoError(t, lr.StoreChunk(hash, data))
	dir := filepath.Dir(lr.objectPath(hash))

	require.NoError(t, os.Chmod(dir, 0o500))
	_, _, err := lr.PruneObjects(nil)
	require.Error(t, err, "a read-only directory cannot be pruned")

	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, _, err = lr.PruneObjects(nil)
	require.Error(t, err, "an unreadable directory fails the walk")
}
