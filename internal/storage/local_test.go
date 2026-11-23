package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const testProject snow.ID = 42

func TestLocalStore_PutGet(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	hash := chunker.Sum([]byte("hello storage"))
	require.NoError(t, store.Put(ctx, testProject, hash, []byte("hello storage")))

	got, err := store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, []byte("hello storage"), got)
}

func TestLocalStore_PutIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	hash := chunker.Sum([]byte("same content"))
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Put(ctx, testProject, hash, []byte("same content")))
	}
	got, err := store.Get(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, []byte("same content"), got)
}

func TestLocalStore_Exists(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	present := chunker.Sum([]byte("present"))
	missing := chunker.Sum([]byte("missing"))

	require.NoError(t, store.Put(ctx, testProject, present, []byte("present")))
	ok, err := store.Exists(ctx, testProject, present)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.Exists(ctx, testProject, missing)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestLocalStore_Size(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	data := []byte("size me")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject, hash, data))

	size, err := store.Size(ctx, testProject, hash)
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), size)

	_, err = store.Size(ctx, testProject, chunker.Sum([]byte("missing")))
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestLocalStore_GetMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	hash := chunker.Sum([]byte("nope"))
	_, err = store.Get(ctx, testProject, hash)
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestLocalStore_DefaultRoot(t *testing.T) {
	t.Chdir(t.TempDir())
	store, err := NewLocalStore("")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	require.Equal(t, "./chunks", store.root)

	hash := chunker.Sum([]byte("x"))
	require.NoError(t, store.Put(context.Background(), testProject, hash, []byte("x")))
	full := store.path(testProject, hash)
	base := filepath.Base(full)
	dir := filepath.Base(filepath.Dir(full))
	require.Equal(t, hash.String()[2:], base)
	require.Equal(t, hash.String()[:2], dir)
	require.Equal(t, filepath.Join("chunks", "p", testProject.Base36(), hash.String()[:2], hash.String()[2:]), filepath.Clean(full))
	_, err = os.Stat(full)
	require.NoError(t, err)
}

func TestLocalStore_NewErrorsOnUncreatableRoot(t *testing.T) {
	_, err := NewLocalStore("/proc/definitely-not-a-dir/chunks")
	require.Error(t, err)
}

func TestLocalStore_Open(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := NewLocalStore(root)
	require.NoError(t, err)

	hash := chunker.Sum([]byte("streamed"))
	require.NoError(t, store.Put(ctx, testProject, hash, []byte("streamed")))

	r, err := store.Open(ctx, testProject, hash)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("streamed"), got)

	_, err = store.Open(ctx, testProject, chunker.Sum([]byte("absent")))
	require.True(t, domain.IsErrorNotFound(err), "an absent chunk is a domain NotFound, got %v", err)

	// A directory where the shard file should be is an I/O error, not NotFound.
	odd := chunker.Sum([]byte("odd"))
	hex := odd.String()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "p", testProject.Base36(), hex[:2], hex[2:], "x"), 0o755))
	r, err = store.Open(ctx, testProject, odd)
	if err == nil {
		_, err = io.ReadAll(r)
		_ = r.Close()
	}
	require.Error(t, err)
	require.False(t, domain.IsErrorNotFound(err))
}

func TestLocalStore_ProjectsAreSeparate(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)

	data := []byte("shared bytes")
	hash := chunker.Sum(data)
	const other snow.ID = 43
	require.NoError(t, store.Put(ctx, testProject, hash, data))

	ok, err := store.Exists(ctx, other, hash)
	require.NoError(t, err)
	require.False(t, ok, "the same hash in another project is another object")
	_, err = store.Get(ctx, other, hash)
	require.True(t, domain.IsErrorNotFound(err))

	require.NoError(t, store.Put(ctx, other, hash, data))
	require.NotEqual(t, store.path(testProject, hash), store.path(other, hash))

	require.NoError(t, store.DeleteProject(ctx, testProject))
	ok, err = store.Exists(ctx, testProject, hash)
	require.NoError(t, err)
	require.False(t, ok)
	got, err := store.Get(ctx, other, hash)
	require.NoError(t, err, "deleting one project leaves the other's object")
	require.Equal(t, data, got)

	require.NoError(t, store.DeleteProject(ctx, 99), "a project without content is not an error")
}
