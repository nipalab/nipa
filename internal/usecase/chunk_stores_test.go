package usecase

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeChunkStores struct {
	projects map[snow.ID]ProjectStore
	err      error
}

func (f fakeChunkStores) ForProject(_ context.Context, projectID snow.ID) (ProjectStore, error) {
	if f.err != nil {
		return ProjectStore{}, f.err
	}
	return f.projects[projectID], nil
}

type twoStoreFixture struct {
	uc     *Chunk
	repo   *fakeChunkRepo
	ledger *MockStorageLedger
	nipa   *stubChunkStore
	own    *stubChunkStore
}

func newTwoStoreFixture(t *testing.T, external ProjectStore) twoStoreFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := newFakeChunkRepo()
	ledger := NewMockStorageLedger(ctrl)
	nipa, own := newStubChunkStore(), newStubChunkStore()
	external.Store = own
	stores := fakeChunkStores{projects: map[snow.ID]ProjectStore{
		testProject.ID: external,
		snow.ID(2):     {Store: nipa},
	}}
	uc := NewChunk(repo, nipa, ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour, MaxPageSize: 10}).
		WithChunkStores(stores).WithStorageLedger(ledger)
	return twoStoreFixture{uc: uc, repo: repo, ledger: ledger, nipa: nipa, own: own}
}

func TestStaticStores(t *testing.T) {
	store := newStubChunkStore()
	resolved, err := StaticStores(store).ForProject(context.Background(), snow.ID(9))
	require.NoError(t, err)
	require.Same(t, store, resolved.Store)
	require.False(t, resolved.External)
	require.False(t, resolved.ReadOnly)
}

func TestProjectChunkReader(t *testing.T) {
	ctx := context.Background()
	store := newStubChunkStore()
	data := []byte("content")
	hash := chunker.Sum(data)
	store.data[ck(hash)] = data

	got, err := NewProjectChunkReader(StaticStores(store)).Get(ctx, snow.ID(1), hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	wantErr := errors.New("resolver down")
	_, err = NewProjectChunkReader(fakeChunkStores{err: wantErr}).Get(ctx, snow.ID(1), hash)
	require.ErrorIs(t, err, wantErr)
}

func TestChunk_ChunkStoresAccessor(t *testing.T) {
	stores := fakeChunkStores{}
	uc := NewChunk(nil, newStubChunkStore(), ChunkTransferConfig{}).WithChunkStores(stores)
	require.Equal(t, stores, uc.ChunkStores())
}

func TestChunk_PresignExistsIsPerStore(t *testing.T) {
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	data := []byte("held by nipa only")
	hash := chunker.Sum(data)
	f.nipa.data[ck(hash)] = data
	f.repo.record(testProject.ID, hash, int64(len(data)))

	got, err := f.uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")

	require.NoError(t, err)
	require.Len(t, got.URLs, 1)
	require.False(t, got.URLs[0].AlreadyStored)
	require.NotEmpty(t, got.BundleURL, "the project's own store lost the object: upload it again")
}

func TestChunk_ExternalConfirmSkipsLedger(t *testing.T) {
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	data := []byte("own bucket chunk")
	hash := chunker.Sum(data)
	f.own.data[ck(hash)] = data

	missing, err := f.uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, nil)

	require.NoError(t, err)
	require.Empty(t, missing)
	require.True(t, f.repo.has(testProject.ID, hash), "own storage still records the project's rows")
}

func TestChunk_ExternalAttributeProjectChunksSkipsLedger(t *testing.T) {
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	hash := chunker.Sum([]byte("merged"))

	require.NoError(t, f.uc.AttributeProjectChunks(context.Background(), testProject.ID, []LedgerChunk{{Hash: hash, SizeBytes: 6}}))
	require.True(t, f.repo.has(testProject.ID, hash))
}

func TestChunk_StoreForProjectWritesProjectStore(t *testing.T) {
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	data := []byte("merge output")
	hash := chunker.Sum(data)

	err := f.uc.StoreForProject(context.Background(), testProject.ID, hash, data)

	require.NoError(t, err)
	require.Equal(t, data, f.own.data[ck(hash)])
	require.NotContains(t, f.nipa.data, ck(hash))
}

func TestChunk_StoreBundleAndDownloadUseProjectStore(t *testing.T) {
	ctx := context.Background()
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	data := []byte("signed put")
	hash := chunker.Sum(data)

	body := bundleOf(t, data)
	_, err := f.uc.StoreBundle(ctx, testProject.ID, bytes.NewReader(body), int64(len(body)), 1)
	require.NoError(t, err)
	require.Equal(t, data, f.own.data[ck(hash)])
	require.NotContains(t, f.nipa.data, ck(hash))

	got, err := f.uc.Download(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)

	_, err = f.uc.Download(ctx, snow.ID(2), hash)
	require.Error(t, err)
}

func TestChunk_ReadOnlyRefusesWrites(t *testing.T) {
	ctx := context.Background()
	f := newTwoStoreFixture(t, ProjectStore{External: true, ReadOnly: true})
	data := []byte("blocked")
	hash := chunker.Sum(data)
	requireReadOnly := func(t *testing.T, err error) {
		t.Helper()
		var domainErr *domain.Error
		require.ErrorAs(t, err, &domainErr)
		require.Equal(t, 402, domainErr.Code)
		require.Equal(t, "own storage requires the Own storage add-on", domainErr.Message)
	}

	_, err := f.uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	requireReadOnly(t, err)
	_, err = f.uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
	requireReadOnly(t, err)
	body := bundleOf(t, data)
	_, err = f.uc.StoreBundle(ctx, testProject.ID, bytes.NewReader(body), int64(len(body)), 1)
	requireReadOnly(t, err)
	requireReadOnly(t, f.uc.UploadForProject(ctx, testProject.ID, hash, data))
	requireReadOnly(t, f.uc.RequireWritable(ctx, testProject.ID))
	require.NoError(t, f.uc.RequireWritable(ctx, snow.ID(2)))

	f.own.data[ck(hash)] = data
	got, err := f.uc.Download(ctx, testProject.ID, hash)
	require.NoError(t, err)
	require.Equal(t, data, got)
	urls, _, err := f.uc.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{hash}, 10, "")
	require.NoError(t, err)
	require.Len(t, urls, 1)
}

func TestChunk_ResolverErrors(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("resolver down")
	uc := NewChunk(newFakeChunkRepo(), newStubChunkStore(), ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour}).
		WithChunkStores(fakeChunkStores{err: wantErr})
	hash := chunker.Sum([]byte("x"))

	_, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 1}}, 10, "")
	require.ErrorIs(t, err, wantErr)
	_, _, err = uc.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{hash}, 10, "")
	require.ErrorIs(t, err, wantErr)
	_, err = uc.Download(ctx, testProject.ID, hash)
	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, uc.AttributeProjectChunks(ctx, testProject.ID, []LedgerChunk{{Hash: hash, SizeBytes: 1}}), wantErr)
	require.ErrorIs(t, uc.RequireWritable(ctx, testProject.ID), wantErr)
}

type stubStorageGate struct {
	err      error
	projects []snow.ID
}

func (g *stubStorageGate) RequireWritable(_ context.Context, projectID snow.ID) error {
	g.projects = append(g.projects, projectID)
	return g.err
}

func TestPush_StorageGateRefuses(t *testing.T) {
	uc, perm, _, _, ctx := newPushFixture(t)
	gate := &stubStorageGate{err: ErrStorageReadOnly()}
	uc.WithStorageGate(gate)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).AnyTimes()

	err := uc.CheckTarget(ctx, snow.ID(1), "main", "msg", "")
	require.ErrorIs(t, err, gate.err)
	_, err = uc.Push(ctx, snow.ID(1), "main", "", "msg", nil, nil, "", "")
	require.ErrorIs(t, err, gate.err)
	require.Equal(t, []snow.ID{1, 1}, gate.projects)
}

func TestBranch_StorageGateRefusesMerges(t *testing.T) {
	ctx := context.Background()
	uc, perm, _ := newStrategyBranch(t)
	gate := &stubStorageGate{err: ErrStorageReadOnly()}
	uc.WithStorageGate(gate)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).AnyTimes()

	_, err := uc.FastForward(ctx, snow.ID(1), "main", "feature")
	require.ErrorIs(t, err, gate.err)
	_, err = uc.FastForwardForMergeRequest(ctx, snow.ID(1), "main", "feature")
	require.ErrorIs(t, err, gate.err)
	for _, strategy := range []string{domain.MergeStrategyFastForward, domain.MergeStrategyMerge, domain.MergeStrategySquash, domain.MergeStrategyRebase} {
		_, err = uc.MergeForMergeRequest(ctx, snow.ID(1), "main", "feature", MergeCommitOptions{Strategy: strategy})
		require.ErrorIs(t, err, gate.err, strategy)
	}
}

func TestBranch_MergedContentDecodesChunkByChunk(t *testing.T) {
	ctx := context.Background()
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	uc, _, _ := newStrategyBranch(t)
	uc.chunks = NewProjectChunkReader(f.uc.ChunkStores())

	// Two compressed chunks whose combined plaintext is over the per-chunk
	// decode cap: decoding the joined bytes in one call would be refused.
	part := bytes.Repeat([]byte("a"), chunker.TextSingleBlobRawMax)
	enc, err := zstd.NewWriter(nil)
	require.NoError(t, err)
	var hashes []domain.Hash
	for range 2 {
		data := enc.EncodeAll(part, nil)
		h := chunker.Sum(data)
		f.own.data[ck(h)] = data
		hashes = append(hashes, h)
	}
	loaded, err := uc.loadMergedContent(ctx, testProject.ID, merge.File{Encoding: chunker.EncodingZstd, ChunkHashes: hashes})
	require.NoError(t, err)
	require.Len(t, loaded, 2*len(part))
}

func TestBranch_MergedContentUsesProjectStore(t *testing.T) {
	ctx := context.Background()
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	uc, _, _ := newStrategyBranch(t)
	uc.chunks = NewProjectChunkReader(f.uc.ChunkStores())
	uc.WithChunkUploader(f.uc)

	base := []byte("line one\nline two\n")
	baseHash := chunker.Sum(base)
	f.own.data[ck(baseHash)] = base
	loaded, err := uc.loadMergedContent(ctx, testProject.ID, merge.File{Encoding: chunker.EncodingRaw, ChunkHashes: []domain.Hash{baseHash}})
	require.NoError(t, err)
	require.Equal(t, base, loaded)
	_, err = uc.loadMergedContent(ctx, snow.ID(2), merge.File{Encoding: chunker.EncodingRaw, ChunkHashes: []domain.Hash{baseHash}})
	require.Error(t, err)

	file, stored, err := uc.storeMergedContent(ctx, testProject.ID, "notes.txt", 0o644, []byte("merged text\n"))
	require.NoError(t, err)
	require.NotEmpty(t, stored)
	for _, hash := range file.ChunkHashes {
		require.Contains(t, f.own.data, ck(hash))
		require.NotContains(t, f.nipa.data, ck(hash))
	}
}

func TestChunk_CheckUploadHeadroom(t *testing.T) {
	ctx := context.Background()
	nipaProject := &domain.Project{ID: snow.ID(2), OrgID: testOrg.ID}

	f := newTwoStoreFixture(t, ProjectStore{External: true})
	checked, err := f.uc.CheckUploadHeadroom(ctx, testOrg, testProject, 10)
	require.NoError(t, err)
	require.False(t, checked)

	checked, err = f.uc.CheckUploadHeadroom(ctx, testOrg, nipaProject, 0)
	require.NoError(t, err)
	require.True(t, checked)

	f.ledger.EXPECT().Headroom(gomock.Any(), testOrg, nipaProject, int64(10)).Return(nil)
	checked, err = f.uc.CheckUploadHeadroom(ctx, testOrg, nipaProject, 10)
	require.NoError(t, err)
	require.True(t, checked)

	quotaErr := domain.NewErrorQuotaExceeded("storage quota exceeded")
	f.ledger.EXPECT().Headroom(gomock.Any(), testOrg, nipaProject, int64(20)).Return(quotaErr)
	checked, err = f.uc.CheckUploadHeadroom(ctx, testOrg, nipaProject, 20)
	require.True(t, checked)
	require.ErrorIs(t, err, quotaErr)

	readOnly := newTwoStoreFixture(t, ProjectStore{External: true, ReadOnly: true})
	_, err = readOnly.uc.CheckUploadHeadroom(ctx, testOrg, testProject, 10)
	require.ErrorContains(t, err, "Own storage add-on")

	noLedger := NewChunk(newFakeChunkRepo(), newStubChunkStore(), ChunkTransferConfig{})
	checked, err = noLedger.CheckUploadHeadroom(ctx, testOrg, testProject, 10)
	require.NoError(t, err)
	require.False(t, checked)
}
