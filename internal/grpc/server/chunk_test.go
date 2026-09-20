package server

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
)

func newChunkUsecase(t *testing.T) (*usecase.Chunk, *storage.LocalStore) {
	t.Helper()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	return usecase.NewChunk(stubChunkRepo{}, store), store
}

type stubChunkRepo struct{}

func (stubChunkRepo) InsertChunkIfNotExists(_ context.Context, _ domain.Hash, _ int64) error {
	return nil
}

type fakeServerStream struct {
	ctx context.Context
}

func (s *fakeServerStream) Context() context.Context     { return s.ctx }
func (s *fakeServerStream) SendMsg(interface{}) error    { return nil }
func (s *fakeServerStream) RecvMsg(interface{}) error    { return nil }
func (s *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeServerStream) SetTrailer(metadata.MD)       {}

type uploadChunksServer struct {
	fakeServerStream
	reqs []*pb.ChunkUploadRequest
	idx  int
	resp *pb.UploadChunksResponse
}

func (s *uploadChunksServer) Recv() (*pb.ChunkUploadRequest, error) {
	if s.idx >= len(s.reqs) {
		return nil, io.EOF
	}
	r := s.reqs[s.idx]
	s.idx++
	return r, nil
}

func (s *uploadChunksServer) SendAndClose(resp *pb.UploadChunksResponse) error {
	s.resp = resp
	return nil
}

type downloadChunksServer struct {
	fakeServerStream
	reqs  []*pb.DownloadChunksRequest
	idx   int
	resps []*pb.DownloadChunk
}

func (s *downloadChunksServer) Recv() (*pb.DownloadChunksRequest, error) {
	if s.idx >= len(s.reqs) {
		return nil, io.EOF
	}
	r := s.reqs[s.idx]
	s.idx++
	return r, nil
}

func (s *downloadChunksServer) Send(resp *pb.DownloadChunk) error {
	s.resps = append(s.resps, resp)
	return nil
}

func testChunkContext() *pb.ProjectContext {
	return &pb.ProjectContext{Org: "org", Project: "proj"}
}

func newUploadServer(reqs ...*pb.ChunkUploadRequest) *uploadChunksServer {
	return &uploadChunksServer{
		fakeServerStream: fakeServerStream{ctx: context.Background()},
		reqs:             reqs,
	}
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

func TestUploadChunksHandler(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	data := []byte("stream me up")
	stream := newUploadServer(&pb.ChunkUploadRequest{
		Hash:    chunker.Sum(data).String(),
		Data:    data,
		Context: testChunkContext(),
	})
	require.NoError(t, srv.UploadChunks(stream))
	require.Equal(t, int32(1), stream.resp.Uploaded)
	require.Equal(t, int32(0), stream.resp.Skipped)
}

func TestUploadChunksHandler_NoPermission(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(false)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	stream := newUploadServer(&pb.ChunkUploadRequest{
		Hash:    chunker.Sum([]byte("x")).String(),
		Data:    []byte("x"),
		Context: testChunkContext(),
	})
	err := srv.UploadChunks(stream)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestUploadChunksHandler_MissingContext(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	stream := newUploadServer(&pb.ChunkUploadRequest{
		Hash: chunker.Sum([]byte("x")).String(),
		Data: []byte("x"),
	})
	err := srv.UploadChunks(stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUploadChunksHandler_Dedupes(t *testing.T) {
	chunk, store := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	data := []byte("same chunk")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(context.Background(), hash, data))

	stream := newUploadServer(&pb.ChunkUploadRequest{
		Hash:    hash.String(),
		Data:    data,
		Context: testChunkContext(),
	})
	require.NoError(t, srv.UploadChunks(stream))
	require.Equal(t, int32(0), stream.resp.Uploaded)
	require.Equal(t, int32(1), stream.resp.Skipped)
}

func TestUploadChunksHandler_InvalidHash(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, perm, _ := newTestBranchUc(t)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).
		Return(true)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	stream := newUploadServer(&pb.ChunkUploadRequest{
		Hash:    "zz-not-hex",
		Data:    []byte("x"),
		Context: testChunkContext(),
	})
	err := srv.UploadChunks(stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func newDownloadServer(reqs ...*pb.DownloadChunksRequest) *downloadChunksServer {
	return &downloadChunksServer{
		fakeServerStream: fakeServerStream{ctx: context.Background()},
		reqs:             reqs,
	}
}

func TestDownloadChunksHandler(t *testing.T) {
	chunk, store := newChunkUsecase(t)
	data := []byte("stream me down")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(context.Background(), hash, data))

	srv := New(&mockUsecaseContainer{
		branch: newVisibleChunkBranch(t, hash),
		common: newTestCommon(),
		chunk:  chunk,
	})
	stream := newDownloadServer(&pb.DownloadChunksRequest{
		Hash:      hash.String(),
		Context:   testChunkContext(),
		CommitIds: []string{snow.ID(7).Base36()},
	})
	require.NoError(t, srv.DownloadChunks(stream))
	require.Len(t, stream.resps, 1)
	require.Equal(t, hash.String(), stream.resps[0].Hash)
	require.Equal(t, data, stream.resps[0].Data)
}

func TestDownloadChunksHandler_HiddenChunk(t *testing.T) {
	chunk, store := newChunkUsecase(t)

	hidden := []byte("secret bytes")
	hiddenHash := chunker.Sum(hidden)
	require.NoError(t, store.Put(context.Background(), hiddenHash, hidden))

	visible := []byte("public bytes")
	visibleHash := chunker.Sum(visible)
	require.NoError(t, store.Put(context.Background(), visibleHash, visible))

	srv := New(&mockUsecaseContainer{
		branch: newVisibleChunkBranch(t, visibleHash),
		common: newTestCommon(),
		chunk:  chunk,
	})
	stream := newDownloadServer(&pb.DownloadChunksRequest{
		Hash:      hiddenHash.String(),
		Context:   testChunkContext(),
		CommitIds: []string{snow.ID(7).Base36()},
	})
	err := srv.DownloadChunks(stream)
	require.Equal(t, codes.NotFound, status.Code(err), "chunks outside the visible tree must not leak")
}

func TestDownloadChunksHandler_MissingContext(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	branch, _, _ := newTestBranchUc(t)
	srv := New(&mockUsecaseContainer{branch: branch, common: newTestCommon(), chunk: chunk})

	stream := newDownloadServer(&pb.DownloadChunksRequest{
		Hash: chunker.Sum([]byte("absent")).String(),
	})
	err := srv.DownloadChunks(stream)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestDownloadChunksHandler_Missing(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	hash := chunker.Sum([]byte("absent"))

	srv := New(&mockUsecaseContainer{
		branch: newVisibleChunkBranch(t, hash),
		common: newTestCommon(),
		chunk:  chunk,
	})
	stream := newDownloadServer(&pb.DownloadChunksRequest{
		Hash:      hash.String(),
		Context:   testChunkContext(),
		CommitIds: []string{snow.ID(7).Base36()},
	})
	err := srv.DownloadChunks(stream)
	require.Equal(t, codes.NotFound, status.Code(err))
}
