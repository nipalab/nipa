package localrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
)

func TestDiskUsage_Empty(t *testing.T) {
	lr := newTestLocalRepo(t)
	usage, err := lr.DiskUsage()
	require.NoError(t, err)
	require.Zero(t, usage.WorkingFiles)
	require.Zero(t, usage.WorkingBytes)
	require.Zero(t, usage.CacheObjects)
	require.Zero(t, usage.CacheBytes)
	require.Empty(t, usage.Pins)
}

func TestDiskUsage_SnapshotObjectsAndPins(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.SaveTree(treeFixture()))
	a, b := []byte("first chunk"), []byte("second")
	require.NoError(t, lr.StoreChunk(chunker.Sum(a), a))
	require.NoError(t, lr.StoreChunk(chunker.Sum(b), b))
	require.NoError(t, lr.PinPaths([]string{"assets"}))

	objects := filepath.Join(lr.target, ConfigDir, ObjectsDir)
	dir := filepath.Dir(lr.objectPath(chunker.Sum(a)))
	require.NoError(t, os.WriteFile(filepath.Join(dir, tempFilePrefix+"x"), []byte("tmp"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(objects, "stray"), []byte("ignored"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(objects, "long-name"), 0o755))

	usage, err := lr.DiskUsage()
	require.NoError(t, err)
	require.Equal(t, int64(1), usage.WorkingFiles)
	require.Equal(t, int64(10), usage.WorkingBytes)
	require.Equal(t, int64(2), usage.CacheObjects)
	require.Equal(t, int64(len(a)+len(b)+3), usage.CacheBytes)
	require.Equal(t, []string{"assets"}, usage.Pins)
}

func TestDiskUsage_MissingObjectsDir(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, os.RemoveAll(filepath.Join(lr.target, ConfigDir, ObjectsDir)))
	usage, err := lr.DiskUsage()
	require.NoError(t, err)
	require.Zero(t, usage.CacheBytes)
}

func TestDiskUsage_NotInitialized(t *testing.T) {
	_, err := NewLocalRepo().DiskUsage()
	require.Error(t, err)
}

func TestDiskUsage_ObjectsNotADirectory(t *testing.T) {
	lr := newTestLocalRepo(t)
	objects := filepath.Join(lr.target, ConfigDir, ObjectsDir)
	require.NoError(t, os.RemoveAll(objects))
	require.NoError(t, os.WriteFile(objects, nil, 0o644))
	_, err := lr.DiskUsage()
	require.Error(t, err)
}
