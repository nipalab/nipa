package usecase

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/storage"
)

func newChunkFixture(t *testing.T) (*Chunk, *MockchunkRepository, *storage.LocalStore) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockchunkRepository(ctrl)
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	uc := NewChunk(repo, store, ChunkTransferConfig{
		SigningKey:  "test-signing-key",
		PresignTTL:  time.Hour,
		MaxPageSize: 2,
	})
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }
	return uc, repo, store
}

func verifyChunkURL(t *testing.T, uc *Chunk, org, project, path, op string, size int64) {
	t.Helper()
	u, err := url.Parse(path)
	require.NoError(t, err)
	params, err := chunkurl.ParseParams(u.Query())
	require.NoError(t, err)
	require.Equal(t, op, params.Op)
	require.Equal(t, size, params.Size)
	require.Equal(t, uc.now().Add(uc.transfer.PresignTTL).Unix(), params.Exp)
	hash := u.Path[strings.LastIndex(u.Path, "/")+1:]
	require.NoError(t, chunkurl.Verify(uc.transfer.SigningKey, org, project, hash, params, uc.now()))
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

func TestChunk_PresignUploadURLs(t *testing.T) {
	ctx := context.Background()
	uc, repo, _ := newChunkFixture(t)

	existing := []byte("already here")
	existingHash := chunker.Sum(existing)
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), existingHash, int64(len(existing))).Return(nil)
	_, err := uc.Upload(ctx, existingHash, existing)
	require.NoError(t, err)

	fresh := []byte("fresh chunk")
	freshHash := chunker.Sum(fresh)

	urls, next, err := uc.PresignUploadURLs(ctx, "acme", "game", []ChunkRef{
		{Hash: existingHash, SizeBytes: int64(len(existing))},
		{Hash: freshHash, SizeBytes: int64(len(fresh))},
	}, 10, "")
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, urls, 2)

	require.Equal(t, existingHash, urls[0].Hash)
	require.True(t, urls[0].AlreadyStored)
	require.Empty(t, urls[0].URL)

	require.Equal(t, freshHash, urls[1].Hash)
	require.False(t, urls[1].AlreadyStored)
	verifyChunkURL(t, uc, "acme", "game", urls[1].URL, chunkurl.OpUpload, int64(len(fresh)))
}

func TestChunk_PresignUploadURLsPaginates(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	refs := make([]ChunkRef, 0, 3)
	for _, data := range []string{"one", "two", "three"} {
		hash := chunker.Sum([]byte(data))
		refs = append(refs, ChunkRef{Hash: hash, SizeBytes: int64(len(data))})
	}

	page1, next, err := uc.PresignUploadURLs(ctx, "acme", "game", refs, 10, "")
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, "2", next)
	require.Equal(t, refs[0].Hash, page1[0].Hash)
	require.Equal(t, refs[1].Hash, page1[1].Hash)

	page2, next2, err := uc.PresignUploadURLs(ctx, "acme", "game", refs, 10, next)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Empty(t, next2)
	require.Equal(t, refs[2].Hash, page2[0].Hash)
}

func TestChunk_PresignUploadURLsInvalid(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)
	hash := chunker.Sum([]byte("x"))

	_, _, err := uc.PresignUploadURLs(ctx, "acme", "game", []ChunkRef{{Hash: hash, SizeBytes: 0}}, 10, "")
	require400(t, err, "invalid chunk size")

	_, _, err = uc.PresignUploadURLs(ctx, "acme", "game", []ChunkRef{{Hash: hash, SizeBytes: chunker.DefaultConfig.Max + 1}}, 10, "")
	require400(t, err, "invalid chunk size")

	_, _, err = uc.PresignUploadURLs(ctx, "acme", "game", []ChunkRef{{Hash: hash, SizeBytes: 10}}, 10, "-1")
	require400(t, err, "invalid page token")
}

func TestChunk_PresignDownloadURLs(t *testing.T) {
	uc, _, _ := newChunkFixture(t)
	h1 := chunker.Sum([]byte("one"))
	h2 := chunker.Sum([]byte("two"))

	urls, next, err := uc.PresignDownloadURLs("acme", "game", []domain.Hash{h1, h2}, 10, "")
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, urls, 2)
	require.Equal(t, h1, urls[0].Hash)
	require.Equal(t, h2, urls[1].Hash)
	require.False(t, urls[0].AlreadyStored)
	verifyChunkURL(t, uc, "acme", "game", urls[0].URL, chunkurl.OpDownload, 0)
	verifyChunkURL(t, uc, "acme", "game", urls[1].URL, chunkurl.OpDownload, 0)
}

func TestChunk_ConfirmUploads(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)

	present := []byte("present")
	presentHash := chunker.Sum(present)
	require.NoError(t, store.Put(ctx, presentHash, present))

	absentHash := chunker.Sum([]byte("absent"))
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), presentHash, int64(len(present))).Return(nil)

	missing, err := uc.ConfirmUploads(ctx, []ChunkRef{
		{Hash: presentHash, SizeBytes: int64(len(present))},
		{Hash: absentHash, SizeBytes: int64(len("absent"))},
	})
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{absentHash}, missing)
}

func TestChunk_ConfirmUploadsInvalidSize(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	_, err := uc.ConfirmUploads(ctx, []ChunkRef{{Hash: chunker.Sum([]byte("x")), SizeBytes: -1}})
	require400(t, err, "invalid chunk size")
}
