package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// readCounter counts every read of chunk content, whichever path takes it.
type readCounter struct {
	reads atomic.Int64
}

// openingLocalStore is the free local store with read counting; it streams
// through storage.ChunkOpener.
type openingLocalStore struct {
	*storage.LocalStore
	readCounter
}

func (s *openingLocalStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	return s.LocalStore.Open(ctx, projectID, hash)
}

func (s *openingLocalStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.LocalStore.Get(ctx, projectID, hash)
}

func (s *openingLocalStore) put(hash domain.Hash, data []byte) error {
	return s.Put(context.Background(), testProject.ID, hash, data)
}

// openingDirectStore stands in for the S3 store: direct transfers (garbage
// is deleted) and streamed reads.
type openingDirectStore struct {
	*stubDirectChunkStore
	readCounter
}

func (s *openingDirectStore) Open(_ context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.data[objectKey{projectID, hash}]
	if !ok {
		return nil, domain.NewErrorNotFound("chunk not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *openingDirectStore) Get(context.Context, snow.ID, domain.Hash) ([]byte, error) {
	panic("a store with Open must be streamed, not read whole")
}

func (s *openingDirectStore) put(hash domain.Hash, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[ck(hash)] = data
	return nil
}

// gettingDirectStore is a direct store without Open: confirm falls back to Get.
type gettingDirectStore struct {
	*stubDirectChunkStore
	readCounter
}

func (s *gettingDirectStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.stubDirectChunkStore.Get(ctx, projectID, hash)
}

func (s *gettingDirectStore) put(hash domain.Hash, data []byte) error {
	s.data[ck(hash)] = data
	return nil
}

type readbackStore interface {
	storage.ChunkStore
	put(hash domain.Hash, data []byte) error
	readCount() int64
	deletedHashes() []domain.Hash
}

func (c *readCounter) readCount() int64 { return c.reads.Load() }

func (s *openingLocalStore) deletedHashes() []domain.Hash  { return nil }
func (s *openingDirectStore) deletedHashes() []domain.Hash { return s.deleted }
func (s *gettingDirectStore) deletedHashes() []domain.Hash { return s.deleted }

// TestChunk_ConfirmUploads_ReadBackDecisionTable runs every confirm decision
// for a chunk without a row (direct uploads) against a streaming local store,
// a streaming direct store and a direct store without streaming, so the
// streamed read-back decides exactly as the whole-object read did: the plain
// BLAKE3 of the stored bytes must match the hash.
func TestChunk_ConfirmUploads_ReadBackDecisionTable(t *testing.T) {
	stores := map[string]func(t *testing.T) readbackStore{
		"local (streamed)": func(t *testing.T) readbackStore {
			local, err := storage.NewLocalStore(t.TempDir())
			require.NoError(t, err)
			return &openingLocalStore{LocalStore: local}
		},
		"direct (streamed)": func(*testing.T) readbackStore {
			return &openingDirectStore{stubDirectChunkStore: &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}}
		},
		"direct (Get fallback)": func(*testing.T) readbackStore {
			return &gettingDirectStore{stubDirectChunkStore: &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}}
		},
	}

	data := []byte("chunk content")
	hash := chunker.Sum(data)
	oversized := bytes.Repeat([]byte{7}, int(chunker.MaxChunkSize())+1)
	oversizedHash := chunker.Sum(oversized)

	type outcome struct {
		recorded bool
		size     int64
		deleted  bool // only stores that support direct transfers delete
	}
	cases := []struct {
		name   string
		hash   domain.Hash
		stored []byte
		want   outcome
	}{
		{name: "valid", hash: hash, stored: data, want: outcome{recorded: true, size: int64(len(data))}},
		{name: "hash mismatch", hash: hash, stored: []byte("garbage at the key"), want: outcome{deleted: true}},
		{name: "oversized object", hash: oversizedHash, stored: oversized, want: outcome{deleted: true}},
		{name: "not found", hash: hash},
	}

	for storeName, newStore := range stores {
		for _, tc := range cases {
			t.Run(storeName+"/"+tc.name, func(t *testing.T) {
				store := newStore(t)
				if tc.stored != nil {
					require.NoError(t, store.put(tc.hash, tc.stored))
				}
				repo := newFakeChunkRepo()
				uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour})
				metrics := obs.NewMetrics()
				uc = uc.WithMetrics(metrics)

				missing, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{tc.hash}, nil)
				require.NoError(t, err)
				if tc.want.recorded {
					require.Empty(t, missing)
					require.Equal(t, [][]ChunkRef{{{Hash: tc.hash, SizeBytes: tc.want.size}}}, repo.inserts)
				} else {
					require.Equal(t, []domain.Hash{tc.hash}, missing)
					require.Empty(t, repo.inserts)
				}
				require.Equal(t, int64(1), store.readCount())
				_, direct := store.(storage.DirectTransferStore)
				if tc.want.deleted && direct {
					require.Equal(t, []domain.Hash{tc.hash}, store.deletedHashes())
					require.Contains(t, scrapeUsecaseMetrics(t, metrics), `nipa_chunk_verify_failures_total{stage="confirm"} 1`)
				} else {
					require.Empty(t, store.deletedHashes())
				}
			})
		}
	}
}

func TestChunk_ConfirmUploads_DropInvalidChunkError(t *testing.T) {
	data := []byte("chunk content")
	hash := chunker.Sum(data)
	store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore(), deleteErr: errors.New("s3 down")}
	store.data[ck(hash)] = []byte("garbage")
	uc, _ := newLinkFixture(t, store)

	_, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, nil)
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, 500, domainErr.Code)
}

func TestChunk_ConfirmUploads_ReadsTheProjectsOwnObject(t *testing.T) {
	data := []byte("chunk content")
	hash := chunker.Sum(data)
	store := &openingDirectStore{stubDirectChunkStore: &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}}
	// Another project holds the bytes; this project uploaded nothing.
	store.data[objectKey{snow.ID(2), hash}] = data
	uc, repo := newLinkFixture(t, store)

	missing, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, nil)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{hash}, missing, "another project's copy never confirms this project's chunk")
	require.False(t, repo.has(testProject.ID, hash))
}

func TestChunk_ConfirmUploads_StreamReadErrorSurfaces(t *testing.T) {
	data := []byte("payload")
	hash := chunker.Sum(data)
	store := &failingReadStore{stubChunkStore: newStubChunkStore()}
	store.data[ck(hash)] = data
	uc, _ := newLinkFixture(t, store)

	_, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, nil)
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, domain.NewErrorDatabase("").Code, domainErr.Code)
	require.Contains(t, domainErr.InternalMessage, "connection reset")

	store.openErr = errors.New("open failed")
	_, err = uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, nil)
	require.ErrorAs(t, err, &domainErr)
	require.Contains(t, domainErr.InternalMessage, "open failed")
}

type failingReadStore struct {
	*stubChunkStore
	openErr error
}

func (s *failingReadStore) Open(context.Context, snow.ID, domain.Hash) (io.ReadCloser, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	return io.NopCloser(io.MultiReader(bytes.NewReader([]byte("pay")), errReader{errors.New("connection reset")})), nil
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// blockingStore holds every read until released and records the peak of
// reads in flight.
type blockingStore struct {
	*stubChunkStore
	release     chan struct{}
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
}

func (s *blockingStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		peak := s.maxInFlight.Load()
		if n <= peak || s.maxInFlight.CompareAndSwap(peak, n) {
			break
		}
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return io.NopCloser(bytes.NewReader(s.data[objectKey{projectID, hash}])), nil
}

func TestChunk_ConfirmUploads_VerifyLimitIsServerWide(t *testing.T) {
	const limit = 4
	store := &blockingStore{stubChunkStore: newStubChunkStore(), release: make(chan struct{})}
	uc := NewChunk(newFakeChunkRepo(), store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour}).WithVerifyLimit(limit)

	requests := make([][]domain.Hash, 6)
	for r := range requests {
		for i := 0; i < 5; i++ {
			data := []byte(fmt.Sprintf("confirm-%d-chunk-%d", r, i))
			hash := chunker.Sum(data)
			store.data[ck(hash)] = data
			requests[r] = append(requests[r], hash)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(requests))
	for _, hashes := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			missing, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, hashes, nil)
			if err == nil && len(missing) > 0 {
				err = fmt.Errorf("missing %v", missing)
			}
			errs <- err
		}()
	}

	require.Eventually(t, func() bool { return store.inFlight.Load() == limit }, 5*time.Second, time.Millisecond,
		"six concurrent confirms fill the shared limit")
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int64(limit), store.inFlight.Load(), "no confirm reads beyond the server-wide limit")
	close(store.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int64(limit), store.maxInFlight.Load())
}

func TestChunk_ConfirmUploads_CanceledWhileWaitingForAVerifySlot(t *testing.T) {
	store := &blockingStore{stubChunkStore: newStubChunkStore(), release: make(chan struct{})}
	uc := NewChunk(newFakeChunkRepo(), store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour}).WithVerifyLimit(1)

	var hashes []domain.Hash
	for i := 0; i < 3; i++ {
		data := []byte(fmt.Sprintf("cancel-%d", i))
		hash := chunker.Sum(data)
		store.data[ck(hash)] = data
		hashes = append(hashes, hash)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, hashes, nil)
		done <- err
	}()
	require.Eventually(t, func() bool { return store.inFlight.Load() == 1 }, 5*time.Second, time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("a canceled confirm must stop waiting for a verify slot")
	}
}

func TestChunk_WithVerifyLimitDefaults(t *testing.T) {
	uc := NewChunk(nil, newStubChunkStore(), ChunkTransferConfig{})
	for _, n := range []int{0, -3} {
		uc.WithVerifyLimit(n)
		require.True(t, uc.verifySlots.TryAcquire(DefaultChunkVerifyConcurrency))
		require.False(t, uc.verifySlots.TryAcquire(1))
	}
	uc.WithVerifyLimit(2)
	require.True(t, uc.verifySlots.TryAcquire(2))
	require.False(t, uc.verifySlots.TryAcquire(1))
}

// TestChunk_Presign_ExistsOnlyForRecordedChunks covers B4 with per-project
// rows: a hash without a row in the project gets an upload target without a
// store check, one with a row is checked (already stored when present,
// uploaded again when lost).
func TestChunk_Presign_ExistsOnlyForRecordedChunks(t *testing.T) {
	store := &countingChunkStore{stubChunkStore: newStubChunkStore()}
	direct := &stubDirectChunkStore{stubChunkStore: store.stubChunkStore, uploadTarget: storage.UploadTarget{URL: "https://s3.example.com/put"}}
	counted := &countingDirectStore{countingChunkStore: store, direct: direct}
	repo := newFakeChunkRepo()
	uc := NewChunk(repo, counted, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour, MaxPageSize: 10})

	payload := func(s string) ([]byte, domain.Hash) { return []byte(s), chunker.Sum([]byte(s)) }
	newData, newHash := payload("never seen")
	unrowedData, unrowedHash := payload("object without a row")
	lostData, lostHash := payload("row, object lost")
	storedData, storedHash := payload("row and object")
	store.data[ck(unrowedHash)] = unrowedData
	store.data[ck(storedHash)] = storedData
	repo.record(testProject.ID, lostHash, int64(len(lostData)))
	repo.record(testProject.ID, storedHash, int64(len(storedData)))

	refs := []ChunkRef{
		{Hash: newHash, SizeBytes: int64(len(newData))},
		{Hash: unrowedHash, SizeBytes: int64(len(unrowedData))},
		{Hash: lostHash, SizeBytes: int64(len(lostData))},
		{Hash: storedHash, SizeBytes: int64(len(storedData))},
	}

	urls, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, refs, 10, "")
	require.NoError(t, err)
	require.Equal(t, int64(2), store.existsCalls.Load(), "only the two hashes with a row are checked")

	require.Equal(t, "https://s3.example.com/put", urls[0].URL)
	require.Equal(t, "https://s3.example.com/put", urls[1].URL, "an object without a row gets a target; the create-only PUT answers 412")
	require.Equal(t, "https://s3.example.com/put", urls[2].URL, "a lost object is uploaded again")
	require.True(t, urls[3].AlreadyStored)
	require.Empty(t, urls[3].URL)
}

// countingDirectStore is a direct-transfer store whose Exists calls are
// counted.
type countingDirectStore struct {
	*countingChunkStore
	direct *stubDirectChunkStore
}

func (s *countingDirectStore) PresignDownload(ctx context.Context, projectID snow.ID, hash domain.Hash, expires time.Duration) (string, error) {
	return s.direct.PresignDownload(ctx, projectID, hash, expires)
}

func (s *countingDirectStore) PresignUpload(ctx context.Context, projectID snow.ID, hash domain.Hash, size int64, expires time.Duration) (storage.UploadTarget, error) {
	return s.direct.PresignUpload(ctx, projectID, hash, size, expires)
}

func (s *countingDirectStore) DeleteChunk(ctx context.Context, projectID snow.ID, hash domain.Hash) error {
	return s.direct.DeleteChunk(ctx, projectID, hash)
}
