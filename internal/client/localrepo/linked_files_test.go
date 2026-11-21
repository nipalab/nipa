package localrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// moveAwayAndLink moves dir aside (the open handles keep following it) and
// leaves a link to a fresh outside folder in its place.
func moveAwayAndLink(t *testing.T, dir string) (moved, outside string) {
	t.Helper()
	moved = filepath.Join(t.TempDir(), "moved")
	require.NoError(t, os.Rename(dir, moved))
	outside = t.TempDir()
	symlinkOrSkip(t, outside, dir)
	return moved, outside
}

func TestShelfWriter_StaysInTheVerifiedFolderWhenLinkSwappedIn(t *testing.T) {
	lr := newTestLocalRepo(t)
	w, err := lr.NewShelfWriter()
	require.NoError(t, err)
	moved, outside := moveAwayAndLink(t, lr.shelvesRoot())

	h := serverDomain.Hash{9}
	require.NoError(t, w.PutChunk(h, []byte("data")))
	require.NoError(t, w.PutChunk(h, []byte("data")), "a stored chunk is not written twice")
	require.Equal(t, int64(4), w.Bytes())
	require.NoError(t, w.Commit(&domain.Shelf{Name: "s"}))

	require.Empty(t, entries(t, outside), "nothing is created behind the link")
	require.FileExists(t, filepath.Join(moved, w.id, shelfManifest))
	require.FileExists(t, shelfObjectPath(filepath.Join(moved, w.id), h))
	require.NoDirExists(t, filepath.Join(moved, w.tmp))
}

func TestShelfWriter_AbortThroughSwappedLinkRemovesNothingOutside(t *testing.T) {
	lr := newTestLocalRepo(t)
	w, err := lr.NewShelfWriter()
	require.NoError(t, err)
	require.NoError(t, w.PutChunk(serverDomain.Hash{1}, []byte("x")))
	moved, outside := moveAwayAndLink(t, lr.shelvesRoot())
	require.NoError(t, os.MkdirAll(filepath.Join(outside, w.tmp), 0o755))

	w.Abort()
	w.Abort()
	require.DirExists(t, filepath.Join(outside, w.tmp), "the lookalike behind the link survives")
	require.NoDirExists(t, filepath.Join(moved, w.tmp))
}

func TestShelfWriter_NormalFlowAndClosedWriter(t *testing.T) {
	lr := newTestLocalRepo(t)
	shelf, err := lr.CreateShelf(func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error) {
		return &domain.Shelf{Name: "ok"}, put(serverDomain.Hash{2}, []byte("abc"))
	})
	require.NoError(t, err)
	got, err := lr.LoadShelfChunk(shelf.ID, serverDomain.Hash{2})
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), got)
	require.NoError(t, lr.DropShelf(shelf.ID))

	w, err := lr.NewShelfWriter()
	require.NoError(t, err)
	require.NoError(t, w.Commit(&domain.Shelf{}))
	require.ErrorIs(t, w.Commit(&domain.Shelf{}), errShelfWriterClosed)
	require.ErrorIs(t, w.PutChunk(serverDomain.Hash{3}, nil), errShelfWriterClosed)
	w.Abort()
	require.NoError(t, lr.DropShelf(w.id))
}

func TestShelfWriter_ErrorPathsCloseAndClean(t *testing.T) {
	lr := newTestLocalRepo(t)
	w, err := lr.NewShelfWriter()
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(lr.shelvesRoot(), w.tmp)))
	require.NoError(t, os.WriteFile(filepath.Join(lr.shelvesRoot(), w.tmp), nil, 0o644))
	require.Error(t, w.PutChunk(serverDomain.Hash{5}, []byte("x")), "the temp folder is a file")
	require.Error(t, w.Commit(&domain.Shelf{}), "the manifest cannot be written")
	require.Nil(t, w.root, "a failed commit closes the writer")

	w, err = lr.NewShelfWriter()
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(filepath.Join(lr.shelvesRoot(), w.tmp)))
	require.NoError(t, os.Mkdir(filepath.Join(lr.shelvesRoot(), w.id), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lr.shelvesRoot(), w.id, "f"), nil, 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(lr.shelvesRoot(), w.tmp), 0o755))
	require.Error(t, w.Commit(&domain.Shelf{}), "the target is a non-empty folder")
	require.NoDirExists(t, filepath.Join(lr.shelvesRoot(), w.tmp))

	w, err = lr.NewShelfWriter()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(lr.shelvesRoot(), w.tmp, ObjectsDir), nil, 0o644))
	require.Error(t, w.PutChunk(serverDomain.Hash{6}, []byte("x")), "objects is a file")
	w.Abort()
}

func TestBeginWorkingWrite_StaysInTheVerifiedFolderWhenLinkSwappedIn(t *testing.T) {
	lr := newTestLocalRepo(t)
	end, _, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	names := entries(t, lr.writesDir())
	require.Len(t, names, 1)
	moved, outside := moveAwayAndLink(t, lr.writesDir())
	require.NoError(t, os.WriteFile(filepath.Join(outside, names[0]), []byte("keep"), 0o644))

	end()
	require.FileExists(t, filepath.Join(outside, names[0]), "the marker name behind the link is untouched")
	require.Empty(t, entries(t, moved), "the marker is removed from the verified folder")
}

func TestBeginWorkingWrite_StaleAndLiveMarkersStillWork(t *testing.T) {
	lr := newTestLocalRepo(t)
	leaveDeadMarker(t, lr, "w-dead")
	first, interrupted, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.True(t, interrupted)
	leaveDeadMarker(t, lr, "w-dead2")
	second, interrupted, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.False(t, interrupted, "a live write keeps stale markers for later")
	require.Equal(t, 3, markerCount(t, lr))
	second()
	first()
	third, interrupted, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.True(t, interrupted)
	third()
	require.Zero(t, markerCount(t, lr))
}

func TestCreateMarker_ClosedRootFails(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, root.Close())
	_, _, err = createMarker(root)
	require.Error(t, err)
}

func TestInit_RefusesLinkedDatabaseAndLockFiles(t *testing.T) {
	for _, name := range []string{DBFile, DBFile + "-wal", DBFile + "-shm", LockFile} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside")
			require.NoError(t, os.WriteFile(outside, nil, 0o644))
			symlinkOrSkip(t, outside, filepath.Join(root, ConfigDir, name))
			lr := NewLocalRepo()
			t.Cleanup(func() { _ = lr.Close() })
			requireLinkedMetadata(t, lr.Init(root), ".nipa/"+name)
			info, err := os.Stat(outside)
			require.NoError(t, err)
			require.Zero(t, info.Size(), "the link target is untouched")
			require.Nil(t, lr.db)
		})
	}
}

func TestInit_FreshAndExistingCloneStillWork(t *testing.T) {
	root := t.TempDir()
	lr := NewLocalRepo()
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	release, err := lr.LockShared()
	require.NoError(t, err)
	release()
	require.NoError(t, lr.Init(root), "an existing clone with its lock file reopens")
	_, ok, err := lr.TryLockExclusive()
	require.NoError(t, err)
	require.True(t, ok)
}

func TestOpenLock_RefusesALinkedLockFile(t *testing.T) {
	lr := newTestLocalRepo(t)
	release, err := lr.LockShared()
	require.NoError(t, err)
	release()
	lockPath := filepath.Join(lr.target, ConfigDir, LockFile)
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, nil, 0o644))
	require.NoError(t, os.Remove(lockPath))
	symlinkOrSkip(t, outside, lockPath)

	_, err = lr.LockShared()
	requireLinkedMetadata(t, err, ".nipa/"+LockFile)
	_, _, err = lr.TryLockExclusive()
	requireLinkedMetadata(t, err, ".nipa/"+LockFile)
	info, err := os.Stat(outside)
	require.NoError(t, err)
	require.Zero(t, info.Size())
}

func TestOpenLock_RefusesALinkedNipaFolderAndUninitialized(t *testing.T) {
	lr := newTestLocalRepo(t)
	_, outside := moveAwayAndLink(t, filepath.Join(lr.target, ConfigDir))
	_, err := lr.LockShared()
	requireLinkedMetadata(t, err, ".nipa")
	require.Empty(t, entries(t, outside))

	_, err = NewLocalRepo().LockShared()
	require.Error(t, err)
}

func TestOpenLock_ReportsAnUnopenableLockFile(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, os.Mkdir(filepath.Join(lr.target, ConfigDir, LockFile), 0o755))
	_, err := lr.LockShared()
	require.Error(t, err, "the lock file is a folder")
}
