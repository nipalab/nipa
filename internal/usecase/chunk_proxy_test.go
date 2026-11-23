package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// overwritingDirectStore stands in for S3 in proxy mode: a direct-transfer
// store with Overwrite. Exists and Put must not be used by a proxied upload.
type overwritingDirectStore struct {
	*stubDirectChunkStore
	overwrites   int
	overwriteErr error
	existsCalls  int
}

func (s *overwritingDirectStore) Overwrite(_ context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	if s.overwriteErr != nil {
		return s.overwriteErr
	}
	s.overwrites++
	s.data[objectKey{projectID, hash}] = data
	return nil
}

func (s *overwritingDirectStore) Exists(ctx context.Context, projectID snow.ID, hash domain.Hash) (bool, error) {
	s.existsCalls++
	return s.stubDirectChunkStore.Exists(ctx, projectID, hash)
}

func newOverwritingDirectStore() *overwritingDirectStore {
	return &overwritingDirectStore{stubDirectChunkStore: &stubDirectChunkStore{
		stubChunkStore: newStubChunkStore(),
		uploadTarget:   storage.UploadTarget{URL: "https://s3.example.com/put", Headers: map[string]string{"If-None-Match": "*"}},
	}}
}

func TestChunk_Presign_UploadModes(t *testing.T) {
	data := []byte("fresh content")
	hash := chunker.Sum(data)
	refs := []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}
	ownProject := &domain.Project{ID: 2, Slug: "own"}

	presign := func(t *testing.T, mode UploadMode, project *domain.Project) ChunkURL {
		t.Helper()
		nipa, byo := newOverwritingDirectStore(), newOverwritingDirectStore()
		stores := fakeChunkStores{projects: map[snow.ID]ProjectStore{
			testProject.ID: {Store: nipa},
			ownProject.ID:  {Store: byo, External: true},
		}}
		uc := NewChunk(newFakeChunkRepo(), nipa, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour, MaxPageSize: 10}).
			WithChunkStores(stores).WithUploadMode(mode)
		uc.now = func() time.Time { return time.Unix(1700000000, 0) }

		urls, _, err := uc.PresignUploadURLs(context.Background(), testOrg, project, refs, 10, "")
		require.NoError(t, err)
		require.Len(t, urls, 1)
		require.False(t, urls[0].AlreadyStored)
		require.Zero(t, nipa.existsCalls+byo.existsCalls, "no HEAD for hashes without a row")
		if strings.HasPrefix(urls[0].URL, "/") {
			verifyChunkURL(t, uc, testOrg.Slug, project.Slug, urls[0].URL, chunkurl.OpUpload, int64(len(data)))
		}
		return urls[0]
	}

	t.Run("proxy mode sends the default store through the server", func(t *testing.T) {
		got := presign(t, UploadModeProxy, testProject)
		require.True(t, strings.HasPrefix(got.URL, "/api/chunks/"), got.URL)
		require.Empty(t, got.Headers)
	})
	t.Run("proxy mode keeps own storage direct", func(t *testing.T) {
		got := presign(t, UploadModeProxy, ownProject)
		require.Equal(t, "https://s3.example.com/put", got.URL)
		require.Equal(t, map[string]string{"If-None-Match": "*"}, got.Headers)
	})
	t.Run("direct mode is unchanged", func(t *testing.T) {
		got := presign(t, UploadModeDirect, testProject)
		require.Equal(t, "https://s3.example.com/put", got.URL)
	})
	t.Run("an unknown mode is direct", func(t *testing.T) {
		got := presign(t, UploadMode("sideways"), testProject)
		require.Equal(t, "https://s3.example.com/put", got.URL)
	})
}

func TestChunk_StoreUploaded_IssuesAReceipt(t *testing.T) {
	ctx := context.Background()
	data := []byte("proxied payload")
	hash := chunker.Sum(data)
	uc, _ := newLinkFixture(t, newStubChunkStore())
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }

	receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
	require.NoError(t, err)
	size, err := chunkproof.VerifyReceipt(testSigningKey, testProject.ID, hash, receipt, uc.now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), size)
	_, err = chunkproof.VerifyReceipt(testSigningKey, testProject.ID, hash, receipt, uc.now().Add(time.Hour+time.Second))
	require.Error(t, err, "a receipt lives as long as a presigned URL")

	// Content the local store already holds is not written again but is
	// still receipted: only the server writes that store.
	again, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
	require.NoError(t, err)
	require.NotEmpty(t, again)
}

func TestChunk_StoreUploaded_OverwritesWithoutExists(t *testing.T) {
	ctx := context.Background()
	data := []byte("proxied payload")
	hash := chunker.Sum(data)
	store := newOverwritingDirectStore()
	store.data[ck(hash)] = []byte("garbage left by a direct upload")
	guard := &recordingGuard{}
	uc, _ := newLinkFixture(t, store)
	uc.WithGCGuard(guard)

	receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
	require.NoError(t, err)
	require.NotEmpty(t, receipt)
	require.Equal(t, 1, store.overwrites)
	require.Zero(t, store.existsCalls, "a proxied write to S3 is one request")
	require.Zero(t, store.puts)
	require.Equal(t, data, store.data[ck(hash)], "garbage at the key is replaced")
	require.Equal(t, [][]domain.Hash{{hash}}, guard.protected, "the GC mark is cleared before the write")
}

func TestChunk_StoreUploaded_FailuresIssueNoReceipt(t *testing.T) {
	ctx := context.Background()
	data := []byte("proxied payload")
	hash := chunker.Sum(data)

	t.Run("hash mismatch", func(t *testing.T) {
		store := newOverwritingDirectStore()
		uc, _ := newLinkFixture(t, store)
		receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, []byte("other payload!!"))
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		require.Equal(t, 400, derr.Code)
		require.Empty(t, receipt)
		require.Zero(t, store.overwrites)
	})
	t.Run("overwrite error", func(t *testing.T) {
		store := newOverwritingDirectStore()
		store.overwriteErr = errors.New("s3 down")
		uc, _ := newLinkFixture(t, store)
		receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
		require.ErrorContains(t, err, "database error")
		require.Empty(t, receipt)
	})
	t.Run("gc guard error", func(t *testing.T) {
		store := newOverwritingDirectStore()
		uc, _ := newLinkFixture(t, store)
		uc.WithGCGuard(&recordingGuard{err: errors.New("db down")})
		receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
		require.Error(t, err)
		require.Empty(t, receipt)
		require.Zero(t, store.overwrites, "nothing is written when the mark cannot be cleared")
	})
}

func TestChunk_ReserveUploadBuffer(t *testing.T) {
	uc, _ := newLinkFixture(t, newStubChunkStore())
	uc.WithProxyBuffer(100)

	first, err := uc.ReserveUploadBuffer(context.Background(), 60)
	require.NoError(t, err)
	second, err := uc.ReserveUploadBuffer(context.Background(), 40)
	require.NoError(t, err)

	// The budget is spent: a third upload waits and gives up with its request.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = uc.ReserveUploadBuffer(ctx, 1)
	require.Error(t, err)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 503, derr.Code)

	// Releasing twice gives back the bytes once.
	first()
	first()
	require.False(t, uc.proxyBuffer.TryAcquire(61))
	require.True(t, uc.proxyBuffer.TryAcquire(60))
	uc.proxyBuffer.Release(60)
	second()

	// A body larger than the whole budget takes all of it instead of
	// waiting forever.
	all, err := uc.ReserveUploadBuffer(context.Background(), 1000)
	require.NoError(t, err)
	require.False(t, uc.proxyBuffer.TryAcquire(1))
	all()
	require.True(t, uc.proxyBuffer.TryAcquire(100))
}

func TestChunk_WithProxyBufferDefaults(t *testing.T) {
	uc, _ := newLinkFixture(t, newStubChunkStore())
	require.Equal(t, int64(DefaultChunkProxyBufferBytes), uc.proxyBufferSize)
	uc.WithProxyBuffer(-1)
	require.Equal(t, int64(DefaultChunkProxyBufferBytes), uc.proxyBufferSize)
	uc.WithProxyBuffer(5)
	require.Equal(t, int64(5), uc.proxyBufferSize)
}

// receiptFixture is a direct store that counts reads, as S3 in proxy mode.
func receiptFixture(t *testing.T) (*Chunk, *fakeChunkRepo, *openingDirectStore) {
	t.Helper()
	store := &openingDirectStore{stubDirectChunkStore: &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}}
	uc, repo := newLinkFixture(t, store)
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }
	return uc, repo, store
}

func TestChunk_ConfirmUploads_ValidReceiptSkipsTheReadBack(t *testing.T) {
	ctx := context.Background()
	data := []byte("proxied payload")
	hash := chunker.Sum(data)
	uc, repo, store := receiptFixture(t)
	receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
	require.NoError(t, err)

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkReceipt{{Hash: hash, Receipt: receipt}})
	require.NoError(t, err)
	require.Empty(t, missing)
	require.Zero(t, store.readCount(), "the server saw the bytes itself; nothing is read back")
	require.Equal(t, [][]ChunkRef{{{Hash: hash, SizeBytes: int64(len(data))}}}, repo.inserts, "the receipt's size is recorded")
}

func TestChunk_ConfirmUploads_ReceiptForARecordedChunkIsIgnored(t *testing.T) {
	ctx := context.Background()
	data := []byte("proxied payload")
	hash := chunker.Sum(data)
	uc, repo, store := receiptFixture(t)
	receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
	require.NoError(t, err)
	repo.record(testProject.ID, hash, int64(len(data)))

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkReceipt{{Hash: hash, Receipt: receipt}})
	require.NoError(t, err)
	require.Empty(t, missing)
	require.Zero(t, store.readCount())
	require.Empty(t, repo.inserts, "the row already exists")
}

func TestChunk_ConfirmUploads_InvalidReceiptFallsBackToTheReadBack(t *testing.T) {
	data := []byte("proxied payload")
	hash := chunker.Sum(data)
	now := time.Unix(1700000000, 0)
	size := int64(len(data))
	otherProject := snow.ID(99)

	receipts := map[string]string{
		"other project": chunkproof.NewReceipt(testSigningKey, otherProject, hash, size, now.Add(time.Hour)),
		"expired":       chunkproof.NewReceipt(testSigningKey, testProject.ID, hash, size, now.Add(-time.Second)),
		"wrong key":     chunkproof.NewReceipt("other key", testProject.ID, hash, size, now.Add(time.Hour)),
		"other hash":    chunkproof.NewReceipt(testSigningKey, testProject.ID, domain.Hash{1}, size, now.Add(time.Hour)),
		"garbage":       "not-a-receipt",
		"empty":         "",
	}
	for name, receipt := range receipts {
		t.Run(name+" with the object is read back", func(t *testing.T) {
			ctx := context.Background()
			uc, repo, store := receiptFixture(t)
			require.NoError(t, store.put(hash, data))

			missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkReceipt{{Hash: hash, Receipt: receipt}})
			require.NoError(t, err)
			require.Empty(t, missing)
			require.Equal(t, int64(1), store.readCount())
			require.True(t, repo.has(testProject.ID, hash))
		})
		t.Run(name+" without the object is missing", func(t *testing.T) {
			ctx := context.Background()
			uc, repo, store := receiptFixture(t)

			missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkReceipt{{Hash: hash, Receipt: receipt}})
			require.NoError(t, err)
			require.Equal(t, []domain.Hash{hash}, missing)
			require.Equal(t, int64(1), store.readCount())
			require.Empty(t, repo.inserts)
		})
	}
}

func TestChunk_ConfirmUploads_ReceiptNeverSkipsTheQuota(t *testing.T) {
	ctx := context.Background()
	data := []byte("proxied payload")
	hash := chunker.Sum(data)
	uc, repo, store := receiptFixture(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc.WithStorageLedger(ledger)
	receipt, err := uc.StoreUploaded(ctx, testProject.ID, hash, data)
	require.NoError(t, err)
	quota := domain.NewErrorQuotaExceeded("over quota")

	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject, []LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}}).Return(quota)

	_, err = uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, []ChunkReceipt{{Hash: hash, Receipt: receipt}})
	require.ErrorIs(t, err, quota)
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Equal(t, 402, derr.Code)
	require.Zero(t, store.readCount())
	require.Empty(t, repo.inserts, "no row without the quota")
}
