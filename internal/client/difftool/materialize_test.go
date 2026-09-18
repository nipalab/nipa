package difftool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaterialize(t *testing.T) {
	tree, pairs, skipped, err := Materialize([]File{
		{Path: "a.txt", Status: "M", Old: []byte("old\n"), New: []byte("new\n")},
		{Path: "sub/b.png", Status: "M", Old: []byte{0x89, 0x50}, New: []byte{0x89, 0x51}},
		{Path: "new.txt", Status: "A", New: []byte("fresh\n")},
		{Path: "gone.txt", Status: "D", Old: []byte("bye\n")},
		{Path: "missing.txt", Status: "M", Unavailable: true},
	})
	require.NoError(t, err)
	defer os.RemoveAll(tree.Dir)
	require.Equal(t, []string{"missing.txt"}, skipped)
	require.Len(t, pairs, 4)

	require.DirExists(t, tree.OldDir)
	require.DirExists(t, tree.NewDir)
	require.Equal(t, filepath.Join(tree.OldDir, "a.txt"), pairs[0].Local)
	require.Equal(t, filepath.Join(tree.NewDir, "a.txt"), pairs[0].Remote)
	require.Equal(t, "M", pairs[0].Status)

	data, err := os.ReadFile(filepath.Join(tree.OldDir, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, []byte("old\n"), data)
	data, err = os.ReadFile(filepath.Join(tree.NewDir, "sub", "b.png"))
	require.NoError(t, err)
	require.Equal(t, []byte{0x89, 0x51}, data)

	// added files get an empty old side, deleted files an empty new side
	info, err := os.Stat(filepath.Join(tree.OldDir, "new.txt"))
	require.NoError(t, err)
	require.Zero(t, info.Size())
	info, err = os.Stat(filepath.Join(tree.NewDir, "gone.txt"))
	require.NoError(t, err)
	require.Zero(t, info.Size())
}

func TestMaterialize_Empty(t *testing.T) {
	tree, pairs, skipped, err := Materialize(nil)
	require.NoError(t, err)
	defer os.RemoveAll(tree.Dir)
	require.Empty(t, pairs)
	require.Empty(t, skipped)
	require.DirExists(t, tree.OldDir)
	require.DirExists(t, tree.NewDir)
}

func TestMaterialize_TempDirError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(tmp, "does-not-exist"))

	_, _, _, err := Materialize([]File{{Path: "a.txt", Status: "M"}})
	require.Error(t, err)
}

func TestMaterialize_WriteError(t *testing.T) {
	// NUL bytes are rejected by the OS, failing the write after the
	// temp dirs were created; the temp dir must not leak.
	_, _, _, err := Materialize([]File{{Path: "a\x00b.txt", Status: "M", Old: []byte("x"), New: []byte("y")}})
	require.Error(t, err)
}
