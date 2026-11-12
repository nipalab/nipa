package localrepo

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListDirReadsOneLevel(t *testing.T) {
	l := NewLocalRepo()
	require.NoError(t, l.Init(t.TempDir()))
	defer func() { _ = l.Close() }()
	bin := tf(4)
	bin.binary = true
	require.NoError(t, l.SaveTree(testTree(1, map[string]testFile{
		"README.md":                tf(1),
		"Assets/hero.png":          bin,
		"Assets/Audio/theme.wav":   tf(3),
		"Assets/Audio/Sfx/hit.wav": tf(5),
		"Source/main.cs":           tf(6),
	})))

	dirs, files, err := l.ListDir("")
	require.NoError(t, err)
	require.Equal(t, []string{"Assets", "Source"}, dirs)
	require.Len(t, files, 1)
	require.Equal(t, "README.md", files[0].Path)
	require.Equal(t, int64(1), files[0].SizeBytes)

	dirs, files, err = l.ListDir("Assets")
	require.NoError(t, err)
	require.Equal(t, []string{"Audio"}, dirs)
	require.Len(t, files, 1)
	require.Equal(t, "Assets/hero.png", files[0].Path)
	require.True(t, files[0].IsBinary)
	require.Len(t, files[0].Chunks, 1)

	dirs, files, err = l.ListDir("Assets/Audio/Sfx")
	require.NoError(t, err)
	require.Empty(t, dirs)
	require.Equal(t, "Assets/Audio/Sfx/hit.wav", files[0].Path)

	dirs, files, err = l.ListDir("Nope")
	require.NoError(t, err)
	require.Empty(t, dirs)
	require.Empty(t, files)
}

func TestListDirLargeDirectory(t *testing.T) {
	l := NewLocalRepo()
	require.NoError(t, l.Init(t.TempDir()))
	defer func() { _ = l.Close() }()
	spec := map[string]testFile{"Other/x.txt": tf(1)}
	for i := range 10000 {
		spec[fmt.Sprintf("Big/f%05d.png", i)] = tf(byte(i%200 + 1))
	}
	require.NoError(t, l.SaveTree(testTree(1, spec)))

	dirs, files, err := l.ListDir("Big")
	require.NoError(t, err)
	require.Empty(t, dirs)
	require.Len(t, files, 10000)
	require.Equal(t, "Big/f00000.png", files[0].Path)
}

func TestSnapshotFile(t *testing.T) {
	l := NewLocalRepo()
	require.NoError(t, l.Init(t.TempDir()))
	defer func() { _ = l.Close() }()
	require.NoError(t, l.SaveTree(testTree(1, map[string]testFile{"a/b.txt": tf(7), "c.txt": tf(2)})))

	f, ok, err := l.SnapshotFile("a/b.txt")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "a/b.txt", f.Path)
	require.Equal(t, int64(7), f.SizeBytes)

	f, ok, err = l.SnapshotFile("c.txt")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "c.txt", f.Path)

	_, ok, err = l.SnapshotFile("a")
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = l.SnapshotFile("")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestListDirUninitialized(t *testing.T) {
	l := NewLocalRepo()
	_, _, err := l.ListDir("")
	require.Error(t, err)
	_, _, err = l.SnapshotFile("a")
	require.Error(t, err)
}
