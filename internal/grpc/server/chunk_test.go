package server

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunkbundle"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
)

const testSigningKey = "test-signing-key"

// testChunkProject is the project newTestCommon resolves org/proj to.
const testChunkProject = snow.ID(42)

func newChunkUsecase(t *testing.T) (*usecase.Chunk, *storage.LocalStore, *stubChunkRepo) {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	repo := newStubChunkRepo()
	uc := usecase.NewChunk(repo, store, usecase.ChunkTransferConfig{
		SigningKey:  testSigningKey,
		PresignTTL:  time.Hour,
		MaxPageSize: 100,
	})
	return uc, store, repo
}

type directStubStore struct {
	storage.ChunkStore
}

func (directStubStore) PresignDownload(context.Context, snow.ID, domain.Hash, time.Duration) (string, error) {
	return "https://s3.example.com/bucket/chunk?X-Amz-Signature=x", nil
}

func (directStubStore) PresignUpload(context.Context, snow.ID, domain.Hash, int64, time.Duration) (storage.UploadTarget, error) {
	return storage.UploadTarget{
		URL:     "https://s3.example.com/bucket/p/16/ab/cdef?X-Amz-Signature=x",
		Headers: map[string]string{"If-None-Match": "*"},
	}, nil
}

func (s directStubStore) DeleteChunk(context.Context, snow.ID, domain.Hash) error {
	return nil
}

func newDirectChunkUsecase(t *testing.T) (*usecase.Chunk, *storage.LocalStore) {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	uc := usecase.NewChunk(newStubChunkRepo(), directStubStore{ChunkStore: store}, usecase.ChunkTransferConfig{
		SigningKey:  testSigningKey,
		PresignTTL:  time.Hour,
		MaxPageSize: 100,
	})
	return uc, store
}

type chunkRowKey struct {
	project snow.ID
	hash    domain.Hash
}

// stubChunkRepo keeps the project's chunk rows like the repositories do.
type stubChunkRepo struct {
	mu   sync.Mutex
	rows map[chunkRowKey]int64
}

func newStubChunkRepo() *stubChunkRepo {
	return &stubChunkRepo{rows: map[chunkRowKey]int64{}}
}

func (r *stubChunkRepo) InsertChunkRows(_ context.Context, projectID snow.ID, chunks []usecase.ChunkRow) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, chunk := range chunks {
		if _, ok := r.rows[chunkRowKey{projectID, chunk.Hash}]; !ok {
			r.rows[chunkRowKey{projectID, chunk.Hash}] = chunk.SizeBytes
		}
	}
	return nil
}

func (r *stubChunkRepo) ProjectChunkRecords(_ context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]usecase.ChunkRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	records := make(map[domain.Hash]usecase.ChunkRecord, len(hashes))
	for _, hash := range hashes {
		if size, ok := r.rows[chunkRowKey{projectID, hash}]; ok {
			records[hash] = usecase.ChunkRecord{SizeBytes: size}
		}
	}
	return records, nil
}

func (r *stubChunkRepo) RelocateChunks(context.Context, snow.ID, []usecase.ChunkRelocation) error {
	return nil
}

func (r *stubChunkRepo) InsertChunkPack(context.Context, usecase.ChunkPack) error { return nil }

func (r *stubChunkRepo) SetChunkPackState(context.Context, snow.ID, usecase.ChunkPackState) error {
	return nil
}

func (r *stubChunkRepo) has(projectID snow.ID, hash domain.Hash) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.rows[chunkRowKey{projectID, hash}]
	return ok
}

func testChunkContext() *pb.ProjectContext {
	return &pb.ProjectContext{Org: "org", Project: "proj"}
}

func verifySignedURL(t *testing.T, raw, org, project, op string, size int64) {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	params, err := chunkurl.ParseParams(u.Query())
	require.NoError(t, err)
	require.Equal(t, op, params.Op)
	require.Equal(t, size, params.Size)
	hash := u.Path[strings.LastIndex(u.Path, "/")+1:]
	require.NoError(t, chunkurl.Verify(testSigningKey, org, project, hash, params, time.Now()))
}

func newVisibleChunkBranch(t *testing.T, visible domain.Hash) *usecase.Branch {
	t.Helper()

	branch, perm, repo := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), snow.ID(7)).
		Return(&domain.Commit{ID: 7, ProjectID: 42, TreeID: 5}, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(5)).
		Return(&domain.TreeNode{ID: 5, Name: "root"}, nil)
	repo.EXPECT().
		ListFileMetaByTrees(gomock.Any(), []int64{5}).
		Return(map[int64][]*domain.File{5: {{ID: 1, Name: "a.txt", TreeID: 5}}}, nil)
	repo.EXPECT().
		ListChunksByFiles(gomock.Any(), []int64{1}).
		Return(map[int64][]domain.Chunk{1: {{ID: 1, Hash: visible}}}, nil)
	repo.EXPECT().
		ListTreeChildrenByTrees(gomock.Any(), []int64{5}).
		Return(nil, nil)
	return branch
}

func TestGetChunkUploadUrlsHandler(t *testing.T) {
	chunk, store, repo := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	stored := []byte("stored chunk")
	storedHash := chunker.Sum(stored)
	require.NoError(t, store.Put(context.Background(), testChunkProject, storedHash, stored))
	require.NoError(t, repo.InsertChunkRows(context.Background(), testChunkProject, []usecase.ChunkRow{{Hash: storedHash, SizeBytes: int64(len(stored))}}))

	fresh := []byte("fresh chunk")
	freshHash := chunker.Sum(fresh)

	res, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context: testChunkContext(),
		Chunks: []*pb.ChunkRef{
			{Hash: storedHash.String(), SizeBytes: int64(len(stored))},
			{Hash: freshHash.String(), SizeBytes: int64(len(fresh))},
		},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.NoError(t, err)
	require.Equal(t, chunkproof.Protocol, res.GetChunkProtocol())
	require.Len(t, res.GetUrls(), 2)

	require.True(t, res.GetUrls()[0].GetAlreadyStored())
	require.Empty(t, res.GetUrls()[0].GetUrl())

	require.False(t, res.GetUrls()[1].GetAlreadyStored())
	require.Empty(t, res.GetUrls()[1].GetUrl(), "the local store takes uploads in bundles")

	u, err := url.Parse(res.GetBundleUrl())
	require.NoError(t, err)
	require.Equal(t, "/api/chunk-bundles/org/proj", u.Path)
	params, err := chunkurl.ParseBundleParams(u.Query())
	require.NoError(t, err)
	require.Equal(t, res.GetBundleMaxBytes(), params.MaxSize)
	require.Equal(t, int64(usecase.DefaultChunkBundleMaxBytes), params.MaxSize)
	require.Equal(t, int(res.GetBundleMaxChunks()), params.MaxCount)
	require.Equal(t, 1, params.MaxCount, "one chunk needs an upload")
	require.NoError(t, chunkurl.VerifyBundle(testSigningKey, "org", "proj", params, time.Now()))
}

func TestGetChunkUploadUrlsHandler_DirectStore(t *testing.T) {
	chunk, _ := newDirectChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	fresh := []byte("fresh chunk")
	freshHash := chunker.Sum(fresh)

	res, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context:       testChunkContext(),
		Chunks:        []*pb.ChunkRef{{Hash: freshHash.String(), SizeBytes: int64(len(fresh))}},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.NoError(t, err)
	require.Len(t, res.GetUrls(), 1)
	require.Equal(t, "https://s3.example.com/bucket/p/16/ab/cdef?X-Amz-Signature=x", res.GetUrls()[0].GetUrl())
	require.Equal(t, map[string]string{"If-None-Match": "*"}, res.GetUrls()[0].GetHeaders())
	require.Empty(t, res.GetBundleUrl(), "direct mode hands out per-chunk URLs only")
}

func TestGetChunkUploadUrlsHandler_NoPermission(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).
		Return(false)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context:       testChunkContext(),
		Chunks:        []*pb.ChunkRef{{Hash: chunker.Sum([]byte("x")).String(), SizeBytes: 1}},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestGetChunkUploadUrlsHandler_MissingContext(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Chunks:        []*pb.ChunkRef{{Hash: chunker.Sum([]byte("x")).String(), SizeBytes: 1}},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetChunkUploadUrlsHandler_InvalidRef(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).
		Return(true).
		Times(2)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context:       testChunkContext(),
		Chunks:        []*pb.ChunkRef{{Hash: "zz-not-hex", SizeBytes: 1}},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context:       testChunkContext(),
		Chunks:        []*pb.ChunkRef{{Hash: chunker.Sum([]byte("x")).String(), SizeBytes: 0}},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetChunkDownloadUrlsHandler(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	visible := []byte("public bytes")
	visibleHash := chunker.Sum(visible)
	hiddenHash := chunker.Sum([]byte("secret bytes"))

	srv := New(&mockUsecaseContainer{
		branch: newVisibleChunkBranch(t, visibleHash),
		common: newTestCommon(),
		chunk:  chunk,
	})
	res, err := srv.GetChunkDownloadUrls(context.Background(), &pb.GetChunkDownloadUrlsRequest{
		Context:       testChunkContext(),
		CommitIds:     []string{snow.ID(7).Base36()},
		Hashes:        []string{visibleHash.String(), hiddenHash.String()},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.NoError(t, err)
	require.Equal(t, chunkproof.Protocol, res.GetChunkProtocol())
	require.Len(t, res.GetUrls(), 1, "chunks outside the visible tree must not leak")
	require.Equal(t, visibleHash.String(), res.GetUrls()[0].GetHash())
	verifySignedURL(t, res.GetUrls()[0].GetUrl(), "org", "proj", chunkurl.OpDownload, 0)
}

// A caller who may read every path is checked against the project's chunk
// rows only: no tree is walked, and a hash without a row is left out.
func TestGetChunkDownloadUrlsHandler_UnrestrictedUsesChunkRows(t *testing.T) {
	chunk, store, rows := newChunkUsecase(t)
	stored := []byte("stored bytes")
	storedHash := chunker.Sum(stored)
	require.NoError(t, store.Put(context.Background(), testChunkProject, storedHash, stored))
	require.NoError(t, rows.InsertChunkRows(context.Background(), testChunkProject, []usecase.ChunkRow{{Hash: storedHash, SizeBytes: int64(len(stored))}}))
	unknownHash := chunker.Sum([]byte("never uploaded"))

	branch, perm, repo := newTestBranchUc(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(7)).Return(&domain.Commit{ID: 7, ProjectID: 42, TreeID: 5}, nil)
	srv := New(&mockUsecaseContainer{branch: branch.WithChunkRows(rows), common: newTestCommon(), chunk: chunk})

	res, err := srv.GetChunkDownloadUrls(context.Background(), &pb.GetChunkDownloadUrlsRequest{
		Context:       testChunkContext(),
		CommitIds:     []string{snow.ID(7).Base36()},
		Hashes:        []string{unknownHash.String(), storedHash.String()},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.NoError(t, err)
	require.Len(t, res.GetUrls(), 1)
	require.Equal(t, storedHash.String(), res.GetUrls()[0].GetHash())
	verifySignedURL(t, res.GetUrls()[0].GetUrl(), "org", "proj", chunkurl.OpDownload, 0)
}

func TestGetChunkDownloadUrlsHandler_InvalidHash(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})
	_, err := srv.GetChunkDownloadUrls(context.Background(), &pb.GetChunkDownloadUrlsRequest{
		Context:       testChunkContext(),
		CommitIds:     []string{snow.ID(7).Base36()},
		Hashes:        []string{"not-a-hash"},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// The caller's rules are compiled once per request; when they cannot be read
// no URL is issued at all.
func TestGetChunkDownloadUrlsHandler_ReadScopeError(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	hash := chunker.Sum([]byte("public bytes"))
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(nil, domain.NewErrorDatabase("rules unavailable"))
	repo := NewMockbranchRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)

	srv := New(&mockUsecaseContainer{branch: usecase.NewBranch(perm, repo, node), common: newTestCommon(), chunk: chunk})
	res, err := srv.GetChunkDownloadUrls(context.Background(), &pb.GetChunkDownloadUrlsRequest{
		Context:       testChunkContext(),
		CommitIds:     []string{snow.ID(7).Base36()},
		Hashes:        []string{hash.String()},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Error(t, err)
	require.Nil(t, res)
}

func TestGetChunkDownloadUrlsHandler_MissingContext(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkDownloadUrls(context.Background(), &pb.GetChunkDownloadUrlsRequest{
		Hashes:        []string{chunker.Sum([]byte("x")).String()},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestConfirmChunkUploadsHandler(t *testing.T) {
	chunk, store, repo := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	present := []byte("present")
	presentHash := chunker.Sum(present)
	require.NoError(t, store.Put(context.Background(), testChunkProject, presentHash, present))

	absentHash := chunker.Sum([]byte("absent"))
	res, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context:       testChunkContext(),
		Hashes:        []string{presentHash.String(), absentHash.String()},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.NoError(t, err)
	require.Equal(t, chunkproof.Protocol, res.GetChunkProtocol())
	require.Equal(t, []string{absentHash.String()}, res.GetMissingHashes())
	require.True(t, repo.has(testChunkProject, presentHash))
}

func TestConfirmChunkUploadsHandler_ReceiptSkipsTheReadBack(t *testing.T) {
	chunk, store, repo := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	data := []byte("proxied")
	hash := chunker.Sum(data)
	body, err := io.ReadAll(chunkbundle.NewEncoder([]chunkbundle.Entry{{Hash: hash, Data: data}}))
	require.NoError(t, err)
	receipts, err := chunk.StoreBundle(context.Background(), testChunkProject, bytes.NewReader(body), int64(len(body)), 1)
	require.NoError(t, err)
	receipt := receipts[0].Receipt
	// The bytes vanish after the PUT: only the receipt can confirm them.
	require.NoError(t, store.DeleteProject(context.Background(), testChunkProject))

	res, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context:       testChunkContext(),
		Hashes:        []string{hash.String()},
		Receipts:      []*pb.ChunkReceipt{{Hash: hash.String(), Receipt: receipt}},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.NoError(t, err)
	require.Empty(t, res.GetMissingHashes(), "a valid receipt is enough, no read-back")
	require.True(t, repo.has(testChunkProject, hash))
}

func TestConfirmChunkUploadsHandler_DirectStoreDeletesGarbageOnTheKey(t *testing.T) {
	repo := newStubChunkRepo()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	deleting := &deletingStore{directStubStore: directStubStore{ChunkStore: store}}
	chunk := usecase.NewChunk(repo, deleting, usecase.ChunkTransferConfig{SigningKey: testSigningKey, PresignTTL: time.Hour, MaxPageSize: 100})
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	hash := chunker.Sum([]byte("expected"))
	require.NoError(t, store.Put(context.Background(), testChunkProject, hash, []byte("garbage")))
	res, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context:       testChunkContext(),
		Hashes:        []string{hash.String()},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.NoError(t, err)
	require.Equal(t, []string{hash.String()}, res.GetMissingHashes(), "an object that does not match its hash is rejected")
	require.Equal(t, []domain.Hash{hash}, deleting.deleted)
	require.Equal(t, []snow.ID{testChunkProject}, deleting.projects)
	require.False(t, repo.has(testChunkProject, hash))
}

type deletingStore struct {
	directStubStore
	deleted  []domain.Hash
	projects []snow.ID
}

func (s *deletingStore) DeleteChunk(_ context.Context, projectID snow.ID, hash domain.Hash) error {
	s.deleted = append(s.deleted, hash)
	s.projects = append(s.projects, projectID)
	return nil
}

func TestConfirmChunkUploadsHandler_InvalidHash(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).
		Return(true).
		Times(2)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context:       testChunkContext(),
		Hashes:        []string{"zz-not-hex"},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context:       testChunkContext(),
		Receipts:      []*pb.ChunkReceipt{{Hash: "zz-not-hex"}},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestConfirmChunkUploadsHandler_NoPermission(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), testChunkProject, domain.PermissionWrite).
		Return(false)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context:       testChunkContext(),
		Hashes:        []string{chunker.Sum([]byte("x")).String()},
		ChunkProtocol: chunkproof.Protocol,
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestChunkRPCs_RefuseAnOlderProtocol checks that a client sending an older
// (or no) chunk_protocol is refused before anything else runs, with an
// upgrade hint.
func TestChunkRPCs_RefuseAnOlderProtocol(t *testing.T) {
	chunk, _, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})
	ctx := context.Background()

	for _, version := range []uint32{0, chunkproof.Protocol - 1} {
		_, err := srv.GetChunkUploadUrls(ctx, &pb.GetChunkUploadUrlsRequest{Context: testChunkContext(), ChunkProtocol: version})
		requireUpgradeHint(t, err)
		_, err = srv.GetChunkDownloadUrls(ctx, &pb.GetChunkDownloadUrlsRequest{Context: testChunkContext(), ChunkProtocol: version})
		requireUpgradeHint(t, err)
		_, err = srv.ConfirmChunkUploads(ctx, &pb.ConfirmChunkUploadsRequest{Context: testChunkContext(), ChunkProtocol: version})
		requireUpgradeHint(t, err)
	}
	require.NoError(t, checkChunkProtocol(chunkproof.Protocol+1), "a newer client is not refused here")
}

func requireUpgradeHint(t *testing.T, err error) {
	t.Helper()
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "upgrade nipa")
}

func TestToChunkReceipts(t *testing.T) {
	hash := chunker.Sum([]byte("payload"))
	receipts, err := toChunkReceipts([]*pb.ChunkReceipt{
		{Hash: hash.String(), Receipt: "r"},
		{Hash: hash.String()},
	})
	require.NoError(t, err)
	require.Equal(t, []usecase.ChunkReceipt{
		{Hash: hash, Receipt: "r"},
		{Hash: hash},
	}, receipts)
}
