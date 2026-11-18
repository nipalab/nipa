package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

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
	repo   *MockchunkRepository
	ledger *MockStorageLedger
	nipa   *stubChunkStore
	own    *stubChunkStore
}

func newTwoStoreFixture(t *testing.T, external ProjectStore) twoStoreFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := NewMockchunkRepository(ctrl)
	ledger := NewMockStorageLedger(ctrl)
	nipa, own := newStubChunkStore(), newStubChunkStore()
	external.Store = own
	stores := fakeChunkStores{projects: map[snow.ID]ProjectStore{
		testProject.ID: external,
		snow.ID(2):     {Store: nipa},
	}}
	uc := NewChunk(repo, nipa, ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour, MaxPageSize: 10}).
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
	store.data[hash] = data

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
	f.nipa.data[hash] = data
	f.repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, []domain.Hash{hash}).
		Return(map[domain.Hash]int64{hash: int64(len(data))}, nil)

	urls, _, err := f.uc.PresignUploadURLs(context.Background(), testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")

	require.NoError(t, err)
	require.Len(t, urls, 1)
	require.False(t, urls[0].AlreadyStored)
	require.NotEmpty(t, urls[0].URL)
	require.NotEmpty(t, urls[0].ProofChallenge)
}

func TestChunk_ExternalConfirmSkipsLedger(t *testing.T) {
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	data := []byte("own bucket chunk")
	hash := chunker.Sum(data)
	f.own.data[hash] = data
	f.repo.EXPECT().LinkedProjectChunkSizes(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(map[domain.Hash]int64{}, nil)
	f.repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)
	f.repo.EXPECT().LinkProjectChunks(gomock.Any(), testProject.ID, []domain.Hash{hash}).Return(nil)

	missing, err := f.uc.ConfirmUploads(context.Background(), testOrg, testProject, []domain.Hash{hash}, []ChunkProof{proofFor(t, f.uc, hash, data)})

	require.NoError(t, err)
	require.Empty(t, missing)
}

func TestChunk_ExternalAttributeProjectChunksSkipsLedger(t *testing.T) {
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	hash := chunker.Sum([]byte("merged"))
	f.repo.EXPECT().LinkProjectChunk(gomock.Any(), testProject.ID, hash).Return(nil)

	require.NoError(t, f.uc.AttributeProjectChunks(context.Background(), testProject.ID, []LedgerChunk{{Hash: hash, SizeBytes: 6}}))
}

func TestChunk_StoreForProjectWritesProjectStore(t *testing.T) {
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	data := []byte("merge output")
	hash := chunker.Sum(data)
	f.repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), hash, int64(len(data))).Return(nil)

	isNew, err := f.uc.StoreForProject(context.Background(), testProject.ID, hash, data)

	require.NoError(t, err)
	require.True(t, isNew)
	require.Equal(t, data, f.own.data[hash])
	require.NotContains(t, f.nipa.data, hash)
}

func TestChunk_StoreUploadedAndDownloadUseProjectStore(t *testing.T) {
	ctx := context.Background()
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	data := []byte("signed put")
	hash := chunker.Sum(data)

	require.NoError(t, f.uc.StoreUploaded(ctx, testProject.ID, hash, data))
	require.Equal(t, data, f.own.data[hash])
	require.NotContains(t, f.nipa.data, hash)

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

	_, _, err := f.uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: int64(len(data))}}, 10, "")
	requireReadOnly(t, err)
	_, err = f.uc.ConfirmUploads(ctx, testOrg, testProject, []domain.Hash{hash}, nil)
	requireReadOnly(t, err)
	requireReadOnly(t, f.uc.StoreUploaded(ctx, testProject.ID, hash, data))
	_, err = f.uc.UploadForProject(ctx, testProject.ID, hash, data)
	requireReadOnly(t, err)
	requireReadOnly(t, f.uc.RequireWritable(ctx, testProject.ID))
	require.NoError(t, f.uc.RequireWritable(ctx, snow.ID(2)))

	f.own.data[hash] = data
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
	uc := NewChunk(NewMockchunkRepository(gomock.NewController(t)), newStubChunkStore(), ChunkTransferConfig{SigningKey: linkTestKey, PresignTTL: time.Hour}).
		WithChunkStores(fakeChunkStores{err: wantErr})
	hash := chunker.Sum([]byte("x"))

	_, _, err := uc.PresignUploadURLs(ctx, testOrg, testProject, []ChunkRef{{Hash: hash, SizeBytes: 1}}, 10, "")
	require.ErrorIs(t, err, wantErr)
	_, _, err = uc.PresignDownloadURLs(ctx, testOrg, testProject, []domain.Hash{hash}, 10, "")
	require.ErrorIs(t, err, wantErr)
	_, err = uc.Download(ctx, testProject.ID, hash)
	require.ErrorIs(t, err, wantErr)
	require.ErrorIs(t, uc.AttributeProjectChunks(ctx, testProject.ID, nil), wantErr)
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

func TestBranch_MergedContentUsesProjectStore(t *testing.T) {
	ctx := context.Background()
	f := newTwoStoreFixture(t, ProjectStore{External: true})
	uc, _, _ := newStrategyBranch(t)
	uc.chunks = NewProjectChunkReader(f.uc.ChunkStores())
	uc.WithChunkUploader(f.uc)

	base := []byte("line one\nline two\n")
	baseHash := chunker.Sum(base)
	f.own.data[baseHash] = base
	loaded, err := uc.loadMergedContent(ctx, testProject.ID, merge.File{Encoding: chunker.EncodingRaw, ChunkHashes: []domain.Hash{baseHash}})
	require.NoError(t, err)
	require.Equal(t, base, loaded)
	_, err = uc.loadMergedContent(ctx, snow.ID(2), merge.File{Encoding: chunker.EncodingRaw, ChunkHashes: []domain.Hash{baseHash}})
	require.Error(t, err)

	f.repo.EXPECT().InsertChunkIfNotExists(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	file, stored, err := uc.storeMergedContent(ctx, testProject.ID, "notes.txt", 0o644, []byte("merged text\n"))
	require.NoError(t, err)
	require.NotEmpty(t, stored)
	for _, hash := range file.ChunkHashes {
		require.Contains(t, f.own.data, hash)
		require.NotContains(t, f.nipa.data, hash)
	}
}
