package server

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
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

func TestUploadChunksHandler(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	srv := New(&mockUsecaseContainer{chunk: chunk})

	data := []byte("stream me up")
	stream := &uploadChunksServer{
		fakeServerStream: fakeServerStream{ctx: context.Background()},
		reqs: []*pb.ChunkUploadRequest{
			{Hash: chunker.Sum(data).String(), Data: data},
		},
	}
	err := srv.UploadChunks(stream)
	require.NoError(t, err)
	require.Equal(t, int32(1), stream.resp.Uploaded)
	require.Equal(t, int32(0), stream.resp.Skipped)
}

func TestUploadChunksHandler_Dedupes(t *testing.T) {
	chunk, store := newChunkUsecase(t)
	srv := New(&mockUsecaseContainer{chunk: chunk})

	data := []byte("same chunk")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(context.Background(), hash, data))

	stream := &uploadChunksServer{
		fakeServerStream: fakeServerStream{ctx: context.Background()},
		reqs: []*pb.ChunkUploadRequest{
			{Hash: hash.String(), Data: data},
		},
	}
	err := srv.UploadChunks(stream)
	require.NoError(t, err)
	require.Equal(t, int32(0), stream.resp.Uploaded)
	require.Equal(t, int32(1), stream.resp.Skipped)
}

func TestUploadChunksHandler_InvalidHash(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	srv := New(&mockUsecaseContainer{chunk: chunk})

	stream := &uploadChunksServer{
		fakeServerStream: fakeServerStream{ctx: context.Background()},
		reqs:             []*pb.ChunkUploadRequest{{Hash: "zz-not-hex", Data: []byte("x")}},
	}
	err := srv.UploadChunks(stream)
	require.Error(t, err)
}

func TestDownloadChunksHandler(t *testing.T) {
	chunk, store := newChunkUsecase(t)
	srv := New(&mockUsecaseContainer{chunk: chunk})

	data := []byte("stream me down")
	hash := chunker.Sum(data)
	require.NoError(t, store.Put(context.Background(), hash, data))

	stream := &downloadChunksServer{
		fakeServerStream: fakeServerStream{ctx: context.Background()},
		reqs: []*pb.DownloadChunksRequest{
			{Hash: hash.String()},
		},
	}
	err := srv.DownloadChunks(stream)
	require.NoError(t, err)
	require.Len(t, stream.resps, 1)
	require.Equal(t, hash.String(), stream.resps[0].Hash)
	require.Equal(t, data, stream.resps[0].Data)
}

func TestDownloadChunksHandler_Missing(t *testing.T) {
	chunk, _ := newChunkUsecase(t)
	srv := New(&mockUsecaseContainer{chunk: chunk})

	stream := &downloadChunksServer{
		fakeServerStream: fakeServerStream{ctx: context.Background()},
		reqs: []*pb.DownloadChunksRequest{
			{Hash: chunker.Sum([]byte("absent")).String()},
		},
	}
	err := srv.DownloadChunks(stream)
	require.Error(t, err)
}
