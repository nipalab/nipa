package usecase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/storage/pack"
)

// fakePackStore is a direct-transfer store (loose objects, overwrites, HEADs
// counted) that also holds packs.
type fakePackStore struct {
	*overwritingDirectStore
	packMu   sync.Mutex
	packs    map[string][]byte
	packPuts int
	ranges   int
	presigns int
	putErr   error
}

func newFakePackStore() *fakePackStore {
	return &fakePackStore{overwritingDirectStore: newOverwritingDirectStore(), packs: map[string][]byte{}}
}

func (s *fakePackStore) PutPack(_ context.Context, key string, body io.ReadSeeker, size int64) error {
	if s.putErr != nil {
		return s.putErr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("size mismatch")
	}
	s.packMu.Lock()
	defer s.packMu.Unlock()
	s.packPuts++
	s.packs[key] = data
	return nil
}

func (s *fakePackStore) GetRange(_ context.Context, key string, offset, length int64) ([]byte, error) {
	s.packMu.Lock()
	defer s.packMu.Unlock()
	s.ranges++
	data, ok := s.packs[key]
	if !ok {
		return nil, domain.NewErrorNotFound("object not found")
	}
	if offset+length > int64(len(data)) {
		return nil, errors.New("short object")
	}
	return append([]byte(nil), data[offset:offset+length]...), nil
}

func (s *fakePackStore) PresignPackDownload(_ context.Context, key string, _ time.Duration) (string, error) {
	s.packMu.Lock()
	defer s.packMu.Unlock()
	s.presigns++
	return "https://s3.example.com/" + key, nil
}

func (s *fakePackStore) DeletePack(_ context.Context, key string) error {
	s.packMu.Lock()
	defer s.packMu.Unlock()
	delete(s.packs, key)
	return nil
}

func mustNode(t *testing.T) snow.Node {
	t.Helper()
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return node
}

// newPackFixture is a chunk usecase with packs on over a fake pack store.
func newPackFixture(t *testing.T) (*Chunk, *fakeChunkRepo, *fakePackStore) {
	t.Helper()
	store := newFakePackStore()
	uc, repo := newLinkFixture(t, store)
	uc.WithPacks(mustNode(t)).WithUploadMode(UploadModeProxy)
	uc.WithChunkStores(PackedReads(StaticStores(store), repo))
	return uc, repo, store
}

func TestChunk_StoreBundle_WritesOnePack(t *testing.T) {
	a, b, c := []byte("pack entry a"), []byte("pack entry bb"), []byte("pack entry ccc")
	uc, repo, store := newPackFixture(t)
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }

	receipts, err := storeBundle(uc, bundleOf(t, a, b, c, a))
	require.NoError(t, err)
	require.Len(t, receipts, 3)
	require.Equal(t, 1, store.packPuts, "one store write for the whole bundle")
	require.Zero(t, store.overwrites, "no loose object")
	require.Empty(t, repo.inserts, "no chunk row before confirm")
	require.Len(t, repo.packs, 1)
	var packID snow.ID
	for id, p := range repo.packs {
		packID = id
		require.Equal(t, testProject.ID, p.ProjectID)
		require.Equal(t, 3, p.ChunkCount)
	}
	require.Equal(t, ChunkPackStored, repo.states[packID], "pending, then stored once the object is written")

	object := store.packs[storage.PackKey(testProject.ID, packID)]
	require.Equal(t, int64(len(object)), repo.packs[packID].SizeBytes)
	index, err := pack.ReadIndex(bytes.NewReader(object), int64(len(object)))
	require.NoError(t, err)
	require.Len(t, index, 3)
	for i, data := range [][]byte{a, b, c} {
		hash := chunker.Sum(data)
		require.Equal(t, hash, receipts[i].Hash)
		size, location, err := chunkproof.VerifyReceipt(testSigningKey, testProject.ID, hash, receipts[i].Receipt, uc.now())
		require.NoError(t, err)
		require.Equal(t, int64(len(data)), size)
		require.Equal(t, chunkproof.LocationPack, location.Kind)
		require.Equal(t, packID, location.PackID)
		require.Equal(t, index[i].Offset, location.Offset)
		require.Equal(t, data, object[location.Offset:location.Offset+size])
	}
}

func TestChunk_StoreChunks_PackFailures(t *testing.T) {
	ctx := context.Background()
	data := []byte("merge output")
	content := []ChunkContent{{Hash: chunker.Sum(data), Data: data}}

	t.Run("object write fails after the row insert", func(t *testing.T) {
		uc, repo, store := newPackFixture(t)
		store.putErr = errors.New("bucket down")
		_, err := uc.StoreChunks(ctx, testProject.ID, content)
		requireInternal(t, err, "bucket down")
		require.Len(t, repo.states, 1)
		for _, state := range repo.states {
			require.Equal(t, ChunkPackPending, state, "the pending row stays for the garbage collector")
		}
	})
	t.Run("pack row insert fails", func(t *testing.T) {
		uc, repo, store := newPackFixture(t)
		repo.packErr = errors.New("db down")
		_, err := uc.StoreChunks(ctx, testProject.ID, content)
		require.ErrorIs(t, err, repo.packErr)
		require.Zero(t, store.packPuts, "no object without a row")
	})
	t.Run("stored state fails", func(t *testing.T) {
		uc, repo, store := newPackFixture(t)
		repo.stateErr = errors.New("db down")
		_, err := uc.StoreChunks(ctx, testProject.ID, content)
		require.ErrorIs(t, err, repo.stateErr)
		require.Equal(t, 1, store.packPuts)
	})
	t.Run("merge output is packed and located", func(t *testing.T) {
		uc, repo, store := newPackFixture(t)
		stored, err := uc.StoreChunks(ctx, testProject.ID, content)
		require.NoError(t, err)
		require.Len(t, stored, 1)
		require.True(t, stored[0].Location.Packed())
		require.Equal(t, 1, store.packPuts)
		require.NoError(t, uc.RecordStoredChunks(ctx, testProject.ID, stored))
		require.Equal(t, stored[0].Location, repo.location(testProject.ID, content[0].Hash))
		got, err := uc.Download(ctx, testProject.ID, content[0].Hash)
		require.NoError(t, err)
		require.Equal(t, data, got, "merged content reads back through the pack")
	})
}

func TestChunk_StoreChunks_PacksOnlyOnTheOwnPackStore(t *testing.T) {
	ctx := context.Background()
	data := []byte("loose content")
	content := []ChunkContent{{Hash: chunker.Sum(data), Data: data}}

	t.Run("packs off", func(t *testing.T) {
		store := newFakePackStore()
		uc, _ := newLinkFixture(t, store)
		stored, err := uc.StoreChunks(ctx, testProject.ID, content)
		require.NoError(t, err)
		require.False(t, stored[0].Location.Packed())
		require.Zero(t, store.packPuts)
		require.Equal(t, 1, store.overwrites)
	})
	t.Run("own storage", func(t *testing.T) {
		store := newFakePackStore()
		uc, _ := newLinkFixture(t, store)
		uc.WithPacks(mustNode(t)).WithChunkStores(fakeChunkStores{projects: map[snow.ID]ProjectStore{
			testProject.ID: {Store: store, External: true},
		}})
		stored, err := uc.StoreChunks(ctx, testProject.ID, content)
		require.NoError(t, err)
		require.False(t, stored[0].Location.Packed(), "a customer bucket keeps loose objects")
		require.Zero(t, store.packPuts)
	})
	t.Run("store without packs", func(t *testing.T) {
		store := newStubChunkStore()
		uc, _ := newLinkFixture(t, store)
		uc.WithPacks(mustNode(t))
		stored, err := uc.StoreChunks(ctx, testProject.ID, content)
		require.NoError(t, err)
		require.False(t, stored[0].Location.Packed())
		require.Equal(t, 1, store.puts)
	})
}

// concurrentStore counts how many writes run at once; a write of failHash
// fails right away and the others wait until their context ends or delay
// passes.
type concurrentStore struct {
	*stubChunkStore
	inFlight  atomic.Int64
	peak      atomic.Int64
	cancelled atomic.Int64
	delay     time.Duration
	failHash  domain.Hash
}

func (s *concurrentStore) Put(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		peak := s.peak.Load()
		if n <= peak || s.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	if hash == s.failHash {
		return errors.New("write failed")
	}
	select {
	case <-ctx.Done():
		s.cancelled.Add(1)
		return ctx.Err()
	case <-time.After(s.delay):
	}
	return s.stubChunkStore.Put(ctx, projectID, hash, data)
}

func manyContents(n int) []ChunkContent {
	out := make([]ChunkContent, 0, n)
	for i := 0; i < n; i++ {
		data := []byte{byte(i), byte(i >> 8), 'x'}
		out = append(out, ChunkContent{Hash: chunker.Sum(data), Data: data})
	}
	return out
}

func TestChunk_StoreChunks_LooseWritesRunConcurrently(t *testing.T) {
	store := &concurrentStore{stubChunkStore: newStubChunkStore(), delay: 10 * time.Millisecond}
	uc, _ := newLinkFixture(t, store)
	uc.WithStoreWriteLimit(4)

	stored, err := uc.StoreChunks(context.Background(), testProject.ID, manyContents(24))
	require.NoError(t, err)
	require.Len(t, stored, 24)
	require.Equal(t, 24, store.puts)
	require.LessOrEqual(t, store.peak.Load(), int64(4), "never more than the server-wide limit")
	require.Greater(t, store.peak.Load(), int64(1), "writes overlap")
}

func TestChunk_StoreChunks_FirstErrorCancelsTheRest(t *testing.T) {
	contents := manyContents(16)
	store := &concurrentStore{stubChunkStore: newStubChunkStore(), delay: time.Minute, failHash: contents[3].Hash}
	uc, _ := newLinkFixture(t, store)
	uc.WithStoreWriteLimit(8)

	start := time.Now()
	_, err := uc.StoreChunks(context.Background(), testProject.ID, contents)
	requireInternal(t, err, "write failed")
	require.Less(t, time.Since(start), 30*time.Second, "waiting writes were cancelled")
	require.Positive(t, store.cancelled.Load())
	require.Zero(t, store.puts)
}

func TestChunk_WithStoreWriteLimitDefault(t *testing.T) {
	uc, _ := newLinkFixture(t, newStubChunkStore())
	uc.WithStoreWriteLimit(0)
	require.True(t, uc.writeSlots.TryAcquire(DefaultChunkStoreWriteConcurrency))
	require.False(t, uc.writeSlots.TryAcquire(1))
}

func TestChunk_ConfirmUploads_PackReceipts(t *testing.T) {
	ctx := context.Background()
	a, b := []byte("confirm a"), []byte("confirm b")
	uc, repo, store := newPackFixture(t)
	receipts, err := storeBundle(uc, bundleOf(t, a, b))
	require.NoError(t, err)
	gets, ranges := store.gets, store.ranges

	hashes := []domain.Hash{chunker.Sum(a), chunker.Sum(b)}
	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, hashes, receipts)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.Equal(t, gets, store.gets, "no store read at confirm")
	require.Equal(t, ranges, store.ranges)
	require.Zero(t, store.existsCalls)
	require.Len(t, repo.inserts, 1)
	for i, row := range repo.inserts[0] {
		require.Equal(t, hashes[i], row.Hash)
		require.True(t, row.Location.Packed(), "the row records the receipt's pack location")
	}
}

func TestChunk_ConfirmUploads_ExistingRows(t *testing.T) {
	ctx := context.Background()
	data := []byte("uploaded twice")
	hash := chunker.Sum(data)
	oldPack := ChunkLocation{PackID: snow.ID(77), Offset: 4}

	confirm := func(t *testing.T, setup func(repo *fakeChunkRepo, store *fakePackStore)) (*fakeChunkRepo, *fakePackStore, ChunkLocation) {
		t.Helper()
		uc, repo, store := newPackFixture(t)
		setup(repo, store)
		receipts, err := storeBundle(uc, bundleOf(t, data))
		require.NoError(t, err)
		_, location, err := chunkproof.VerifyReceipt(testSigningKey, testProject.ID, hash, receipts[0].Receipt, time.Now())
		require.NoError(t, err)
		missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, receipts)
		require.NoError(t, err)
		require.Empty(t, missing)
		require.Empty(t, repo.inserts, "an existing row is never inserted again")
		return repo, store, locationFromProof(location)
	}

	t.Run("a readable pack keeps its location", func(t *testing.T) {
		repo, store, _ := confirm(t, func(repo *fakeChunkRepo, _ *fakePackStore) {
			repo.recordAt(testProject.ID, hash, int64(len(data)), oldPack)
			repo.states[oldPack.PackID] = ChunkPackStored
		})
		require.Equal(t, oldPack, repo.location(testProject.ID, hash), "the new copy is dead bytes")
		require.Empty(t, repo.moves)
		require.Zero(t, store.existsCalls, "a packed row is checked without a HEAD")
	})
	t.Run("a lost pack is relocated", func(t *testing.T) {
		repo, store, fresh := confirm(t, func(repo *fakeChunkRepo, _ *fakePackStore) {
			repo.recordAt(testProject.ID, hash, int64(len(data)), oldPack)
		})
		require.Equal(t, fresh, repo.location(testProject.ID, hash))
		require.Len(t, repo.moves, 1)
		require.Zero(t, store.existsCalls)
	})
	t.Run("a present loose object keeps its location", func(t *testing.T) {
		repo, store, _ := confirm(t, func(repo *fakeChunkRepo, store *fakePackStore) {
			repo.recordAt(testProject.ID, hash, int64(len(data)), ChunkLocation{})
			store.data[ck(hash)] = data
		})
		require.Equal(t, ChunkLocation{}, repo.location(testProject.ID, hash))
		require.Empty(t, repo.moves)
		require.Equal(t, 1, store.existsCalls, "one HEAD for the loose row")
	})
	t.Run("a lost loose object is relocated", func(t *testing.T) {
		repo, store, fresh := confirm(t, func(repo *fakeChunkRepo, _ *fakePackStore) {
			repo.recordAt(testProject.ID, hash, int64(len(data)), ChunkLocation{})
		})
		require.Equal(t, fresh, repo.location(testProject.ID, hash))
		require.Equal(t, []ChunkRelocation{{Hash: hash, From: ChunkLocation{}, To: fresh}}, repo.moves)
		require.Equal(t, 1, store.existsCalls)
	})
}

func TestChunk_ConfirmUploads_RelocationErrors(t *testing.T) {
	ctx := context.Background()
	data := []byte("relocation error")
	hash := chunker.Sum(data)

	t.Run("existence check fails", func(t *testing.T) {
		uc, repo, store := newPackFixture(t)
		repo.recordAt(testProject.ID, hash, int64(len(data)), ChunkLocation{})
		receipts, err := storeBundle(uc, bundleOf(t, data))
		require.NoError(t, err)
		store.existsErr = errors.New("head failed")
		_, err = uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, receipts)
		requireInternal(t, err, "head failed")
	})
	t.Run("move fails", func(t *testing.T) {
		uc, repo, _ := newPackFixture(t)
		repo.recordAt(testProject.ID, hash, int64(len(data)), ChunkLocation{PackID: 9, Offset: 4})
		receipts, err := storeBundle(uc, bundleOf(t, data))
		require.NoError(t, err)
		repo.moveErr = errors.New("db down")
		_, err = uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, receipts)
		require.ErrorIs(t, err, repo.moveErr)
	})
}

func TestChunk_PresignUpload_PackedRowsWithoutHead(t *testing.T) {
	ctx := context.Background()
	stored, pending := []byte("in a stored pack"), []byte("in a pending pack")
	uc, repo, store := newPackFixture(t)
	repo.recordAt(testProject.ID, chunker.Sum(stored), int64(len(stored)), ChunkLocation{PackID: 1, Offset: 4})
	repo.states[1] = ChunkPackStored
	repo.recordAt(testProject.ID, chunker.Sum(pending), int64(len(pending)), ChunkLocation{PackID: 2, Offset: 4})
	repo.states[2] = ChunkPackPending

	presign, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{
		{Hash: chunker.Sum(stored), SizeBytes: int64(len(stored))},
		{Hash: chunker.Sum(pending), SizeBytes: int64(len(pending))},
	}, 0, "")
	require.NoError(t, err)
	require.True(t, presign.URLs[0].AlreadyStored)
	require.False(t, presign.URLs[1].AlreadyStored, "a pack that is not readable needs the upload again")
	require.NotEmpty(t, presign.BundleURL)
	require.Zero(t, store.existsCalls, "packed rows are answered from the pack row")
}

func TestChunk_PresignDownload_PackRanges(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newPackFixture(t)
	store.downloadURL = "https://s3.example.com/loose"
	a, b, c, loose, unknown := domain.Hash{1}, domain.Hash{2}, domain.Hash{3}, domain.Hash{4}, domain.Hash{5}
	repo.recordAt(testProject.ID, a, 10, ChunkLocation{PackID: 7, Offset: 4})
	repo.recordAt(testProject.ID, b, 20, ChunkLocation{PackID: 8, Offset: 4})
	repo.recordAt(testProject.ID, c, 30, ChunkLocation{PackID: 7, Offset: 14})
	repo.recordAt(testProject.ID, loose, 40, ChunkLocation{})

	urls, next, err := uc.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{a, b, c, loose, unknown}, 0, "")
	require.NoError(t, err)
	require.Empty(t, next)
	packA := "https://s3.example.com/" + storage.PackKey(testProject.ID, 7)
	packB := "https://s3.example.com/" + storage.PackKey(testProject.ID, 8)
	require.Equal(t, []ChunkURL{
		{Hash: a, URL: packA, Offset: 4, Length: 10},
		{Hash: b, URL: packB, Offset: 4, Length: 20},
		{Hash: c, URL: packA, Offset: 14, Length: 30},
		{Hash: loose, URL: "https://s3.example.com/loose"},
		{Hash: unknown, URL: "https://s3.example.com/loose"},
	}, urls)
	require.Equal(t, 2, store.presigns, "one presign per pack per page")
}

func TestChunk_PresignDownload_NoLookupWithoutPacks(t *testing.T) {
	store := newStubChunkStore()
	uc, repo := newLinkFixture(t, store)
	repo.sizesErr = errors.New("must not be queried")
	urls, _, err := uc.PresignDownloadURLs(context.Background(), testOrg, testProject, []domain.Hash{{1}}, 0, "")
	require.NoError(t, err)
	require.Len(t, urls, 1)
	require.Zero(t, urls[0].Length)

	ext := newFakePackStore()
	uc.WithChunkStores(fakeChunkStores{projects: map[snow.ID]ProjectStore{testProject.ID: {Store: ext, External: true}}})
	ext.downloadURL = "https://byo.example.com/x"
	urls, _, err = uc.PresignDownloadURLs(context.Background(), testOrg, testProject, []domain.Hash{{1}}, 0, "")
	require.NoError(t, err)
	require.Equal(t, "https://byo.example.com/x", urls[0].URL, "own storage never has packed rows")
}

func TestChunk_PresignDownload_PackErrors(t *testing.T) {
	ctx := context.Background()
	uc, repo, _ := newPackFixture(t)
	repo.sizesErr = errors.New("db down")
	_, _, err := uc.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{{1}}, 0, "")
	require.ErrorIs(t, err, repo.sizesErr)

	failing := &failingPresignPackStore{fakePackStore: newFakePackStore()}
	uc2, repo2 := newLinkFixture(t, failing)
	repo2.recordAt(testProject.ID, domain.Hash{1}, 10, ChunkLocation{PackID: 7, Offset: 4})
	_, _, err = uc2.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{{1}}, 0, "")
	requireInternal(t, err, "presign download for pack")
}

type failingPresignPackStore struct {
	*fakePackStore
}

func (s *failingPresignPackStore) PresignPackDownload(context.Context, string, time.Duration) (string, error) {
	return "", errors.New("signer down")
}

// countingLocator counts the lookups a pack-aware reader makes.
type countingLocator struct {
	*fakeChunkRepo
	calls atomic.Int64
	err   error
}

func (l *countingLocator) ProjectChunkRecords(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]ChunkRecord, error) {
	l.calls.Add(1)
	if l.err != nil {
		return nil, l.err
	}
	return l.fakeChunkRepo.ProjectChunkRecords(ctx, projectID, hashes)
}

// packedFixture stores contents in one pack of testProject and records
// their rows; it returns the pack-aware store.
func packedFixture(t *testing.T, contents ...[]byte) (*packedStore, *countingLocator, *fakePackStore, []ChunkRow) {
	t.Helper()
	store := newFakePackStore()
	repo := newFakeChunkRepo()
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour}).WithPacks(mustNode(t))
	chunks := make([]ChunkContent, 0, len(contents))
	for _, data := range contents {
		chunks = append(chunks, ChunkContent{Hash: chunker.Sum(data), Data: data})
	}
	stored, err := uc.StoreChunks(context.Background(), testProject.ID, chunks)
	require.NoError(t, err)
	require.NoError(t, uc.RecordStoredChunks(context.Background(), testProject.ID, stored))
	locator := &countingLocator{fakeChunkRepo: repo}
	resolved, err := PackedReads(StaticStores(store), locator).ForProject(context.Background(), testProject.ID)
	require.NoError(t, err)
	packed, ok := resolved.Store.(*packedStore)
	require.True(t, ok)
	return packed, locator, store, stored
}

func TestPackedStore_Reads(t *testing.T) {
	ctx := context.Background()
	a, b := []byte("packed a"), []byte("packed b")
	packed, locator, store, stored := packedFixture(t, a, b)

	got, err := packed.Get(ctx, testProject.ID, chunker.Sum(a))
	require.NoError(t, err)
	require.Equal(t, a, got)
	require.Equal(t, 1, store.ranges, "a range read of the pack")
	got, err = packed.Get(ctx, testProject.ID, chunker.Sum(a))
	require.NoError(t, err)
	require.Equal(t, a, got)
	require.Equal(t, int64(1), locator.calls.Load(), "the location is cached")

	reader, err := packed.Open(ctx, testProject.ID, chunker.Sum(b))
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, b, body)

	size, err := packed.Size(ctx, testProject.ID, chunker.Sum(b))
	require.NoError(t, err)
	require.Equal(t, int64(len(b)), size)

	exists, err := packed.Exists(ctx, testProject.ID, stored[0].Hash)
	require.NoError(t, err)
	require.True(t, exists)
	require.Zero(t, store.existsCalls, "a packed chunk exists by its pack row, no HEAD")
	locator.states[stored[0].Location.PackID] = ChunkPackPending
	exists, err = packed.Exists(ctx, testProject.ID, stored[0].Hash)
	require.NoError(t, err)
	require.False(t, exists)
	require.Zero(t, store.existsCalls)

	require.NoError(t, packed.Close())
}

func TestPackedStore_LooseReads(t *testing.T) {
	ctx := context.Background()
	packed, locator, store, _ := packedFixture(t, []byte("packed"))
	loose := []byte("loose object")
	hash := chunker.Sum(loose)
	require.NoError(t, packed.Put(ctx, testProject.ID, hash, loose))

	got, err := packed.Get(ctx, testProject.ID, hash)
	require.NoError(t, err, "a hash without a row reads its per-project key")
	require.Equal(t, loose, got)

	locator.recordAt(testProject.ID, hash, int64(len(loose)), ChunkLocation{})
	packed.cache.forget(testProject.ID, hash)
	got, err = packed.Get(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, loose, got)

	size, err := packed.Size(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, int64(len(loose)), size)
	exists, err := packed.Exists(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, 1, store.existsCalls, "a loose row is checked in the store")

	store.data[ck(hash)] = []byte("garbage at the key")
	_, err = packed.Get(ctx, testProject.ID, hash)
	requireInternal(t, err, "does not match its hash")
	_, err = packed.Open(ctx, testProject.ID, hash)
	require.Error(t, err)
}

func TestPackedStore_HashMismatchIsAnError(t *testing.T) {
	ctx := context.Background()
	data := []byte("corrupted in the pack")
	packed, _, store, stored := packedFixture(t, data)
	key := storage.PackKey(testProject.ID, stored[0].Location.PackID)
	store.packs[key][stored[0].Location.Offset] ^= 0xff

	_, err := packed.Get(ctx, testProject.ID, chunker.Sum(data))
	requireInternal(t, err, "does not match its hash")
}

func TestPackedStore_StaleLocationIsRefreshed(t *testing.T) {
	ctx := context.Background()
	data := []byte("moved by compaction")
	hash := chunker.Sum(data)
	packed, locator, store, stored := packedFixture(t, data)
	_, err := packed.Get(ctx, testProject.ID, hash)
	require.NoError(t, err)

	// Compaction copied the chunk into another pack and deleted the old one.
	newPack := stored[0].Location.PackID + 1
	built, err := pack.Build([]pack.Entry{{Hash: hash, Data: data}})
	require.NoError(t, err)
	store.packs[storage.PackKey(testProject.ID, newPack)] = built.Bytes()
	delete(store.packs, storage.PackKey(testProject.ID, stored[0].Location.PackID))
	locator.locations[ck(hash)] = ChunkLocation{PackID: newPack, Offset: built.Index()[0].Offset}

	got, err := packed.Get(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	// A location that is still current but unreadable stays an error.
	delete(store.packs, storage.PackKey(testProject.ID, newPack))
	_, err = packed.Get(ctx, testProject.ID, hash)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestPackedStore_LookupErrors(t *testing.T) {
	ctx := context.Background()
	packed, locator, _, stored := packedFixture(t, []byte("lookup"))
	locator.err = errors.New("db down")
	hash := domain.Hash{9}
	_, err := packed.Get(ctx, testProject.ID, hash)
	require.ErrorIs(t, err, locator.err)
	_, err = packed.Size(ctx, testProject.ID, hash)
	require.ErrorIs(t, err, locator.err)
	_, err = packed.Exists(ctx, testProject.ID, hash)
	require.ErrorIs(t, err, locator.err)
	require.ErrorIs(t, packed.Prefetch(ctx, testProject.ID, []domain.Hash{hash}), locator.err)

	// A cached location that fails is looked up again; that lookup failing
	// is reported.
	packed.cache.put(testProject.ID, stored[0].Hash, ChunkRecord{SizeBytes: 1, Location: ChunkLocation{PackID: 999, Offset: 4}})
	_, err = packed.Get(ctx, testProject.ID, stored[0].Hash)
	require.ErrorIs(t, err, locator.err)
}

func TestPackedStore_Prefetch(t *testing.T) {
	ctx := context.Background()
	a, b, c := []byte("prefetch a"), []byte("prefetch b"), []byte("prefetch c")
	packed, locator, _, _ := packedFixture(t, a, b, c)
	hashes := []domain.Hash{chunker.Sum(a), chunker.Sum(b), chunker.Sum(c), chunker.Sum(a)}
	require.NoError(t, packed.Prefetch(ctx, testProject.ID, hashes))
	require.Equal(t, int64(1), locator.calls.Load(), "one lookup for many chunks")
	for i, data := range [][]byte{a, b, c} {
		got, err := packed.Get(ctx, testProject.ID, hashes[i])
		require.NoError(t, err)
		require.Equal(t, data, got)
	}
	require.NoError(t, packed.Prefetch(ctx, testProject.ID, hashes))
	require.Equal(t, int64(1), locator.calls.Load(), "everything was cached")
}

func TestPackedReads_PassThrough(t *testing.T) {
	ctx := context.Background()
	repo := newFakeChunkRepo()
	plain := newStubChunkStore()
	packStore := newFakePackStore()
	stores := PackedReads(fakeChunkStores{projects: map[snow.ID]ProjectStore{
		1: {Store: plain},
		2: {Store: packStore, External: true},
		3: {Store: packStore},
	}}, repo)

	resolved, err := stores.ForProject(ctx, 1)
	require.NoError(t, err)
	require.Same(t, plain, resolved.Store, "a store without packs is served unchanged")
	resolved, err = stores.ForProject(ctx, 2)
	require.NoError(t, err)
	require.Same(t, packStore, resolved.Store, "own storage is served unchanged")
	resolved, err = stores.ForProject(ctx, 3)
	require.NoError(t, err)
	require.IsType(t, &packedStore{}, resolved.Store)
	require.Same(t, packStore, unwrapStore(resolved.Store))
	require.Same(t, plain, unwrapStore(plain))

	failing := PackedReads(fakeChunkStores{err: errors.New("resolver down")}, repo)
	_, err = failing.ForProject(ctx, 1)
	require.Error(t, err)
}

func TestProjectChunkReader_Prefetch(t *testing.T) {
	ctx := context.Background()
	repo := newFakeChunkRepo()
	reader := NewProjectChunkReader(PackedReads(fakeChunkStores{projects: map[snow.ID]ProjectStore{
		1: {Store: newStubChunkStore()},
		3: {Store: newFakePackStore()},
	}}, repo))
	require.NoError(t, reader.Prefetch(ctx, 1, []domain.Hash{{1}}), "no-op without packs")
	require.NoError(t, reader.Prefetch(ctx, 3, []domain.Hash{{1}}))

	failing := NewProjectChunkReader(fakeChunkStores{err: errors.New("resolver down")})
	require.Error(t, failing.Prefetch(ctx, 1, nil))
}

func TestLocationCache_Evicts(t *testing.T) {
	cache := newLocationCache(2)
	cache.put(1, domain.Hash{1}, ChunkRecord{SizeBytes: 1})
	cache.put(1, domain.Hash{2}, ChunkRecord{SizeBytes: 2})
	_, ok := cache.get(1, domain.Hash{1})
	require.True(t, ok, "touching an entry keeps it")
	cache.put(1, domain.Hash{3}, ChunkRecord{SizeBytes: 3})
	_, ok = cache.get(1, domain.Hash{2})
	require.False(t, ok, "the least recently used entry is evicted")
	cache.put(1, domain.Hash{1}, ChunkRecord{SizeBytes: 10})
	record, ok := cache.get(1, domain.Hash{1})
	require.True(t, ok)
	require.Equal(t, int64(10), record.SizeBytes, "an update replaces the entry")
	cache.forget(1, domain.Hash{1})
	cache.forget(1, domain.Hash{1})
	_, ok = cache.get(1, domain.Hash{1})
	require.False(t, ok)
}

func TestChunkRecord_Readable(t *testing.T) {
	require.True(t, ChunkRecord{}.Readable(), "a loose row")
	packed := ChunkLocation{PackID: 1}
	require.True(t, ChunkRecord{Location: packed, PackState: ChunkPackStored}.Readable())
	require.True(t, ChunkRecord{Location: packed, PackState: ChunkPackRetired}.Readable())
	require.False(t, ChunkRecord{Location: packed, PackState: ChunkPackPending}.Readable())
	require.False(t, ChunkRecord{Location: packed}.Readable(), "a pack without a row")
	require.Equal(t, chunkproof.Loose(), ChunkLocation{}.proof())
	require.Equal(t, ChunkLocation{}, locationFromProof(chunkproof.Loose()))
}

func TestMergeLoadsPrefetchedContent(t *testing.T) {
	ctx := context.Background()
	data := []byte("merge input\n")
	_, locator, store, stored := packedFixture(t, data)
	reader := NewProjectChunkReader(PackedReads(StaticStores(store), locator))
	b := &Branch{chunks: reader}
	got, err := b.loadMergedContent(ctx, testProject.ID, mergeFileOf(stored[0].Hash))
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.Equal(t, int64(1), locator.calls.Load(), "one prefetch lookup, then cached reads")

	locator.err = errors.New("db down")
	reader = NewProjectChunkReader(PackedReads(StaticStores(store), locator))
	_, err = (&Branch{chunks: reader}).loadMergedContent(ctx, testProject.ID, mergeFileOf(domain.Hash{9}))
	require.ErrorIs(t, err, locator.err)
}

func mergeFileOf(hashes ...domain.Hash) merge.File {
	return merge.File{Path: "a.txt", Encoding: chunker.EncodingRaw, ChunkHashes: hashes}
}

// requireInternal checks a domain error's internal message.
func requireInternal(t *testing.T, err error, substr string) {
	t.Helper()
	var derr *domain.Error
	require.ErrorAs(t, err, &derr)
	require.Contains(t, derr.InternalMessage+" "+derr.Message, substr)
}

// sweepingGuard stands in for a sweep that held the rows Protect waited
// for: when Protect returns, those rows are gone.
type sweepingGuard struct {
	repo *fakeChunkRepo
}

func (g sweepingGuard) Protect(_ context.Context, projectID snow.ID, hashes []domain.Hash) error {
	g.repo.mu.Lock()
	defer g.repo.mu.Unlock()
	for _, hash := range hashes {
		delete(g.repo.rows, objectKey{projectID, hash})
	}
	return nil
}

func TestChunk_PresignUpload_PackedRowSweptDuringProtect(t *testing.T) {
	ctx := context.Background()
	data := []byte("swept while presign waited")
	uc, repo, store := newPackFixture(t)
	repo.recordAt(testProject.ID, chunker.Sum(data), int64(len(data)), ChunkLocation{PackID: 1, Offset: 4})
	repo.states[1] = ChunkPackStored
	uc.WithGCGuard(sweepingGuard{repo: repo})

	presign, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 0, "")
	require.NoError(t, err)
	require.False(t, presign.URLs[0].AlreadyStored, "the swept row is uploaded again")
	require.Zero(t, store.existsCalls)

	repo.recordAt(testProject.ID, chunker.Sum(data), int64(len(data)), ChunkLocation{PackID: 1, Offset: 4})
	uc.WithGCGuard(&failingAfterProtect{repo: repo})
	_, err = uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 0, "")
	require.ErrorContains(t, err, "db down", "the second lookup's error is reported")
}

// failingAfterProtect makes the lookup after Protect fail.
type failingAfterProtect struct {
	repo *fakeChunkRepo
}

func (g *failingAfterProtect) Protect(context.Context, snow.ID, []domain.Hash) error {
	g.repo.sizesErr = errors.New("db down")
	return nil
}
