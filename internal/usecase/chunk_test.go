package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/storage"
)

func newChunkFixture(t *testing.T) (*Chunk, *MockchunkRepository, *storage.LocalStore) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockchunkRepository(ctrl)
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	return NewChunk(repo, store), repo, store
}

func TestChunk_UploadStoresAndRecords(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)

	data := []byte("chunk one")
	hash := chunker.Sum(data)
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

	isNew, err := uc.Upload(ctx, hash, data)
	require.NoError(t, err)
	require.True(t, isNew)

	got, err := store.Get(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestChunk_UploadDedupes(t *testing.T) {
	ctx := context.Background()
	uc, repo, _ := newChunkFixture(t)

	data := []byte("dedupe me")
	hash := chunker.Sum(data)
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

	first, err := uc.Upload(ctx, hash, data)
	require.NoError(t, err)
	require.True(t, first)

	second, err := uc.Upload(ctx, hash, data)
	require.NoError(t, err)
	require.False(t, second)
}

func TestChunk_UploadHashMismatch(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	_, err := uc.Upload(ctx, domain.Hash{9}, []byte("not matching"))
	require400(t, err, "chunk hash mismatch")
}

func TestChunk_DownloadFound(t *testing.T) {
	ctx := context.Background()
	uc, repo, _ := newChunkFixture(t)

	data := []byte("download me")
	hash := chunker.Sum(data)
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
	_, err := uc.Upload(ctx, hash, data)
	require.NoError(t, err)

	got, err := uc.Download(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestChunk_DownloadMissing(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	_, err := uc.Download(ctx, chunker.Sum([]byte("absent")))
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}
