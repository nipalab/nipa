package localrepo

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func newShelfRepo(t *testing.T) *LocalRepo {
	t.Helper()
	l := NewLocalRepo()
	require.NoError(t, l.Init(t.TempDir()))
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestShelves_CreateListLoadDrop(t *testing.T) {
	l := newShelfRepo(t)
	h := serverDomain.Hash{1, 2, 3}
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	first, err := l.CreateShelf(func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error) {
		require.NoError(t, put(h, []byte("chunk")))
		require.NoError(t, put(h, []byte("chunk")))
		return &domain.Shelf{Name: "first", CreatedAt: older, Entries: []domain.ShelfEntry{{Path: "a.txt", Status: domain.ShelfModified}}}, nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, first.ID)
	require.EqualValues(t, 5, first.SizeBytes)
	second, err := l.CreateShelf(func(func(serverDomain.Hash, []byte) error) (*domain.Shelf, error) {
		return &domain.Shelf{Name: "second", CreatedAt: older.Add(time.Hour)}, nil
	})
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	list, err := l.ListShelves()
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, "second", list[0].Name, "newest first")

	loaded, err := l.LoadShelf(first.ID)
	require.NoError(t, err)
	require.Equal(t, "a.txt", loaded.Entries[0].Path)
	require.True(t, l.HasShelfChunk(first.ID, h))
	require.False(t, l.HasShelfChunk(first.ID, serverDomain.Hash{9}))
	require.False(t, l.HasShelfChunk("../x", h))
	data, err := l.LoadShelfChunk(first.ID, h)
	require.NoError(t, err)
	require.Equal(t, "chunk", string(data))
	_, err = l.OpenShelfChunk(first.ID, serverDomain.Hash{9})
	require.ErrorContains(t, err, "not found")

	require.NoError(t, l.DropShelf(first.ID))
	_, err = l.LoadShelf(first.ID)
	require.ErrorContains(t, err, "not found")
	require.ErrorContains(t, l.DropShelf(first.ID), "not found")
	list, err = l.ListShelves()
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestShelves_FailedBuildLeavesNothing(t *testing.T) {
	l := newShelfRepo(t)
	_, err := l.CreateShelf(func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error) {
		require.NoError(t, put(serverDomain.Hash{1}, []byte("x")))
		return nil, errors.New("boom")
	})
	require.ErrorContains(t, err, "boom")
	entries, err := os.ReadDir(filepath.Join(l.target, ConfigDir, ShelvesDir))
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestShelves_InvalidIDsAndBrokenManifests(t *testing.T) {
	l := newShelfRepo(t)
	for _, id := range []string{"", "../x", "A", "a/b"} {
		_, err := l.LoadShelf(id)
		require.ErrorContains(t, err, "invalid shelf id")
		require.Error(t, l.DropShelf(id))
		_, err = l.OpenShelfChunk(id, serverDomain.Hash{})
		require.Error(t, err)
	}
	_, err := l.LoadShelfChunk("zz", serverDomain.Hash{})
	require.Error(t, err)

	list, err := l.ListShelves()
	require.NoError(t, err)
	require.Empty(t, list)

	root := filepath.Join(l.target, ConfigDir, ShelvesDir)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "broken"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "broken", shelfManifest), []byte("{"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o644))
	_, err = l.LoadShelf("broken")
	require.ErrorContains(t, err, "read shelf")
	list, err = l.ListShelves()
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestShelves_SweepsStaleTemps(t *testing.T) {
	l := newShelfRepo(t)
	root := filepath.Join(l.target, ConfigDir, ShelvesDir)
	stale := filepath.Join(root, shelfTmpPrefix+"old")
	fresh := filepath.Join(root, shelfTmpPrefix+"new")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.MkdirAll(fresh, 0o755))
	past := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stale, past, past))

	w, err := l.NewShelfWriter()
	require.NoError(t, err)
	w.Abort()
	require.NoDirExists(t, stale)
	require.DirExists(t, fresh)
	require.Zero(t, w.Bytes())
}

func TestShelves_State(t *testing.T) {
	l := newShelfRepo(t)
	state, err := l.LoadShelfState()
	require.NoError(t, err)
	require.Nil(t, state)
	require.NoError(t, l.SaveShelfState(&domain.ShelfState{ShelfID: "abc", Conflicts: []string{"a"}}))
	state, err = l.LoadShelfState()
	require.NoError(t, err)
	require.Equal(t, "abc", state.ShelfID)
	require.NoError(t, l.ClearShelfState())
	state, err = l.LoadShelfState()
	require.NoError(t, err)
	require.Nil(t, state)
}

func TestShelves_Uninitialized(t *testing.T) {
	l := NewLocalRepo()
	_, err := l.NewShelfWriter()
	require.Error(t, err)
	_, err = l.ListShelves()
	require.Error(t, err)
	_, err = l.LoadShelfState()
	require.Error(t, err)
	require.Error(t, l.SaveShelfState(&domain.ShelfState{}))
	require.Error(t, l.ClearShelfState())
	_, err = l.CreateShelf(nil)
	require.Error(t, err)
}

func TestShelves_OpenChunkReads(t *testing.T) {
	l := newShelfRepo(t)
	h := serverDomain.Hash{4}
	shelf, err := l.CreateShelf(func(put func(serverDomain.Hash, []byte) error) (*domain.Shelf, error) {
		return &domain.Shelf{}, put(h, []byte("bytes"))
	})
	require.NoError(t, err)
	r, err := l.OpenShelfChunk(shelf.ID, h)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, "bytes", string(data))
}
