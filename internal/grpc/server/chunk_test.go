package server

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
)

const testSigningKey = "test-signing-key"

func newChunkUsecase(t *testing.T) (*usecase.Chunk, *storage.LocalStore) {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	uc := usecase.NewChunk(stubChunkRepo{}, store, usecase.ChunkTransferConfig{
		SigningKey:  testSigningKey,
		PresignTTL:  time.Hour,
		MaxPageSize: 100,
	})
	return uc, store
}

type stubChunkRepo struct{}

func (stubChunkRepo) InsertChunkIfNotExists(_ context.Context, _ domain.Hash, _ int64) error {
	return nil
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
		ListFilesByTree(gomock.Any(), int64(5)).
		Return([]*domain.File{{
			ID:     1,
			Name:   "a.txt",
			TreeID: 5,
			Chunks: []domain.Chunk{{ID: 1, Hash: visible}},
		}}, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), int64(5)).
		Return(nil, nil)
	return branch
}

func TestGetChunkUploadUrlsHandler(t *testing.T) {
	chunk, store := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	stored := []byte("stored chunk")
	storedHash := chunker.Sum(stored)
	require.NoError(t, store.Put(context.Background(), storedHash, stored))

	fresh := []byte("fresh chunk")
	freshHash := chunker.Sum(fresh)

	res, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context: testChunkContext(),
		Chunks: []*pb.ChunkRef{
			{Hash: storedHash.String(), SizeBytes: int64(len(stored))},
			{Hash: freshHash.String(), SizeBytes: int64(len(fresh))},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.GetUrls(), 2)

	require.True(t, res.GetUrls()[0].GetAlreadyStored())
	require.Empty(t, res.GetUrls()[0].GetUrl())

	require.False(t, res.GetUrls()[1].GetAlreadyStored())
	verifySignedURL(t, res.GetUrls()[1].GetUrl(), "org", "proj", chunkurl.OpUpload, int64(len(fresh)))
}

func TestGetChunkUploadUrlsHandler_NoPermission(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(false)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context: testChunkContext(),
		Chunks:  []*pb.ChunkRef{{Hash: chunker.Sum([]byte("x")).String(), SizeBytes: 1}},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestGetChunkUploadUrlsHandler_MissingContext(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Chunks: []*pb.ChunkRef{{Hash: chunker.Sum([]byte("x")).String(), SizeBytes: 1}},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetChunkUploadUrlsHandler_InvalidRef(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(true).
		Times(2)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context: testChunkContext(),
		Chunks:  []*pb.ChunkRef{{Hash: "zz-not-hex", SizeBytes: 1}},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = srv.GetChunkUploadUrls(context.Background(), &pb.GetChunkUploadUrlsRequest{
		Context: testChunkContext(),
		Chunks:  []*pb.ChunkRef{{Hash: chunker.Sum([]byte("x")).String(), SizeBytes: 0}},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetChunkDownloadUrlsHandler(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	visible := []byte("public bytes")
	visibleHash := chunker.Sum(visible)
	hiddenHash := chunker.Sum([]byte("secret bytes"))

	srv := New(&mockUsecaseContainer{
		branch: newVisibleChunkBranch(t, visibleHash),
		common: newTestCommon(),
		chunk:  chunk,
	})
	res, err := srv.GetChunkDownloadUrls(context.Background(), &pb.GetChunkDownloadUrlsRequest{
		Context:   testChunkContext(),
		CommitIds: []string{snow.ID(7).Base36()},
		Hashes:    []string{visibleHash.String(), hiddenHash.String()},
	})
	require.NoError(t, err)
	require.Len(t, res.GetUrls(), 1, "chunks outside the visible tree must not leak")
	require.Equal(t, visibleHash.String(), res.GetUrls()[0].GetHash())
	verifySignedURL(t, res.GetUrls()[0].GetUrl(), "org", "proj", chunkurl.OpDownload, 0)
}

func TestGetChunkDownloadUrlsHandler_MissingContext(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.GetChunkDownloadUrls(context.Background(), &pb.GetChunkDownloadUrlsRequest{
		Hashes: []string{chunker.Sum([]byte("x")).String()},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestConfirmChunkUploadsHandler(t *testing.T) {
	chunk, store := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	present := []byte("present")
	presentHash := chunker.Sum(present)
	require.NoError(t, store.Put(context.Background(), presentHash, present))

	absentHash := chunker.Sum([]byte("absent"))
	res, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context: testChunkContext(),
		Hashes:  []string{presentHash.String(), absentHash.String()},
	})
	require.NoError(t, err)
	require.Equal(t, []string{absentHash.String()}, res.GetMissingHashes())
}

func TestConfirmChunkUploadsHandler_InvalidHash(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context: testChunkContext(),
		Hashes:  []string{"zz-not-hex"},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestConfirmChunkUploadsHandler_NoPermission(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(false)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	_, err := srv.ConfirmChunkUploads(context.Background(), &pb.ConfirmChunkUploadsRequest{
		Context: testChunkContext(),
		Hashes:  []string{chunker.Sum([]byte("x")).String()},
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
