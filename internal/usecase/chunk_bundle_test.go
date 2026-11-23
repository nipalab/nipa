package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunkbundle"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// overwritingDirectStore stands in for S3 in proxy mode: a direct-transfer
// store with Overwrite. Exists and Put must not be used by a bundle upload.
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

	presign := func(t *testing.T, mode UploadMode, project *domain.Project) UploadPresign {
		t.Helper()
		nipa, byo := newOverwritingDirectStore(), newOverwritingDirectStore()
		stores := fakeChunkStores{projects: map[snow.ID]ProjectStore{
			testProject.ID: {Store: nipa},
			ownProject.ID:  {Store: byo, External: true},
		}}
		uc := NewChunk(newFakeChunkRepo(), nipa, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour, MaxPageSize: 10}).
			WithChunkStores(stores).WithUploadMode(mode)
		uc.now = func() time.Time { return time.Unix(1700000000, 0) }

		got, err := uc.PresignUploadURLs(context.Background(), testOrg, project, refs, 10, "")
		require.NoError(t, err)
		require.Len(t, got.URLs, 1)
		require.False(t, got.URLs[0].AlreadyStored)
		require.Zero(t, nipa.existsCalls+byo.existsCalls, "no HEAD for hashes without a row")
		if got.BundleURL != "" {
			verifyBundleURL(t, uc, testOrg.Slug, project.Slug, got, 1)
		}
		return got
	}

	t.Run("proxy mode sends the default store through the server in bundles", func(t *testing.T) {
		got := presign(t, UploadModeProxy, testProject)
		require.Empty(t, got.URLs[0].URL, "no per-chunk URL next to a bundle URL")
		require.Empty(t, got.URLs[0].Headers)
		require.True(t, strings.HasPrefix(got.BundleURL, "/api/chunk-bundles/acme/game?"), got.BundleURL)
	})
	t.Run("proxy mode keeps own storage direct", func(t *testing.T) {
		got := presign(t, UploadModeProxy, ownProject)
		require.Equal(t, "https://s3.example.com/put", got.URLs[0].URL)
		require.Equal(t, map[string]string{"If-None-Match": "*"}, got.URLs[0].Headers)
		require.Empty(t, got.BundleURL)
	})
	t.Run("direct mode is unchanged", func(t *testing.T) {
		got := presign(t, UploadModeDirect, testProject)
		require.Equal(t, "https://s3.example.com/put", got.URLs[0].URL)
		require.Empty(t, got.BundleURL)
	})
	t.Run("an unknown mode is direct", func(t *testing.T) {
		got := presign(t, UploadMode("sideways"), testProject)
		require.Equal(t, "https://s3.example.com/put", got.URLs[0].URL)
		require.Empty(t, got.BundleURL)
	})
}

func TestChunk_Presign_LocalStoreBundlesOnlyWhatNeedsUpload(t *testing.T) {
	ctx := context.Background()
	stored, fresh, other := []byte("stored"), []byte("fresh"), []byte("other")
	uc, repo, store := newChunkFixture(t)
	uc.WithBundleMax(1)
	require.NoError(t, store.Put(ctx, testProject.ID, chunker.Sum(stored), stored))
	repo.record(testProject.ID, chunker.Sum(stored), int64(len(stored)))

	refs := []ChunkRef{
		{Hash: chunker.Sum(stored), SizeBytes: int64(len(stored))},
		{Hash: chunker.Sum(fresh), SizeBytes: int64(len(fresh))},
		{Hash: chunker.Sum(other), SizeBytes: int64(len(other))},
	}
	got, err := uc.PresignUploadURLs(ctx, testOrg, testProject, refs, 0, "")
	require.NoError(t, err)
	require.Len(t, got.URLs, 2, "the fixture's page size is 2")
	require.Equal(t, "2", got.NextPageToken)
	require.True(t, got.URLs[0].AlreadyStored)
	require.Empty(t, got.URLs[1].URL)
	require.Equal(t, chunkbundle.EncodedSize(int(chunker.MaxChunkSize())), got.BundleMaxBytes, "never below one largest chunk")
	verifyBundleURL(t, uc, testOrg.Slug, testProject.Slug, got, 1)

	all, err := uc.PresignUploadURLs(ctx, testOrg, testProject, refs[:1], 0, "")
	require.NoError(t, err)
	require.Empty(t, all.BundleURL, "nothing to upload, no bundle URL")
}

func TestChunk_WithBundleMax(t *testing.T) {
	uc, _ := newLinkFixture(t, newStubChunkStore())
	require.Equal(t, int64(DefaultChunkBundleMaxBytes), uc.bundleMax)
	uc.WithBundleMax(128 << 20)
	require.Equal(t, int64(128<<20), uc.bundleMax)
	uc.WithBundleMax(0)
	require.Equal(t, int64(DefaultChunkBundleMaxBytes), uc.bundleMax)
}

// verifyBundleURL checks a presign's bundle URL carries its limits and
// verifies for the project.
func verifyBundleURL(t *testing.T, uc *Chunk, org, project string, got UploadPresign, count int) {
	t.Helper()
	u, err := url.Parse(got.BundleURL)
	require.NoError(t, err)
	params, err := chunkurl.ParseBundleParams(u.Query())
	require.NoError(t, err)
	require.Equal(t, got.BundleMaxBytes, params.MaxSize)
	require.Equal(t, count, params.MaxCount)
	require.Equal(t, count, got.BundleMaxChunks)
	require.Equal(t, uc.now().Add(uc.transfer.PresignTTL).Unix(), params.Exp)
	require.NoError(t, uc.VerifyBundleURL(org, project, params))
	require.Error(t, uc.VerifyBundleURL(org, "other", params))
}

// bundleOf encodes contents as a bundle body.
func bundleOf(t *testing.T, contents ...[]byte) []byte {
	t.Helper()
	entries := make([]chunkbundle.Entry, 0, len(contents))
	for _, data := range contents {
		entries = append(entries, chunkbundle.Entry{Hash: chunker.Sum(data), Data: data})
	}
	body, err := io.ReadAll(chunkbundle.NewEncoder(entries))
	require.NoError(t, err)
	return body
}

func storeBundle(uc *Chunk, body []byte) ([]ChunkReceipt, error) {
	return uc.StoreBundle(context.Background(), testProject.ID, bytes.NewReader(body), int64(len(body)), 100)
}

func TestChunk_StoreBundle_IssuesReceipts(t *testing.T) {
	a, b := []byte("bundle entry a"), []byte("bundle entry b")
	store := newStubChunkStore()
	uc, repo := newLinkFixture(t, store)
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }

	receipts, err := storeBundle(uc, bundleOf(t, a, b, a))
	require.NoError(t, err)
	require.Len(t, receipts, 2, "a repeated entry is stored and receipted once")
	require.Equal(t, 2, store.puts, "one store write per entry")
	require.Empty(t, repo.inserts, "no row before confirm")
	for i, data := range [][]byte{a, b} {
		hash := chunker.Sum(data)
		require.Equal(t, hash, receipts[i].Hash)
		require.Equal(t, data, store.data[ck(hash)])
		size, location, err := chunkproof.VerifyReceipt(testSigningKey, testProject.ID, hash, receipts[i].Receipt, uc.now().Add(time.Hour))
		require.NoError(t, err)
		require.Equal(t, int64(len(data)), size)
		require.Equal(t, chunkproof.Loose(), location)
		_, _, err = chunkproof.VerifyReceipt(testSigningKey, testProject.ID, hash, receipts[i].Receipt, uc.now().Add(time.Hour+time.Second))
		require.Error(t, err, "a receipt lives as long as a presigned URL")
	}
}

func TestChunk_StoreBundle_OverwritesWithoutExists(t *testing.T) {
	data := []byte("proxied payload")
	hash := chunker.Sum(data)
	store := newOverwritingDirectStore()
	store.data[ck(hash)] = []byte("garbage left by a direct upload")
	guard := &recordingGuard{}
	uc, _ := newLinkFixture(t, store)
	uc.WithGCGuard(guard)

	receipts, err := storeBundle(uc, bundleOf(t, data))
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, 1, store.overwrites)
	require.Zero(t, store.existsCalls, "a proxied write to S3 is one request")
	require.Zero(t, store.puts)
	require.Equal(t, data, store.data[ck(hash)], "garbage at the key is replaced")
	require.Equal(t, [][]domain.Hash{{hash}}, guard.protected, "the GC mark is cleared before the write")
}

func TestChunk_StoreBundle_RejectionsWriteNothing(t *testing.T) {
	good := bundleOf(t, []byte("first entry"), []byte("second entry"))
	tampered := bytes.Clone(good)
	tampered[len(tampered)-1] ^= 1

	cases := map[string]struct {
		body     []byte
		size     int64
		maxCount int
	}{
		"hash mismatch":    {tampered, int64(len(tampered)), 10},
		"truncated":        {good[:len(good)-3], int64(len(good)), 10},
		"trailing bytes":   {append(bytes.Clone(good), 1), int64(len(good)), 10},
		"too many entries": {good, int64(len(good)), 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := newOverwritingDirectStore()
			guard := &recordingGuard{}
			uc, _ := newLinkFixture(t, store)
			uc.WithGCGuard(guard)
			receipts, err := uc.StoreBundle(context.Background(), testProject.ID, bytes.NewReader(tc.body), tc.size, tc.maxCount)
			var derr *domain.Error
			require.ErrorAs(t, err, &derr)
			require.Equal(t, 400, derr.Code)
			require.Nil(t, receipts)
			require.Zero(t, store.overwrites+store.puts, "nothing is written")
			require.Empty(t, guard.protected)
		})
	}
	t.Run("read error", func(t *testing.T) {
		store := newOverwritingDirectStore()
		uc, _ := newLinkFixture(t, store)
		body := io.MultiReader(bytes.NewReader(good[:5]), iotest.ErrReader(errors.New("connection reset")))
		_, err := uc.StoreBundle(context.Background(), testProject.ID, body, int64(len(good)), 10)
		require.ErrorContains(t, err, "unable to read chunk bundle body")
		require.Zero(t, store.overwrites)
	})
}

func TestChunk_StoreBundle_Failures(t *testing.T) {
	body := bundleOf(t, []byte("proxied payload"))

	t.Run("overwrite error", func(t *testing.T) {
		store := newOverwritingDirectStore()
		store.overwriteErr = errors.New("s3 down")
		uc, _ := newLinkFixture(t, store)
		receipts, err := storeBundle(uc, body)
		require.ErrorContains(t, err, "database error")
		require.Empty(t, receipts)
	})
	t.Run("put error", func(t *testing.T) {
		store := newStubChunkStore()
		store.putErr = errors.New("disk full")
		uc, _ := newLinkFixture(t, store)
		_, err := storeBundle(uc, body)
		require.ErrorContains(t, err, "database error")
	})
	t.Run("gc guard error", func(t *testing.T) {
		store := newOverwritingDirectStore()
		uc, _ := newLinkFixture(t, store)
		uc.WithGCGuard(&recordingGuard{err: errors.New("db down")})
		receipts, err := storeBundle(uc, body)
		require.Error(t, err)
		require.Empty(t, receipts)
		require.Zero(t, store.overwrites, "nothing is written when the mark cannot be cleared")
	})
	t.Run("read-only store", func(t *testing.T) {
		store := newStubChunkStore()
		uc, _ := newLinkFixture(t, store)
		uc.WithChunkStores(fakeChunkStores{projects: map[snow.ID]ProjectStore{testProject.ID: {Store: store, ReadOnly: true}}})
		_, err := storeBundle(uc, body)
		require.Error(t, err)
		require.Zero(t, store.puts)
	})
}

func TestChunk_StoreChunks_SharedService(t *testing.T) {
	ctx := context.Background()
	a, b := []byte("merge output a"), []byte("merge output b")

	t.Run("overwriting store", func(t *testing.T) {
		store := newOverwritingDirectStore()
		guard := &recordingGuard{}
		uc, repo := newLinkFixture(t, store)
		uc.WithGCGuard(guard)
		require.NoError(t, uc.StoreChunks(ctx, testProject.ID, []ChunkContent{{chunker.Sum(a), a}, {chunker.Sum(b), b}}))
		require.Equal(t, 2, store.overwrites, "server content is written like a bundle entry")
		require.Zero(t, store.existsCalls)
		require.Equal(t, [][]domain.Hash{{chunker.Sum(a), chunker.Sum(b)}}, guard.protected, "one protect for the batch")
		require.Empty(t, repo.inserts)
	})
	t.Run("hash mismatch writes nothing", func(t *testing.T) {
		store := newStubChunkStore()
		uc, _ := newLinkFixture(t, store)
		err := uc.StoreChunks(ctx, testProject.ID, []ChunkContent{{chunker.Sum(a), a}, {chunker.Sum(a), b}})
		var derr *domain.Error
		require.ErrorAs(t, err, &derr)
		require.Equal(t, 400, derr.Code)
		require.Zero(t, store.puts)
	})
	t.Run("empty", func(t *testing.T) {
		uc, _ := newLinkFixture(t, newStubChunkStore())
		require.NoError(t, uc.StoreChunks(ctx, testProject.ID, nil))
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
	receipts, err := storeBundle(uc, bundleOf(t, data))
	require.NoError(t, err)
	receipt := receipts[0].Receipt

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
	receipts, err := storeBundle(uc, bundleOf(t, data))
	require.NoError(t, err)
	receipt := receipts[0].Receipt
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
		"other project": chunkproof.NewReceipt(testSigningKey, otherProject, hash, size, chunkproof.Loose(), now.Add(time.Hour)),
		"expired":       chunkproof.NewReceipt(testSigningKey, testProject.ID, hash, size, chunkproof.Loose(), now.Add(-time.Second)),
		"wrong key":     chunkproof.NewReceipt("other key", testProject.ID, hash, size, chunkproof.Loose(), now.Add(time.Hour)),
		"other hash":    chunkproof.NewReceipt(testSigningKey, testProject.ID, domain.Hash{1}, size, chunkproof.Loose(), now.Add(time.Hour)),
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
	receipts, err := storeBundle(uc, bundleOf(t, data))
	require.NoError(t, err)
	receipt := receipts[0].Receipt
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
