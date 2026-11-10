package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

var (
	testOrg     = &domain.Organization{ID: 1, Slug: "acme"}
	testProject = &domain.Project{ID: 1, Slug: "game"}
)

type stubChunkStore struct {
	data      map[domain.Hash][]byte
	existsErr error
	putErr    error
	getErr    error
	puts      int
	gets      int
}

func newStubChunkStore() *stubChunkStore {
	return &stubChunkStore{data: map[domain.Hash][]byte{}}
}

func (s *stubChunkStore) Put(_ context.Context, hash domain.Hash, data []byte) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.puts++
	s.data[hash] = data
	return nil
}

func (s *stubChunkStore) Get(_ context.Context, hash domain.Hash) ([]byte, error) {
	s.gets++
	if s.getErr != nil {
		return nil, s.getErr
	}
	data, ok := s.data[hash]
	if !ok {
		return nil, domain.NewErrorNotFound("chunk not found")
	}
	return data, nil
}

func (s *stubChunkStore) Size(_ context.Context, hash domain.Hash) (int64, error) {
	data, ok := s.data[hash]
	if !ok {
		return 0, domain.NewErrorNotFound("chunk not found")
	}
	return int64(len(data)), nil
}

func (s *stubChunkStore) Exists(_ context.Context, hash domain.Hash) (bool, error) {
	if s.existsErr != nil {
		return false, s.existsErr
	}
	_, ok := s.data[hash]
	return ok, nil
}

func (s *stubChunkStore) Close() error { return nil }

type stubDirectChunkStore struct {
	*stubChunkStore
	downloadURL string
	downloadErr error
	ttl         time.Duration

	uploadTarget storage.UploadTarget
	uploadErr    error
	uploadSize   int64
	uploadTTL    time.Duration

	deleted   []domain.Hash
	deleteErr error
}

func (s *stubDirectChunkStore) PresignDownload(_ context.Context, _ domain.Hash, expires time.Duration) (string, error) {
	if s.downloadErr != nil {
		return "", s.downloadErr
	}
	s.ttl = expires
	return s.downloadURL, nil
}

func (s *stubDirectChunkStore) PresignUpload(_ context.Context, _ domain.Hash, size int64, expires time.Duration) (storage.UploadTarget, error) {
	s.uploadSize = size
	s.uploadTTL = expires
	if s.uploadErr != nil {
		return storage.UploadTarget{}, s.uploadErr
	}
	return s.uploadTarget, nil
}

func (s *stubDirectChunkStore) DeleteChunk(_ context.Context, hash domain.Hash) error {
	s.deleted = append(s.deleted, hash)
	delete(s.data, hash)
	return s.deleteErr
}

func linkEverything(repo *MockchunkRepository, store storage.ChunkStore) {
	repo.EXPECT().LinkProjectChunk(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	repo.EXPECT().LinkProjectChunks(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _ snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error) {
			linked := make(map[domain.Hash]int64, len(hashes))
			for _, hash := range hashes {
				if size, err := store.Size(ctx, hash); err == nil {
					linked[hash] = size
				}
			}
			return linked, nil
		}).AnyTimes()
}

func newChunkFixture(t *testing.T) (*Chunk, *MockchunkRepository, *storage.LocalStore) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockchunkRepository(ctrl)
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	linkEverything(repo, store)
	uc := NewChunk(repo, store, ChunkTransferConfig{
		SigningKey:  "test-signing-key",
		PresignTTL:  time.Hour,
		MaxPageSize: 2,
	})
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }
	return uc, repo, store
}

func newDirectChunkFixture(t *testing.T, store storage.ChunkStore) (*Chunk, *MockchunkRepository) {
	t.Helper()
	repo := NewMockchunkRepository(gomock.NewController(t))
	linkEverything(repo, store)
	uc := NewChunk(repo, store, ChunkTransferConfig{
		SigningKey:  "test-signing-key",
		PresignTTL:  time.Hour,
		MaxPageSize: 10,
	})
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }
	return uc, repo
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
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil).Times(2)

	first, err := uc.Upload(ctx, hash, data)
	require.NoError(t, err)
	require.True(t, first)

	second, err := uc.Upload(ctx, hash, data)
	require.NoError(t, err)
	require.False(t, second)
}

func TestChunk_UploadDedupRecordsMetadata(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)

	data := []byte("stored without a metadata row")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, hash, data))

	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

	isNew, err := uc.Upload(ctx, hash, data)
	require.NoError(t, err)
	require.False(t, isNew)
}

func TestChunk_UploadDedupMetadataError(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)

	data := []byte("stored without a metadata row")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, hash, data))

	wantErr := errors.New("insert failed")
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(wantErr)

	_, err := uc.Upload(ctx, hash, data)
	require.ErrorIs(t, err, wantErr)
}

func TestChunk_AttributeProjectChunks(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc = uc.WithStorageLedger(ledger)

	require.NoError(t, uc.AttributeProjectChunks(ctx, snow.ID(1), nil), "nothing to attribute")

	chunks := []LedgerChunk{{Hash: domain.Hash{1}, SizeBytes: 1}}
	ledger.EXPECT().AttributeProjectChunks(ctx, snow.ID(1), chunks).Return(nil)
	require.NoError(t, uc.AttributeProjectChunks(ctx, snow.ID(1), chunks))
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

	urls, next, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{
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

	page1, next, err := uc.PresignUploadURLs(ctx, testOrg, testProject, refs, 10, "")
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, "2", next)
	require.Equal(t, refs[0].Hash, page1[0].Hash)
	require.Equal(t, refs[1].Hash, page1[1].Hash)

	page2, next2, err := uc.PresignUploadURLs(ctx, testOrg, testProject, refs, 10, next)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Empty(t, next2)
	require.Equal(t, refs[2].Hash, page2[0].Hash)
}

func TestChunk_PresignUploadURLsInvalid(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)
	hash := chunker.Sum([]byte("x"))

	_, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 0}}, 10, "")
	require400(t, err, "invalid chunk size")

	_, _, err = uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: chunker.MaxChunkSize() + 1}}, 10, "")
	require400(t, err, "invalid chunk size")

	urls, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: chunker.PackedAssetConfig.Max}}, 10, "")
	require.NoError(t, err)
	require.Len(t, urls, 1)

	_, _, err = uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 10}}, 10, "-1")
	require400(t, err, "invalid page token")
}

func TestChunk_PresignDownloadURLs(t *testing.T) {
	uc, _, _ := newChunkFixture(t)
	h1 := chunker.Sum([]byte("one"))
	h2 := chunker.Sum([]byte("two"))

	urls, next, err := uc.PresignDownloadURLs(context.Background(), "acme", "game", []domain.Hash{h1, h2}, 10, "")
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, urls, 2)
	require.Equal(t, h1, urls[0].Hash)
	require.Equal(t, h2, urls[1].Hash)
	require.False(t, urls[0].AlreadyStored)
	verifyChunkURL(t, uc, "acme", "game", urls[0].URL, chunkurl.OpDownload, 0)
	verifyChunkURL(t, uc, "acme", "game", urls[1].URL, chunkurl.OpDownload, 0)
}

func TestChunk_PresignDownloadURLsDirectStore(t *testing.T) {
	store := &stubDirectChunkStore{
		stubChunkStore: newStubChunkStore(),
		downloadURL:    "https://s3.example.com/nipa-chunks/ab/cdef?X-Amz-Signature=sig",
	}
	uc, _ := newDirectChunkFixture(t, store)

	h1 := chunker.Sum([]byte("one"))
	h2 := chunker.Sum([]byte("two"))
	urls, next, err := uc.PresignDownloadURLs(context.Background(), "acme", "game", []domain.Hash{h1, h2}, 10, "")
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, urls, 2)
	require.Equal(t, h1, urls[0].Hash)
	require.Equal(t, store.downloadURL, urls[0].URL)
	require.Equal(t, store.downloadURL, urls[1].URL)
	require.Equal(t, time.Hour, store.ttl)
}

func TestChunk_PresignDownloadURLsDirectStoreError(t *testing.T) {
	store := &stubDirectChunkStore{
		stubChunkStore: newStubChunkStore(),
		downloadErr:    errors.New("s3 down"),
	}
	uc, _ := newDirectChunkFixture(t, store)

	_, _, err := uc.PresignDownloadURLs(context.Background(), "acme", "game", []domain.Hash{chunker.Sum([]byte("x"))}, 10, "")
	require.Error(t, err)
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, 500, domainErr.Code)
}

func TestChunk_PresignUploadURLsDirectStore(t *testing.T) {
	store := &stubDirectChunkStore{
		stubChunkStore: newStubChunkStore(),
		uploadTarget: storage.UploadTarget{
			URL:     "https://s3.example.com/nipa-chunks",
			Headers: map[string]string{"If-None-Match": "*"},
		},
	}
	uc, _ := newDirectChunkFixture(t, store)

	data := []byte("payload")
	hash := chunker.Sum(data)
	urls, next, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, urls, 1)
	require.Equal(t, hash, urls[0].Hash)
	require.Equal(t, store.uploadTarget.URL, urls[0].URL)
	require.Equal(t, store.uploadTarget.Headers, urls[0].Headers)
	require.Equal(t, int64(len(data)), store.uploadSize)
	require.Equal(t, time.Hour, store.uploadTTL)
	require.NotEmpty(t, urls[0].ProofChallenge)
}

func TestChunk_PresignUploadURLsDirectStoreAlreadyStored(t *testing.T) {
	data := []byte("already here")
	store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
	store.data[chunker.Sum(data)] = data
	uc, _ := newDirectChunkFixture(t, store)

	urls, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 10, "")
	require.NoError(t, err)
	require.Len(t, urls, 1)
	require.True(t, urls[0].AlreadyStored)
	require.Empty(t, urls[0].URL)
	require.Zero(t, store.uploadSize)
}

func TestChunk_PresignUploadURLsDirectStoreError(t *testing.T) {
	store := &stubDirectChunkStore{
		stubChunkStore: newStubChunkStore(),
		uploadErr:      errors.New("s3 down"),
	}
	uc, _ := newDirectChunkFixture(t, store)

	data := []byte("payload")
	_, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 10, "")
	require.Error(t, err)
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, 500, domainErr.Code)
}

func TestChunk_ConfirmUploads(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)

	present := []byte("present")
	presentHash := chunker.Sum(present)
	require.NoError(t, store.Put(ctx, presentHash, present))

	absentHash := chunker.Sum([]byte("absent"))
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{presentHash, absentHash}, nil)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{absentHash}, missing)
}

func TestChunk_UploadStoreErrors(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("exists", func(t *testing.T) {
		store := &stubChunkStore{data: map[domain.Hash][]byte{}, existsErr: errors.New("boom")}
		uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), store, ChunkTransferConfig{})
		_, err := uc.Upload(ctx, hash, data)
		require.Error(t, err)
	})

	t.Run("put", func(t *testing.T) {
		store := &stubChunkStore{data: map[domain.Hash][]byte{}, putErr: errors.New("boom")}
		uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), store, ChunkTransferConfig{})
		_, err := uc.Upload(ctx, hash, data)
		require.Error(t, err)
	})

	t.Run("metadata", func(t *testing.T) {
		repo := NewMockchunkRepository(gomock.NewController(t))
		repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(errors.New("boom"))
		uc := NewChunk(repo, newStubChunkStore(), ChunkTransferConfig{})
		_, err := uc.Upload(ctx, hash, data)
		require.Error(t, err)
	})
}

func TestChunk_StoreUploaded(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	repo := NewMockchunkRepository(gomock.NewController(t))
	uc := NewChunk(repo, store, ChunkTransferConfig{})

	data := []byte("payload")
	hash := chunker.Sum(data)

	require.NoError(t, uc.StoreUploaded(ctx, hash, data))
	require.Equal(t, data, store.data[hash])
	require.Equal(t, 1, store.puts)

	require.NoError(t, uc.StoreUploaded(ctx, hash, data))
	require.Equal(t, 1, store.puts, "stored content must not be written twice")

	err := uc.StoreUploaded(ctx, hash, []byte("other bytes"))
	require400(t, err, "chunk hash mismatch")
}

func TestChunk_StoreUploadedErrors(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("exists", func(t *testing.T) {
		store := &stubChunkStore{data: map[domain.Hash][]byte{}, existsErr: errors.New("boom")}
		uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), store, ChunkTransferConfig{})
		require.Error(t, uc.StoreUploaded(ctx, hash, data))
	})

	t.Run("put", func(t *testing.T) {
		store := &stubChunkStore{data: map[domain.Hash][]byte{}, putErr: errors.New("boom")}
		uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), store, ChunkTransferConfig{})
		require.Error(t, uc.StoreUploaded(ctx, hash, data))
	})
}

func TestChunk_VerifyTransferURL(t *testing.T) {
	uc, _, _ := newChunkFixture(t)

	data := []byte("payload")
	hash := chunker.Sum(data)
	expiry := uc.now().Add(uc.transfer.PresignTTL).Unix()
	path := chunkurl.UploadPath(uc.transfer.SigningKey, "acme", "game", hash.String(), int64(len(data)), expiry)

	u, err := url.Parse(path)
	require.NoError(t, err)
	params, err := chunkurl.ParseParams(u.Query())
	require.NoError(t, err)

	require.NoError(t, uc.VerifyTransferURL("acme", "game", hash.String(), params))

	params.Sig = strings.Repeat("0", 64)
	require.Error(t, uc.VerifyTransferURL("acme", "game", hash.String(), params))
	require.True(t, domain.IsErrorNoPermission(uc.VerifyTransferURL("acme", "game", hash.String(), params)))
}

func TestChunk_PresignUploadURLsStoreError(t *testing.T) {
	ctx := context.Background()
	store := &stubChunkStore{data: map[domain.Hash][]byte{}, existsErr: errors.New("boom")}
	repo := NewMockchunkRepository(gomock.NewController(t))
	linkEverything(repo, store)
	uc := NewChunk(repo, store, ChunkTransferConfig{
		SigningKey:  "test-signing-key",
		PresignTTL:  time.Hour,
		MaxPageSize: 10,
	})

	data := []byte("payload")
	_, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 10, "")
	require.Error(t, err)
}

func TestChunk_PresignDownloadURLsInvalidToken(t *testing.T) {
	uc, _, _ := newChunkFixture(t)

	_, _, err := uc.PresignDownloadURLs(context.Background(), "acme", "game", []domain.Hash{chunker.Sum([]byte("x"))}, 10, "bad")
	require400(t, err, "invalid page token")
}

func TestChunk_ConfirmUploadsMetadataError(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	store := newStubChunkStore()
	store.data[hash] = data
	repo := NewMockchunkRepository(gomock.NewController(t))
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]int64{}, nil)
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(errors.New("boom"))
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour})

	_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
	require.EqualError(t, err, "boom")
}

func TestPaginate(t *testing.T) {
	items := []int{1, 2, 3}

	page, next, err := paginate(items, 10, "", 0)
	require.NoError(t, err)
	require.Equal(t, items, page)
	require.Empty(t, next)

	page, next, err = paginate(items, 0, "", 0)
	require.NoError(t, err)
	require.Equal(t, items, page)
	require.Empty(t, next)

	page, next, err = paginate(items, 10, "3", 10)
	require.NoError(t, err)
	require.Empty(t, page)
	require.Empty(t, next)
}

func TestChunk_PresignUploadURLsLedgerHeadroomRejects(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc = uc.WithStorageLedger(ledger)

	data := []byte("new bytes")
	hash := chunker.Sum(data)
	quotaErr := domain.NewErrorQuotaExceeded("organization storage quota exceeded")

	ledger.EXPECT().AttributedSizes(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil, nil)
	ledger.EXPECT().Headroom(gomock.Any(), testOrg, testProject, int64(len(data))).Return(quotaErr)

	_, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	require.ErrorIs(t, err, quotaErr)
}

func TestChunk_PresignUploadURLsLedgerSkipsAttributedRefs(t *testing.T) {
	ctx := context.Background()
	uc, _, store := newChunkFixture(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc = uc.WithStorageLedger(ledger)

	data := []byte("known bytes")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, hash, data))

	ledger.EXPECT().AttributedSizes(gomock.Any(), testProject.ID, []domain.Hash{hash}).
		Return(map[domain.Hash]int64{hash: int64(len(data))}, nil)

	urls, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	require.NoError(t, err)
	require.Len(t, urls, 1)
	require.True(t, urls[0].AlreadyStored)
}

func TestChunk_PresignUploadURLsLedgerReadError(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc = uc.WithStorageLedger(ledger)

	hash := chunker.Sum([]byte("read error"))
	wantErr := errors.New("ledger down")
	ledger.EXPECT().AttributedSizes(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil, wantErr)

	_, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 4}}, 10, "")
	require.ErrorIs(t, err, wantErr)
}

func TestChunk_ConfirmUploadsLedgerAttributesPresentChunks(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	data := []byte("attribute me")
	hash := chunker.Sum(data)
	store.data[hash] = data
	absentHash := chunker.Sum([]byte("absent"))

	repo := NewMockchunkRepository(gomock.NewController(t))
	linkEverything(repo, store)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject,
		[]LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}}).Return(nil)
	uc := NewChunk(repo, store, ChunkTransferConfig{}).WithStorageLedger(ledger)

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash, absentHash}, nil)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{absentHash}, missing)
}

func TestChunk_ConfirmUploadsLedgerQuotaError(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	data := []byte("over quota")
	hash := chunker.Sum(data)
	store.data[hash] = data

	repo := NewMockchunkRepository(gomock.NewController(t))
	linkEverything(repo, store)
	quotaErr := domain.NewErrorQuotaExceeded("organization storage quota exceeded")
	ledger := NewMockStorageLedger(gomock.NewController(t))
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject,
		[]LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}}).Return(quotaErr)
	uc := NewChunk(repo, store, ChunkTransferConfig{}).WithStorageLedger(ledger)

	_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
	require.ErrorIs(t, err, quotaErr)
}

type countingChunkStore struct {
	*stubChunkStore
	existsCalls atomic.Int64
	sizeCalls   atomic.Int64
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	existsDelay time.Duration
}

func (s *countingChunkStore) Exists(ctx context.Context, hash domain.Hash) (bool, error) {
	s.existsCalls.Add(1)
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		peak := s.maxInFlight.Load()
		if n <= peak || s.maxInFlight.CompareAndSwap(peak, n) {
			break
		}
	}
	time.Sleep(s.existsDelay)
	return s.stubChunkStore.Exists(ctx, hash)
}

func (s *countingChunkStore) Size(ctx context.Context, hash domain.Hash) (int64, error) {
	s.sizeCalls.Add(1)
	return s.stubChunkStore.Size(ctx, hash)
}

func TestChunk_PresignUploadURLs_ChecksTheStoreConcurrently(t *testing.T) {
	prev := presignExistsConcurrency
	presignExistsConcurrency = 4
	t.Cleanup(func() { presignExistsConcurrency = prev })

	store := &countingChunkStore{stubChunkStore: newStubChunkStore(), existsDelay: 2 * time.Millisecond}
	repo := NewMockchunkRepository(gomock.NewController(t))
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: "k", PresignTTL: time.Hour, MaxPageSize: 100})

	refs := make([]ChunkRef, 0, 60)
	linked := map[domain.Hash]int64{}
	for i := 0; i < 60; i++ {
		data := []byte(fmt.Sprintf("chunk-%d", i))
		hash := chunker.Sum(data)
		refs = append(refs, ChunkRef{Hash: hash, SizeBytes: int64(len(data))})
		switch i % 4 {
		case 0:
			linked[hash] = int64(len(data))
			store.data[hash] = data
		case 1:
			linked[hash] = int64(len(data))
		case 2:
			store.data[hash] = data
		}
	}
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, gomock.Any()).Return(linked, nil)

	urls, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, refs, 100, "")
	require.NoError(t, err)
	require.Len(t, urls, len(refs))
	require.Equal(t, int64(len(refs)), store.existsCalls.Load())
	require.LessOrEqual(t, store.maxInFlight.Load(), int64(4))
	require.Greater(t, store.maxInFlight.Load(), int64(1))
	for i, u := range urls {
		require.Equal(t, refs[i].Hash, u.Hash)
		switch i % 4 {
		case 0:
			require.True(t, u.AlreadyStored)
			require.Empty(t, u.ProofChallenge)
		case 2:
			require.False(t, u.AlreadyStored)
			require.Empty(t, u.URL)
			require.NotEmpty(t, u.ProofChallenge)
		default:
			require.False(t, u.AlreadyStored, "a linked chunk whose object is gone must be uploaded again")
			require.NotEmpty(t, u.URL)
			require.NotEmpty(t, u.ProofChallenge)
		}
	}
}

func TestChunk_PresignUploadURLs_ExistsErrorIsADatabaseError(t *testing.T) {
	store := &stubChunkStore{data: map[domain.Hash][]byte{}, existsErr: errors.New("boom")}
	repo := NewMockchunkRepository(gomock.NewController(t))
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]int64{}, nil)
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: "k", PresignTTL: time.Hour, MaxPageSize: 10})

	refs := []ChunkRef{{Hash: chunker.Sum([]byte("a")), SizeBytes: 1}, {Hash: chunker.Sum([]byte("b")), SizeBytes: 1}}
	_, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, refs, 10, "")
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, domain.NewErrorDatabase("").Code, domainErr.Code)
	require.Contains(t, domainErr.InternalMessage, "boom")
}

func TestChunk_ConfirmUploads_LinkedChunksUseStoredSizes(t *testing.T) {
	ctx := context.Background()
	linkedData := []byte("linked content")
	linkedHash := chunker.Sum(linkedData)
	freshData := []byte("fresh content")
	freshHash := chunker.Sum(freshData)
	absentHash := chunker.Sum([]byte("absent"))

	store := &countingChunkStore{stubChunkStore: newStubChunkStore()}
	store.data[freshHash] = freshData
	repo := NewMockchunkRepository(gomock.NewController(t))
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour}).WithStorageLedger(ledger)
	metrics := obs.NewMetrics()
	uc = uc.WithMetrics(metrics)

	const storedSize = 1234
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, []domain.Hash{linkedHash, freshHash, absentHash}).
		Return(map[domain.Hash]int64{linkedHash: storedSize}, nil)
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), freshHash, int64(len(freshData))).Return(nil)
	repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{freshHash}).Return(nil)
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject, []LedgerChunk{
		{Hash: linkedHash, SizeBytes: storedSize},
		{Hash: freshHash, SizeBytes: int64(len(freshData))},
	}).Return(nil)

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{linkedHash, freshHash, absentHash},
		[]ChunkProof{proofFor(t, uc, freshHash, freshData)})
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{absentHash}, missing)
	require.Zero(t, store.sizeCalls.Load())
	require.Contains(t, scrapeUsecaseMetrics(t, metrics), fmt.Sprintf(`nipa_chunk_bytes_total{op="confirm"} %d`, storedSize+len(freshData)))
}

type gettingChunkStore struct {
	*stubChunkStore
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
}

func (s *gettingChunkStore) Get(_ context.Context, hash domain.Hash) ([]byte, error) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		peak := s.maxInFlight.Load()
		if n <= peak || s.maxInFlight.CompareAndSwap(peak, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	return s.data[hash], nil
}

func TestChunk_ConfirmUploads_BoundsConcurrentVerificationReads(t *testing.T) {
	prev := confirmVerifyConcurrency
	confirmVerifyConcurrency = 3
	t.Cleanup(func() { confirmVerifyConcurrency = prev })

	store := &gettingChunkStore{stubChunkStore: newStubChunkStore()}
	hashes := make([]domain.Hash, 0, 30)
	for i := 0; i < 30; i++ {
		data := []byte(fmt.Sprintf("verify-%d", i))
		hash := chunker.Sum(data)
		store.data[hash] = data
		hashes = append(hashes, hash)
	}
	repo := NewMockchunkRepository(gomock.NewController(t))
	linkEverything(repo, store)
	repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour})

	proofs := make([]ChunkProof, 0, len(hashes))
	for _, hash := range hashes {
		proofs = append(proofs, proofFor(t, uc, hash, store.data[hash]))
	}
	missing, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, hashes, proofs)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.LessOrEqual(t, store.maxInFlight.Load(), int64(3))
	require.Greater(t, store.maxInFlight.Load(), int64(1))
}
