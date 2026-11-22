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
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
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

func (s *openingLocalStore) Open(ctx context.Context, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	return s.LocalStore.Open(ctx, hash)
}

func (s *openingLocalStore) Get(ctx context.Context, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.LocalStore.Get(ctx, hash)
}

func (s *openingLocalStore) put(hash domain.Hash, data []byte) error {
	return s.Put(context.Background(), hash, data)
}

// openingDirectStore stands in for the S3 store: direct transfers (garbage
// is deleted) and streamed reads.
type openingDirectStore struct {
	*stubDirectChunkStore
	readCounter
	mu sync.Mutex
}

func (s *openingDirectStore) Open(_ context.Context, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.data[hash]
	if !ok {
		return nil, domain.NewErrorNotFound("chunk not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *openingDirectStore) Get(context.Context, domain.Hash) ([]byte, error) {
	panic("a store with Open must be streamed, not read whole")
}

func (s *openingDirectStore) put(hash domain.Hash, data []byte) error {
	s.data[hash] = data
	return nil
}

// gettingDirectStore is a direct store without Open: confirm falls back to Get.
type gettingDirectStore struct {
	*stubDirectChunkStore
	readCounter
}

func (s *gettingDirectStore) Get(ctx context.Context, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.stubDirectChunkStore.Get(ctx, hash)
}

func (s *gettingDirectStore) put(hash domain.Hash, data []byte) error {
	s.data[hash] = data
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
// against a streaming local store, a streaming direct store and a direct store
// without streaming, so the streamed read-back decides exactly as the
// whole-object read did.
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
	now := time.Unix(1700000000, 0)
	challengeFor := func(t *testing.T, project snow.ID, h domain.Hash, expires time.Time) string {
		challenge, err := chunkproof.NewChallenge(linkTestKey, project, h, expires)
		require.NoError(t, err)
		return challenge
	}

	type outcome struct {
		linked  bool
		size    int64
		reads   bool
		deleted bool // only stores that support direct transfers delete
	}
	cases := []struct {
		name   string
		hash   domain.Hash
		stored []byte
		proof  func(t *testing.T) ChunkProof
		want   outcome
	}{
		{
			name: "valid", hash: hash, stored: data,
			proof: func(t *testing.T) ChunkProof {
				c := challengeFor(t, testProject.ID, hash, now.Add(time.Hour))
				return ChunkProof{Hash: hash, Challenge: c, Proof: chunkproof.Proof(c, data)}
			},
			want: outcome{linked: true, size: int64(len(data)), reads: true},
		},
		{
			name: "hash mismatch", hash: hash, stored: []byte("garbage at the key"),
			proof: func(t *testing.T) ChunkProof {
				c := challengeFor(t, testProject.ID, hash, now.Add(time.Hour))
				return ChunkProof{Hash: hash, Challenge: c, Proof: chunkproof.Proof(c, data)}
			},
			want: outcome{reads: true, deleted: true},
		},
		{
			name: "oversized object", hash: oversizedHash, stored: oversized,
			proof: func(t *testing.T) ChunkProof {
				c := challengeFor(t, testProject.ID, oversizedHash, now.Add(time.Hour))
				return ChunkProof{Hash: oversizedHash, Challenge: c, Proof: chunkproof.Proof(c, oversized)}
			},
			want: outcome{reads: true, deleted: true},
		},
		{
			name: "not found", hash: hash,
			proof: func(t *testing.T) ChunkProof {
				c := challengeFor(t, testProject.ID, hash, now.Add(time.Hour))
				return ChunkProof{Hash: hash, Challenge: c, Proof: chunkproof.Proof(c, data)}
			},
			want: outcome{reads: true},
		},
		{
			name: "no proof", hash: hash, stored: data,
			proof: func(*testing.T) ChunkProof { return ChunkProof{} },
		},
		{
			name: "forged challenge", hash: hash, stored: data,
			proof: func(*testing.T) ChunkProof {
				return ChunkProof{Hash: hash, Challenge: "forged.sig", Proof: chunkproof.Proof("forged.sig", data)}
			},
		},
		{
			name: "expired challenge", hash: hash, stored: data,
			proof: func(t *testing.T) ChunkProof {
				c := challengeFor(t, testProject.ID, hash, now.Add(-time.Second))
				return ChunkProof{Hash: hash, Challenge: c, Proof: chunkproof.Proof(c, data)}
			},
		},
		{
			name: "challenge for another project", hash: hash, stored: data,
			proof: func(t *testing.T) ChunkProof {
				c := challengeFor(t, snow.ID(99), hash, now.Add(time.Hour))
				return ChunkProof{Hash: hash, Challenge: c, Proof: chunkproof.Proof(c, data)}
			},
		},
		{
			name: "challenge for another hash", hash: hash, stored: data,
			proof: func(t *testing.T) ChunkProof {
				other := chunker.Sum([]byte("other"))
				c := challengeFor(t, testProject.ID, other, now.Add(time.Hour))
				return ChunkProof{Hash: hash, Challenge: c, Proof: chunkproof.Proof(c, data)}
			},
		},
		{
			name: "wrong proof", hash: hash, stored: data,
			proof: func(t *testing.T) ChunkProof {
				c := challengeFor(t, testProject.ID, hash, now.Add(time.Hour))
				return ChunkProof{Hash: hash, Challenge: c, Proof: chunkproof.Proof(c, []byte("guess"))}
			},
			want: outcome{reads: true},
		},
	}

	for storeName, newStore := range stores {
		for _, tc := range cases {
			t.Run(storeName+"/"+tc.name, func(t *testing.T) {
				store := newStore(t)
				if tc.stored != nil {
					require.NoError(t, store.put(tc.hash, tc.stored))
				}
				repo := NewMockchunkRepository(gomock.NewController(t))
				uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour})
				uc.now = func() time.Time { return now }
				metrics := obs.NewMetrics()
				uc = uc.WithMetrics(metrics)
				repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, []domain.Hash{tc.hash}).Return(map[domain.Hash]int64{}, nil)
				if tc.want.linked {
					repo.EXPECT().InsertChunksIfNotExist(gomock.Any(), []ChunkRef{{Hash: tc.hash, SizeBytes: tc.want.size}}).Return(nil)
					repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{tc.hash}).Return(nil)
				}

				missing, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{tc.hash}, []ChunkProof{tc.proof(t)})
				require.NoError(t, err)
				if tc.want.linked {
					require.Empty(t, missing)
				} else {
					require.Equal(t, []domain.Hash{tc.hash}, missing)
				}
				if tc.want.reads {
					require.Equal(t, int64(1), store.readCount())
				} else {
					require.Zero(t, store.readCount(), "a rejected challenge must not cost a store read")
				}
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

func TestChunk_ConfirmUploads_StreamReadErrorSurfaces(t *testing.T) {
	data := []byte("payload")
	hash := chunker.Sum(data)
	store := &failingReadStore{stubChunkStore: newStubChunkStore()}
	store.data[hash] = data
	uc, repo := newLinkFixture(t, store)
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]int64{}, nil).Times(2)

	_, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, domain.NewErrorDatabase("").Code, domainErr.Code)
	require.Contains(t, domainErr.InternalMessage, "connection reset")

	store.openErr = errors.New("open failed")
	_, err = uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
	require.ErrorAs(t, err, &domainErr)
	require.Contains(t, domainErr.InternalMessage, "open failed")
}

type failingReadStore struct {
	*stubChunkStore
	openErr error
}

func (s *failingReadStore) Open(context.Context, domain.Hash) (io.ReadCloser, error) {
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

func (s *blockingStore) Open(ctx context.Context, hash domain.Hash) (io.ReadCloser, error) {
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
	return io.NopCloser(bytes.NewReader(s.data[hash])), nil
}

func TestChunk_ConfirmUploads_VerifyLimitIsServerWide(t *testing.T) {
	const limit = 4
	store := &blockingStore{stubChunkStore: newStubChunkStore(), release: make(chan struct{})}
	repo := NewMockchunkRepository(gomock.NewController(t))
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[domain.Hash]int64{}, nil).AnyTimes()
	repo.EXPECT().InsertChunksIfNotExist(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	repo.EXPECT().LinkProjectChunks(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour}).WithVerifyLimit(limit)

	type request struct {
		hashes []domain.Hash
		proofs []ChunkProof
	}
	requests := make([]request, 6)
	for r := range requests {
		for i := 0; i < 5; i++ {
			data := []byte(fmt.Sprintf("confirm-%d-chunk-%d", r, i))
			hash := chunker.Sum(data)
			store.data[hash] = data
			requests[r].hashes = append(requests[r].hashes, hash)
			requests[r].proofs = append(requests[r].proofs, proofFor(t, uc, hash, data))
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(requests))
	for _, req := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			missing, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, req.hashes, req.proofs)
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
	repo := NewMockchunkRepository(gomock.NewController(t))
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), gomock.Any(), gomock.Any()).Return(map[domain.Hash]int64{}, nil)
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour}).WithVerifyLimit(1)

	var hashes []domain.Hash
	var proofs []ChunkProof
	for i := 0; i < 3; i++ {
		data := []byte(fmt.Sprintf("cancel-%d", i))
		hash := chunker.Sum(data)
		store.data[hash] = data
		hashes = append(hashes, hash)
		proofs = append(proofs, proofFor(t, uc, hash, data))
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, hashes, proofs)
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

// TestChunk_Presign_ExistsOnlyForKnownChunks covers B4: a hash without a
// chunk row gets an upload target without a store check, one with a row is
// checked (claim when present, upload again when lost), and linked + present
// is already stored.
func TestChunk_Presign_ExistsOnlyForKnownChunks(t *testing.T) {
	store := &countingChunkStore{stubChunkStore: newStubChunkStore()}
	direct := &stubDirectChunkStore{stubChunkStore: store.stubChunkStore, uploadTarget: storage.UploadTarget{URL: "https://s3.example.com/put"}}
	counted := &countingDirectStore{countingChunkStore: store, direct: direct}
	repo := NewMockchunkRepository(gomock.NewController(t))
	uc := NewChunk(repo, counted, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour, MaxPageSize: 10})

	payload := func(s string) ([]byte, domain.Hash) { return []byte(s), chunker.Sum([]byte(s)) }
	newData, newHash := payload("never seen")
	unrowedData, unrowedHash := payload("object without a row")
	claimData, claimHash := payload("row and object")
	lostData, lostHash := payload("row, object lost")
	linkedData, linkedHash := payload("linked and present")
	store.data[unrowedHash] = unrowedData
	store.data[claimHash] = claimData
	store.data[linkedHash] = linkedData

	refs := []ChunkRef{
		{Hash: newHash, SizeBytes: int64(len(newData))},
		{Hash: unrowedHash, SizeBytes: int64(len(unrowedData))},
		{Hash: claimHash, SizeBytes: int64(len(claimData))},
		{Hash: lostHash, SizeBytes: int64(len(lostData))},
		{Hash: linkedHash, SizeBytes: int64(len(linkedData))},
	}
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, gomock.Any()).
		Return(map[domain.Hash]int64{linkedHash: int64(len(linkedData))}, nil)
	repo.EXPECT().KnownChunks(gomock.Any(), []domain.Hash{newHash, unrowedHash, claimHash, lostHash, linkedHash}).
		Return(map[domain.Hash]struct{}{claimHash: {}, lostHash: {}, linkedHash: {}}, nil)

	urls, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, refs, 10, "")
	require.NoError(t, err)
	require.Equal(t, int64(3), store.existsCalls.Load(), "only the three hashes with a row are checked")

	require.Equal(t, "https://s3.example.com/put", urls[0].URL)
	require.NotEmpty(t, urls[0].ProofChallenge)
	require.Equal(t, "https://s3.example.com/put", urls[1].URL, "an object without a row gets a target; the create-only PUT answers 412")
	require.NotEmpty(t, urls[1].ProofChallenge)
	require.Empty(t, urls[2].URL, "a stored chunk with a row is claimed")
	require.NotEmpty(t, urls[2].ProofChallenge)
	require.Equal(t, "https://s3.example.com/put", urls[3].URL, "a lost object is uploaded again")
	require.True(t, urls[4].AlreadyStored)
	require.Empty(t, urls[4].ProofChallenge)
}

func TestChunk_Presign_KnownChunksError(t *testing.T) {
	uc, repo := newLinkFixture(t, newStubChunkStore())
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]int64{}, nil)
	repo.EXPECT().KnownChunks(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

	_, _, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: chunker.Sum([]byte("x")), SizeBytes: 1}}, 10, "")
	require.EqualError(t, err, "db down")
}

// countingDirectStore is a direct-transfer store whose Exists calls are
// counted.
type countingDirectStore struct {
	*countingChunkStore
	direct *stubDirectChunkStore
}

func (s *countingDirectStore) PresignDownload(ctx context.Context, hash domain.Hash, expires time.Duration) (string, error) {
	return s.direct.PresignDownload(ctx, hash, expires)
}

func (s *countingDirectStore) PresignUpload(ctx context.Context, hash domain.Hash, size int64, expires time.Duration) (storage.UploadTarget, error) {
	return s.direct.PresignUpload(ctx, hash, size, expires)
}

func (s *countingDirectStore) DeleteChunk(ctx context.Context, hash domain.Hash) error {
	return s.direct.DeleteChunk(ctx, hash)
}

func TestChunk_ConfirmUploads_RowInsertError(t *testing.T) {
	data := []byte("payload")
	hash := chunker.Sum(data)
	store := newStubChunkStore()
	store.data[hash] = data
	uc, repo := newLinkFixture(t, store)
	repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, gomock.Any()).Return(map[domain.Hash]int64{}, nil)
	repo.EXPECT().InsertChunksIfNotExist(gomock.Any(), gomock.Any()).Return(errors.New("rows failed"))
	repo.EXPECT().LinkProjectChunks(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	_, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, uc, hash, data)})
	require.EqualError(t, err, "rows failed")
}
