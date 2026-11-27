package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/sync/semaphore"

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

const testSigningKey = "key"

// objectKey is one stored object: chunks are keyed by project and hash.
type objectKey struct {
	project snow.ID
	hash    domain.Hash
}

// ck is the key of hash in testProject.
func ck(hash domain.Hash) objectKey {
	return objectKey{project: testProject.ID, hash: hash}
}

type stubChunkStore struct {
	mu        sync.Mutex
	data      map[objectKey][]byte
	existsErr error
	putErr    error
	getErr    error
	puts      int
	gets      int
}

func newStubChunkStore() *stubChunkStore {
	return &stubChunkStore{data: map[objectKey][]byte{}}
}

func (s *stubChunkStore) Put(_ context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	if s.putErr != nil {
		return s.putErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	s.data[objectKey{projectID, hash}] = data
	return nil
}

func (s *stubChunkStore) Get(_ context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.getErr != nil {
		return nil, s.getErr
	}
	data, ok := s.data[objectKey{projectID, hash}]
	if !ok {
		return nil, domain.NewErrorNotFound("chunk not found")
	}
	return data, nil
}

func (s *stubChunkStore) Size(_ context.Context, projectID snow.ID, hash domain.Hash) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.data[objectKey{projectID, hash}]
	if !ok {
		return 0, domain.NewErrorNotFound("chunk not found")
	}
	return int64(len(data)), nil
}

func (s *stubChunkStore) Exists(_ context.Context, projectID snow.ID, hash domain.Hash) (bool, error) {
	if s.existsErr != nil {
		return false, s.existsErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[objectKey{projectID, hash}]
	return ok, nil
}

func (s *stubChunkStore) Close() error { return nil }

type stubDirectChunkStore struct {
	*stubChunkStore
	downloadURL string
	downloadErr error
	ttl         time.Duration

	uploadTarget  storage.UploadTarget
	uploadErr     error
	uploadSize    int64
	uploadTTL     time.Duration
	uploadProject snow.ID

	deleted   []domain.Hash
	deleteErr error
}

func (s *stubDirectChunkStore) PresignDownload(_ context.Context, _ snow.ID, _ domain.Hash, expires time.Duration) (string, error) {
	if s.downloadErr != nil {
		return "", s.downloadErr
	}
	s.ttl = expires
	return s.downloadURL, nil
}

func (s *stubDirectChunkStore) PresignUpload(_ context.Context, projectID snow.ID, _ domain.Hash, size int64, expires time.Duration) (storage.UploadTarget, error) {
	s.uploadSize = size
	s.uploadTTL = expires
	s.uploadProject = projectID
	if s.uploadErr != nil {
		return storage.UploadTarget{}, s.uploadErr
	}
	return s.uploadTarget, nil
}

func (s *stubDirectChunkStore) DeleteChunk(_ context.Context, projectID snow.ID, hash domain.Hash) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, hash)
	delete(s.data, objectKey{projectID, hash})
	return s.deleteErr
}

// fakeChunkRepo keeps chunk rows per (project, hash) and pack rows like the
// repositories.
type fakeChunkRepo struct {
	mu        sync.Mutex
	rows      map[objectKey]int64
	locations map[objectKey]ChunkLocation
	packs     map[snow.ID]ChunkPack
	states    map[snow.ID]ChunkPackState
	inserts   [][]ChunkRow
	moves     []ChunkRelocation
	insertErr error
	sizesErr  error
	packErr   error
	stateErr  error
	moveErr   error
	// onInsert runs before an insert is recorded (ordering checks).
	onInsert func(ctx context.Context, projectID snow.ID, chunks []ChunkRow)
}

func newFakeChunkRepo() *fakeChunkRepo {
	return &fakeChunkRepo{
		rows:      map[objectKey]int64{},
		locations: map[objectKey]ChunkLocation{},
		packs:     map[snow.ID]ChunkPack{},
		states:    map[snow.ID]ChunkPackState{},
	}
}

func (r *fakeChunkRepo) InsertChunkRows(ctx context.Context, projectID snow.ID, chunks []ChunkRow) error {
	if r.onInsert != nil {
		r.onInsert(ctx, projectID, chunks)
	}
	if r.insertErr != nil {
		return r.insertErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inserts = append(r.inserts, append([]ChunkRow(nil), chunks...))
	for _, chunk := range chunks {
		key := objectKey{projectID, chunk.Hash}
		if _, ok := r.rows[key]; !ok {
			r.rows[key] = chunk.SizeBytes
			r.locations[key] = chunk.Location
		}
	}
	return nil
}

func (r *fakeChunkRepo) ProjectChunkRecords(_ context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]ChunkRecord, error) {
	if r.sizesErr != nil {
		return nil, r.sizesErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	records := make(map[domain.Hash]ChunkRecord, len(hashes))
	for _, hash := range hashes {
		key := objectKey{projectID, hash}
		if size, ok := r.rows[key]; ok {
			record := ChunkRecord{SizeBytes: size, Location: r.locations[key]}
			if record.Location.Packed() {
				record.PackState = r.states[record.Location.PackID]
			}
			records[hash] = record
		}
	}
	return records, nil
}

func (r *fakeChunkRepo) RelocateChunks(_ context.Context, projectID snow.ID, moves []ChunkRelocation) error {
	if r.moveErr != nil {
		return r.moveErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, move := range moves {
		key := objectKey{projectID, move.Hash}
		if r.locations[key] == move.From {
			r.locations[key] = move.To
			r.moves = append(r.moves, move)
		}
	}
	return nil
}

func (r *fakeChunkRepo) InsertChunkPack(_ context.Context, pack ChunkPack) error {
	if r.packErr != nil {
		return r.packErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.packs[pack.ID] = pack
	r.states[pack.ID] = ChunkPackPending
	return nil
}

func (r *fakeChunkRepo) SetChunkPackState(_ context.Context, packID snow.ID, state ChunkPackState) error {
	if r.stateErr != nil {
		return r.stateErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[packID] = state
	return nil
}

func (r *fakeChunkRepo) location(projectID snow.ID, hash domain.Hash) ChunkLocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.locations[objectKey{projectID, hash}]
}

func (r *fakeChunkRepo) recordAt(projectID snow.ID, hash domain.Hash, size int64, location ChunkLocation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[objectKey{projectID, hash}] = size
	r.locations[objectKey{projectID, hash}] = location
}

func (r *fakeChunkRepo) record(projectID snow.ID, hash domain.Hash, size int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[objectKey{projectID, hash}] = size
}

func (r *fakeChunkRepo) has(projectID snow.ID, hash domain.Hash) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.rows[objectKey{projectID, hash}]
	return ok
}

// storeOne stores one chunk of server-held content (StoreChunks).
func storeOne(uc *Chunk, ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	_, err := uc.StoreChunks(ctx, projectID, []ChunkContent{{Hash: hash, Data: data}})
	return err
}

func newChunkFixture(t *testing.T) (*Chunk, *fakeChunkRepo, *storage.LocalStore) {
	t.Helper()
	repo := newFakeChunkRepo()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	uc := NewChunk(repo, store, ChunkTransferConfig{
		SigningKey:  testSigningKey,
		PresignTTL:  time.Hour,
		MaxPageSize: 2,
	})
	uc.now = func() time.Time { return time.Unix(1700000000, 0) }
	return uc, repo, store
}

func newDirectChunkFixture(t *testing.T, store storage.ChunkStore) (*Chunk, *fakeChunkRepo) {
	t.Helper()
	repo := newFakeChunkRepo()
	uc := NewChunk(repo, store, ChunkTransferConfig{
		SigningKey:  testSigningKey,
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

func TestChunk_StoreForProjectStoresWithoutARow(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)

	data := []byte("chunk one")
	hash := chunker.Sum(data)

	require.NoError(t, storeOne(uc, ctx, testProject.ID, hash, data))

	got, err := store.Get(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.False(t, repo.has(testProject.ID, hash), "the row waits for RecordStoredChunks")
}

func TestChunk_StoreForProjectDedupesPerProject(t *testing.T) {
	ctx := context.Background()
	uc, _, store := newChunkFixture(t)

	data := []byte("dedupe me")
	hash := chunker.Sum(data)

	require.NoError(t, storeOne(uc, ctx, testProject.ID, hash, data))
	require.NoError(t, storeOne(uc, ctx, testProject.ID, hash, data), "storing again is harmless")

	require.NoError(t, storeOne(uc, ctx, snow.ID(2), hash, data), "the same bytes in another project are another object")
	ok, err := store.Exists(ctx, snow.ID(2), hash)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestChunk_RecordStoredChunks(t *testing.T) {
	ctx := context.Background()
	uc, repo, _ := newChunkFixture(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc = uc.WithStorageLedger(ledger)

	require.NoError(t, uc.RecordStoredChunks(ctx, testProject.ID, nil), "nothing to attribute")

	chunks := []LedgerChunk{{Hash: domain.Hash{1}, SizeBytes: 1}}
	ledger.EXPECT().AttributeProjectChunks(gomock.Any(), testProject.ID, chunks).DoAndReturn(
		func(context.Context, snow.ID, []LedgerChunk) error {
			require.False(t, repo.has(testProject.ID, domain.Hash{1}), "the ledger runs before the row exists")
			return nil
		})
	location := ChunkLocation{PackID: snow.ID(5), Offset: 4}
	require.NoError(t, uc.RecordStoredChunks(ctx, testProject.ID, []ChunkRow{{Hash: domain.Hash{1}, SizeBytes: 1, Location: location}}))
	require.Equal(t, location, repo.location(testProject.ID, domain.Hash{1}), "the row records where StoreChunks put the bytes")
	require.True(t, repo.has(testProject.ID, domain.Hash{1}))
	require.False(t, repo.has(snow.ID(2), domain.Hash{1}))
}

func TestChunk_UploadHashMismatch(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	require400(t, storeOne(uc, ctx, testProject.ID, domain.Hash{9}, []byte("not matching")), "chunk hash mismatch")
}

func TestChunk_DownloadFound(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	data := []byte("download me")
	hash := chunker.Sum(data)
	require.NoError(t, storeOne(uc, ctx, testProject.ID, hash, data))

	got, err := uc.Download(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	_, err = uc.Download(ctx, snow.ID(2), hash)
	require.True(t, domain.IsErrorNotFound(err), "another project cannot read the object")
}

func TestChunk_DownloadMissing(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	_, err := uc.Download(ctx, testProject.ID, chunker.Sum([]byte("absent")))
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestChunk_PresignUploadURLs(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)

	existing := []byte("already here")
	existingHash := chunker.Sum(existing)
	require.NoError(t, store.Put(ctx, testProject.ID, existingHash, existing))
	repo.record(testProject.ID, existingHash, int64(len(existing)))

	fresh := []byte("fresh chunk")
	freshHash := chunker.Sum(fresh)

	presign, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{
		{Hash: existingHash, SizeBytes: int64(len(existing))},
		{Hash: freshHash, SizeBytes: int64(len(fresh))},
	}, 10, "")
	require.NoError(t, err)
	require.Equal(t, "", presign.NextPageToken)
	urls := presign.URLs
	require.Len(t, urls, 2)

	require.Equal(t, existingHash, urls[0].Hash)
	require.True(t, urls[0].AlreadyStored)
	require.Empty(t, urls[0].URL)

	require.Equal(t, freshHash, urls[1].Hash)
	require.False(t, urls[1].AlreadyStored)
	require.Empty(t, urls[1].URL, "the local store takes uploads in bundles")
	verifyBundleURL(t, uc, "acme", "game", presign, 1)
}

// presignURLs unpacks an upload presign for tests that only look at the
// per-chunk decisions.
func presignURLs(presign UploadPresign, err error) ([]ChunkURL, string, error) {
	return presign.URLs, presign.NextPageToken, err
}

func TestChunk_PresignUploadURLsPaginates(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)

	refs := make([]ChunkRef, 0, 3)
	for _, data := range []string{"one", "two", "three"} {
		hash := chunker.Sum([]byte(data))
		refs = append(refs, ChunkRef{Hash: hash, SizeBytes: int64(len(data))})
	}

	page1, next, err := presignURLs(uc.PresignUploadURLs(ctx, testOrg, testProject, refs, 10, ""))
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, "2", next)
	require.Equal(t, refs[0].Hash, page1[0].Hash)
	require.Equal(t, refs[1].Hash, page1[1].Hash)

	page2, next2, err := presignURLs(uc.PresignUploadURLs(ctx, testOrg, testProject, refs, 10, next))
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Empty(t, next2)
	require.Equal(t, refs[2].Hash, page2[0].Hash)
}

func TestChunk_PresignUploadURLsInvalid(t *testing.T) {
	ctx := context.Background()
	uc, _, _ := newChunkFixture(t)
	hash := chunker.Sum([]byte("x"))

	_, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 0}}, 10, "")
	require400(t, err, "invalid chunk size")

	_, err = uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: chunker.MaxChunkSize() + 1}}, 10, "")
	require400(t, err, "invalid chunk size")

	urls, _, err := presignURLs(uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: chunker.PackedAssetConfig.Max}}, 10, ""))
	require.NoError(t, err)
	require.Len(t, urls, 1)

	_, err = uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 10}}, 10, "-1")
	require400(t, err, "invalid page token")
}

func TestChunk_PresignUploadURLsRowLookupError(t *testing.T) {
	uc, repo, _ := newChunkFixture(t)
	repo.sizesErr = errors.New("db down")

	_, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: domain.Hash{1}, SizeBytes: 1}}, 10, "")
	require.ErrorIs(t, err, repo.sizesErr)
}

func TestChunk_PresignDownloadURLs(t *testing.T) {
	uc, _, _ := newChunkFixture(t)
	h1 := chunker.Sum([]byte("one"))
	h2 := chunker.Sum([]byte("two"))

	urls, next, err := uc.PresignDownloadURLs(context.Background(), &domain.Organization{Slug: "acme"}, &domain.Project{ID: snow.ID(1), Slug: "game"}, []domain.Hash{h1, h2}, 10, "", ChunkReadProject)
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
		downloadURL:    "https://s3.example.com/nipa-chunks/p/1/ab/cdef?X-Amz-Signature=sig",
	}
	uc, _ := newDirectChunkFixture(t, store)

	h1 := chunker.Sum([]byte("one"))
	h2 := chunker.Sum([]byte("two"))
	urls, next, err := uc.PresignDownloadURLs(context.Background(), &domain.Organization{Slug: "acme"}, &domain.Project{ID: snow.ID(1), Slug: "game"}, []domain.Hash{h1, h2}, 10, "", ChunkReadProject)
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

	_, _, err := uc.PresignDownloadURLs(context.Background(), &domain.Organization{Slug: "acme"}, &domain.Project{ID: snow.ID(1), Slug: "game"}, []domain.Hash{chunker.Sum([]byte("x"))}, 10, "", ChunkReadProject)
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
	urls, next, err := presignURLs(uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, ""))
	require.NoError(t, err)
	require.Empty(t, next)
	require.Len(t, urls, 1)
	require.Equal(t, hash, urls[0].Hash)
	require.Equal(t, store.uploadTarget.URL, urls[0].URL)
	require.Equal(t, store.uploadTarget.Headers, urls[0].Headers)
	require.Equal(t, int64(len(data)), store.uploadSize)
	require.Equal(t, time.Hour, store.uploadTTL)
	require.Equal(t, testProject.ID, store.uploadProject, "the target writes the project's key")
}

func TestChunk_PresignUploadURLsDirectStoreAlreadyStored(t *testing.T) {
	data := []byte("already here")
	store := &stubDirectChunkStore{stubChunkStore: newStubChunkStore()}
	store.data[ck(chunker.Sum(data))] = data
	uc, repo := newDirectChunkFixture(t, store)
	repo.record(testProject.ID, chunker.Sum(data), int64(len(data)))

	urls, _, err := presignURLs(uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 10, ""))
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
	_, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 10, "")
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
	require.NoError(t, store.Put(ctx, testProject.ID, presentHash, present))

	absentHash := chunker.Sum([]byte("absent"))

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{presentHash, absentHash, presentHash}, nil)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{absentHash}, missing)
	require.Equal(t, [][]ChunkRow{{{Hash: presentHash, SizeBytes: int64(len(present))}}}, repo.inserts, "one row per hash, measured by the server")
}

func TestChunk_UploadStoreErrors(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("put", func(t *testing.T) {
		store := &stubChunkStore{data: map[objectKey][]byte{}, putErr: errors.New("boom")}
		uc := NewChunk(newFakeChunkRepo(), store, ChunkTransferConfig{})
		require.Error(t, storeOne(uc, ctx, testProject.ID, hash, data))
		require.Error(t, uc.UploadForProject(ctx, testProject.ID, hash, data))
	})

	t.Run("row", func(t *testing.T) {
		repo := newFakeChunkRepo()
		repo.insertErr = errors.New("boom")
		uc := NewChunk(repo, newStubChunkStore(), ChunkTransferConfig{})
		require.ErrorIs(t, uc.UploadForProject(ctx, testProject.ID, hash, data), repo.insertErr)
	})
}

func TestChunk_VerifyTransferURL(t *testing.T) {
	uc, _, _ := newChunkFixture(t)

	data := []byte("payload")
	hash := chunker.Sum(data)
	expiry := uc.now().Add(uc.transfer.PresignTTL).Unix()
	path := chunkurl.DownloadPath(uc.transfer.SigningKey, "acme", "game", hash.String(), expiry)

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
	store := &stubChunkStore{data: map[objectKey][]byte{}, existsErr: errors.New("boom")}
	repo := newFakeChunkRepo()
	uc := NewChunk(repo, store, ChunkTransferConfig{
		SigningKey:  testSigningKey,
		PresignTTL:  time.Hour,
		MaxPageSize: 10,
	})

	data := []byte("payload")
	repo.record(testProject.ID, chunker.Sum(data), int64(len(data))) // a row, so presign checks the store
	_, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: chunker.Sum(data), SizeBytes: int64(len(data))}}, 10, "")
	require.Error(t, err)
}

func TestChunk_PresignDownloadURLsInvalidToken(t *testing.T) {
	uc, _, _ := newChunkFixture(t)

	_, _, err := uc.PresignDownloadURLs(context.Background(), &domain.Organization{Slug: "acme"}, &domain.Project{ID: snow.ID(1), Slug: "game"}, []domain.Hash{chunker.Sum([]byte("x"))}, 10, "bad", ChunkReadProject)
	require400(t, err, "invalid page token")
}

func TestChunk_ConfirmUploadsRowErrors(t *testing.T) {
	ctx := context.Background()
	data := []byte("payload")
	hash := chunker.Sum(data)

	t.Run("insert", func(t *testing.T) {
		store := newStubChunkStore()
		store.data[ck(hash)] = data
		repo := newFakeChunkRepo()
		repo.insertErr = errors.New("boom")
		uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour})

		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
		require.EqualError(t, err, "boom")
	})

	t.Run("lookup", func(t *testing.T) {
		repo := newFakeChunkRepo()
		repo.sizesErr = errors.New("db down")
		uc := NewChunk(repo, newStubChunkStore(), ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour})

		_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
		require.ErrorIs(t, err, repo.sizesErr)
	})
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

	_, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	require.ErrorIs(t, err, quotaErr)
}

func TestChunk_PresignUploadURLsLedgerSkipsAttributedRefs(t *testing.T) {
	ctx := context.Background()
	uc, repo, store := newChunkFixture(t)
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc = uc.WithStorageLedger(ledger)

	data := []byte("known bytes")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(ctx, testProject.ID, hash, data))
	repo.record(testProject.ID, hash, int64(len(data)))

	ledger.EXPECT().AttributedSizes(gomock.Any(), testProject.ID, []domain.Hash{hash}).
		Return(map[domain.Hash]int64{hash: int64(len(data))}, nil)

	urls, _, err := presignURLs(uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, ""))
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

	_, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 4}}, 10, "")
	require.ErrorIs(t, err, wantErr)
}

func TestChunk_ConfirmUploadsLedgerAttributesNewRows(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	data := []byte("attribute me")
	hash := chunker.Sum(data)
	store.data[ck(hash)] = data
	known := []byte("known")
	knownHash := chunker.Sum(known)
	store.data[ck(knownHash)] = known
	absentHash := chunker.Sum([]byte("absent"))

	repo := newFakeChunkRepo()
	repo.record(testProject.ID, knownHash, int64(len(known)))
	ledger := NewMockStorageLedger(gomock.NewController(t))
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject,
		[]LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}}).Return(nil)
	uc := NewChunk(repo, store, ChunkTransferConfig{}).WithStorageLedger(ledger)

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash, knownHash, absentHash}, nil)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{absentHash}, missing)
	require.True(t, repo.has(testProject.ID, hash))
}

func TestChunk_ConfirmUploadsLedgerQuotaErrorLeavesNoRow(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	data := []byte("over quota")
	hash := chunker.Sum(data)
	store.data[ck(hash)] = data

	repo := newFakeChunkRepo()
	quotaErr := domain.NewErrorQuotaExceeded("organization storage quota exceeded")
	ledger := NewMockStorageLedger(gomock.NewController(t))
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject,
		[]LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}}).Return(quotaErr)
	uc := NewChunk(repo, store, ChunkTransferConfig{}).WithStorageLedger(ledger)

	_, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
	require.ErrorIs(t, err, quotaErr)
	require.False(t, repo.has(testProject.ID, hash), "a refused quota leaves no row")
	require.Empty(t, repo.inserts)
}

type countingChunkStore struct {
	*stubChunkStore
	existsCalls atomic.Int64
	sizeCalls   atomic.Int64
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	existsDelay time.Duration
}

func (s *countingChunkStore) Exists(ctx context.Context, projectID snow.ID, hash domain.Hash) (bool, error) {
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
	return s.stubChunkStore.Exists(ctx, projectID, hash)
}

func (s *countingChunkStore) Size(ctx context.Context, projectID snow.ID, hash domain.Hash) (int64, error) {
	s.sizeCalls.Add(1)
	return s.stubChunkStore.Size(ctx, projectID, hash)
}

// TestChunk_PresignUploadURLs_DecisionTable checks the per-project presign
// table: no row -> upload (no store check), row and object -> already
// stored, row but object lost -> upload again. The checks run concurrently
// within the shared limit.
func TestChunk_PresignUploadURLs_DecisionTable(t *testing.T) {
	store := &countingChunkStore{stubChunkStore: newStubChunkStore(), existsDelay: 2 * time.Millisecond}
	repo := newFakeChunkRepo()
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: "k", PresignTTL: time.Hour, MaxPageSize: 100})
	uc.existsSlots = semaphore.NewWeighted(4)

	refs := make([]ChunkRef, 0, 60)
	rows := 0
	for i := 0; i < 60; i++ {
		data := []byte(fmt.Sprintf("chunk-%d", i))
		hash := chunker.Sum(data)
		refs = append(refs, ChunkRef{Hash: hash, SizeBytes: int64(len(data))})
		switch i % 4 {
		case 0: // row and object
			repo.record(testProject.ID, hash, int64(len(data)))
			store.data[ck(hash)] = data
			rows++
		case 1: // row, object lost
			repo.record(testProject.ID, hash, int64(len(data)))
			rows++
		case 2: // object without a row (an upload whose confirm never ran)
			store.data[ck(hash)] = data
		case 3: // another project's row and object
			repo.record(snow.ID(2), hash, int64(len(data)))
			store.data[objectKey{snow.ID(2), hash}] = data
		}
	}

	urls, _, err := presignURLs(uc.PresignUploadURLs(context.Background(), testOrg, testProject, refs, 100, ""))
	require.NoError(t, err)
	require.Len(t, urls, len(refs))
	require.Equal(t, int64(rows), store.existsCalls.Load(), "only hashes with a row in the project are checked")
	require.LessOrEqual(t, store.maxInFlight.Load(), int64(4))
	require.Greater(t, store.maxInFlight.Load(), int64(1))
	for i, u := range urls {
		require.Equal(t, refs[i].Hash, u.Hash)
		if i%4 == 0 {
			require.True(t, u.AlreadyStored)
			require.Empty(t, u.URL)
			continue
		}
		require.False(t, u.AlreadyStored, "case %d needs an upload", i%4)
		require.Empty(t, u.URL, "bundled")
	}
}

func TestChunk_PresignUploadURLs_ExistsErrorIsADatabaseError(t *testing.T) {
	store := &stubChunkStore{data: map[objectKey][]byte{}, existsErr: errors.New("boom")}
	repo := newFakeChunkRepo()
	refs := []ChunkRef{{Hash: chunker.Sum([]byte("a")), SizeBytes: 1}, {Hash: chunker.Sum([]byte("b")), SizeBytes: 1}}
	repo.record(testProject.ID, refs[0].Hash, 1)
	repo.record(testProject.ID, refs[1].Hash, 1)
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: "k", PresignTTL: time.Hour, MaxPageSize: 10})

	_, err := uc.PresignUploadURLs(context.Background(), testOrg, testProject, refs, 10, "")
	var domainErr *domain.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, domain.NewErrorDatabase("").Code, domainErr.Code)
	require.Contains(t, domainErr.InternalMessage, "boom")
}

func TestChunk_ConfirmUploads_RecordedChunksUseStoredSizes(t *testing.T) {
	ctx := context.Background()
	recordedData := []byte("recorded content")
	recordedHash := chunker.Sum(recordedData)
	freshData := []byte("fresh content")
	freshHash := chunker.Sum(freshData)
	absentHash := chunker.Sum([]byte("absent"))

	store := &countingChunkStore{stubChunkStore: newStubChunkStore()}
	store.data[ck(freshHash)] = freshData
	store.data[ck(recordedHash)] = recordedData
	repo := newFakeChunkRepo()
	ledger := NewMockStorageLedger(gomock.NewController(t))
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour}).WithStorageLedger(ledger)
	metrics := obs.NewMetrics()
	uc = uc.WithMetrics(metrics)

	const storedSize = 1234
	repo.record(testProject.ID, recordedHash, storedSize)
	ledger.EXPECT().Attribute(gomock.Any(), testOrg, testProject, []LedgerChunk{
		{Hash: freshHash, SizeBytes: int64(len(freshData))},
	}).Return(nil)

	missing, err := uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{recordedHash, freshHash, absentHash}, nil)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{absentHash}, missing)
	require.Zero(t, store.sizeCalls.Load())
	require.Equal(t, 3, store.gets, "every chunk without a receipt is read back, recorded or not")
	require.Contains(t, scrapeUsecaseMetrics(t, metrics), fmt.Sprintf(`nipa_chunk_bytes_total{op="confirm"} %d`, storedSize+len(freshData)))
}

type gettingChunkStore struct {
	*stubChunkStore
	inFlight    atomic.Int64
	maxInFlight atomic.Int64
}

func (s *gettingChunkStore) Get(_ context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		peak := s.maxInFlight.Load()
		if n <= peak || s.maxInFlight.CompareAndSwap(peak, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[objectKey{projectID, hash}], nil
}

func TestChunk_ConfirmUploads_BoundsConcurrentVerificationReads(t *testing.T) {
	store := &gettingChunkStore{stubChunkStore: newStubChunkStore()}
	hashes := make([]domain.Hash, 0, 30)
	for i := 0; i < 30; i++ {
		data := []byte(fmt.Sprintf("verify-%d", i))
		hash := chunker.Sum(data)
		store.data[ck(hash)] = data
		hashes = append(hashes, hash)
	}
	repo := newFakeChunkRepo()
	uc := NewChunk(repo, store, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour}).WithVerifyLimit(3)

	missing, err := uc.ConfirmUploads(context.Background(), testOrg, testProject, hashes, nil)
	require.NoError(t, err)
	require.Empty(t, missing)
	require.LessOrEqual(t, store.maxInFlight.Load(), int64(3))
	require.Greater(t, store.maxInFlight.Load(), int64(1))
}
