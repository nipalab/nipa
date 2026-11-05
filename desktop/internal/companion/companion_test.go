package companion

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestName(t *testing.T) {
	if runtime.GOOS == "windows" {
		require.Equal(t, "nipa.exe", Name())
	} else {
		require.Equal(t, "nipa", Name())
	}
}

func TestInstallWithoutPayload(t *testing.T) {
	dir := t.TempDir()
	require.Equal(t, len(payload) > 0, Bundled())
	changed, err := install(dir, nil)
	require.NoError(t, err)
	require.False(t, changed)
	_, err = os.Stat(filepath.Join(dir, Name()))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestInstallWritesAndSkipsIdentical(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, Name())

	changed, err := install(dir, []byte("v1"))
	require.NoError(t, err)
	require.True(t, changed)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "v1", string(data))
	if runtime.GOOS != "windows" {
		info, err := os.Stat(target)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}

	changed, err = install(dir, []byte("v1"))
	require.NoError(t, err)
	require.False(t, changed)

	require.NoError(t, os.WriteFile(target+".old", []byte("stale"), 0o644))
	changed, err = install(dir, []byte("v2!"))
	require.NoError(t, err)
	require.True(t, changed)
	data, err = os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "v2!", string(data))
	_, err = os.Stat(target + ".old")
	require.ErrorIs(t, err, os.ErrNotExist, "leftovers from an earlier swap are swept")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp files are left behind")
}

func TestInstallSameSizeDifferentContent(t *testing.T) {
	dir := t.TempDir()
	_, err := install(dir, []byte("aaaa"))
	require.NoError(t, err)
	changed, err := install(dir, []byte("bbbb"))
	require.NoError(t, err)
	require.True(t, changed)
}

func TestInstallFailsWhenDirIsMissing(t *testing.T) {
	_, err := install(filepath.Join(t.TempDir(), "missing"), []byte("x"))
	require.Error(t, err)
}

func TestInstallMovesABusyTargetAside(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, Name())
	require.NoError(t, os.MkdirAll(filepath.Join(target, "child"), 0o755))
	changed, err := install(dir, []byte("x"))
	require.NoError(t, err)
	require.True(t, changed)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "x", string(data))
	_, err = os.Stat(filepath.Join(target+".old", "child"))
	require.NoError(t, err)
}

func TestInstallFailsInReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permissions are not enforced")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err := install(dir, []byte("x"))
	require.Error(t, err)
}

func TestInstallPublic(t *testing.T) {
	changed, err := Install(t.TempDir())
	require.NoError(t, err)
	require.Equal(t, Bundled(), changed)
}
