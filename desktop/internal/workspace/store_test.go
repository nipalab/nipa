package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStore_AddRemovePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "desktop.json")
	s := NewStore(path)

	repos, err := s.Repos()
	require.NoError(t, err)
	require.Empty(t, repos)

	require.NoError(t, s.Add("/work/a"))
	require.NoError(t, s.Add("/work/b"))
	require.NoError(t, s.Add("/work/a"))

	repos, err = NewStore(path).Repos()
	require.NoError(t, err)
	require.Equal(t, []string{"/work/a", "/work/b"}, repos)

	require.NoError(t, s.Remove("/work/a"))
	require.NoError(t, s.Remove("/work/missing"))
	repos, err = s.Repos()
	require.NoError(t, err)
	require.Equal(t, []string{"/work/b"}, repos)

	info, err := os.Stat(path)
	require.NoError(t, err)
	if info.Mode().Perm() != 0o666 {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestStore_CorruptAndNullFiles(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.json")
	require.NoError(t, os.WriteFile(corrupt, []byte("{"), 0o600))
	s := NewStore(corrupt)
	_, err := s.Repos()
	require.ErrorContains(t, err, "parse")
	require.Error(t, s.Add("/a"))
	require.Error(t, s.Remove("/a"))

	null := filepath.Join(dir, "null.json")
	require.NoError(t, os.WriteFile(null, []byte(`{"repos":null}`), 0o600))
	repos, err := NewStore(null).Repos()
	require.NoError(t, err)
	require.NotNil(t, repos)
	require.Empty(t, repos)
}

func TestStore_WriteErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	require.Error(t, NewStore(filepath.Join(blocker, "desktop.json")).Add("/a"))

	unreadable := filepath.Join(dir, "dir.json")
	require.NoError(t, os.Mkdir(unreadable, 0o700))
	_, err := NewStore(unreadable).Repos()
	require.Error(t, err)

	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	readOnly := filepath.Join(dir, "ro")
	require.NoError(t, os.Mkdir(readOnly, 0o500))
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })
	require.Error(t, NewStore(filepath.Join(readOnly, "desktop.json")).Add("/a"))
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	path, err := DefaultPath()
	require.NoError(t, err)
	require.Equal(t, "desktop.json", filepath.Base(path))
	require.Equal(t, "nipa", filepath.Base(filepath.Dir(path)))
}
