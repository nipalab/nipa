package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

func TestLocalStore_PutGet(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	hash := chunker.Sum([]byte("hello storage"))
	require.NoError(t, store.Put(ctx, hash, []byte("hello storage")))

	got, err := store.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, []byte("hello storage"), got)
}

func TestLocalStore_PutIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	hash := chunker.Sum([]byte("same content"))
	for i := 0; i < 3; i++ {
		require.NoError(t, store.Put(ctx, hash, []byte("same content")))
	}
	got, err := store.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, []byte("same content"), got)
}

func TestLocalStore_Exists(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	present := chunker.Sum([]byte("present"))
	missing := chunker.Sum([]byte("missing"))

	require.NoError(t, store.Put(ctx, present, []byte("present")))
	ok, err := store.Exists(ctx, present)
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.Exists(ctx, missing)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestLocalStore_GetMissingReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocalStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	hash := chunker.Sum([]byte("nope"))
	_, err = store.Get(ctx, hash)
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestLocalStore_DefaultRoot(t *testing.T) {
	store, err := NewLocalStore("")
	require.NoError(t, err)
	defer store.Close()
	require.Equal(t, "./chunks", store.root)

	hash := chunker.Sum([]byte("x"))
	require.NoError(t, store.Put(context.Background(), hash, []byte("x")))
	full := store.path(hash)
	base := filepath.Base(full)
	dir := filepath.Base(filepath.Dir(full))
	require.Equal(t, hash.String()[2:], base)
	require.Equal(t, hash.String()[:2], dir)
	_, err = os.Stat(full)
	require.NoError(t, err)
}

func TestLocalStore_NewErrorsOnUncreatableRoot(t *testing.T) {
	_, err := NewLocalStore("/proc/definitely-not-a-dir/chunks")
	require.Error(t, err)
}
