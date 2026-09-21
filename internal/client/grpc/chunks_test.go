package grpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
)

type chunkTestServer struct {
	pb.UnimplementedNipaServiceServer

	mu             sync.Mutex
	stored         map[string][]byte
	uploadErr      error
	downloadErr    error
	confirmMissing []string
	uploadAuth     string
	downloadAuth   string
	lastUploadRefs []*pb.ChunkRef
	lastDownload   *pb.GetChunkDownloadUrlsRequest
}

func newChunkTestServer() *chunkTestServer {
	return &chunkTestServer{stored: map[string][]byte{}}
}

func (s *chunkTestServer) put(hash string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stored[hash] = data
}

func (s *chunkTestServer) storedChunk(hash string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.stored[hash]
	return data, ok
}

func (s *chunkTestServer) GetChunkUploadUrls(ctx context.Context, req *pb.GetChunkUploadUrlsRequest) (*pb.GetChunkUploadUrlsResponse, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if auth := md.Get("authorization"); len(auth) > 0 {
			s.uploadAuth = auth[0]
		}
	}
	s.lastUploadRefs = req.GetChunks()
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	resp := &pb.GetChunkUploadUrlsResponse{}
	for _, ref := range req.GetChunks() {
		_, exists := s.storedChunk(ref.GetHash())
		url := ""
		if !exists {
			url = fmt.Sprintf("/api/chunks/%s/%s/%s?op=put&size=%d&exp=9999999999&sig=test",
				req.GetContext().GetOrg(), req.GetContext().GetProject(), ref.GetHash(), ref.GetSizeBytes())
		}
		resp.Urls = append(resp.Urls, &pb.PresignedChunkUrl{
			Hash:          ref.GetHash(),
			Url:           url,
			AlreadyStored: exists,
		})
	}
	return resp, nil
}

func (s *chunkTestServer) GetChunkDownloadUrls(ctx context.Context, req *pb.GetChunkDownloadUrlsRequest) (*pb.GetChunkDownloadUrlsResponse, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if auth := md.Get("authorization"); len(auth) > 0 {
			s.downloadAuth = auth[0]
		}
	}
	s.lastDownload = req
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	resp := &pb.GetChunkDownloadUrlsResponse{}
	for _, hash := range req.GetHashes() {
		if _, exists := s.storedChunk(hash); !exists {
			continue
		}
		resp.Urls = append(resp.Urls, &pb.PresignedChunkUrl{
			Hash: hash,
			Url: fmt.Sprintf("/api/chunks/%s/%s/%s?op=get&exp=9999999999&sig=test",
				req.GetContext().GetOrg(), req.GetContext().GetProject(), hash),
		})
	}
	return resp, nil
}

func (s *chunkTestServer) ConfirmChunkUploads(_ context.Context, _ *pb.ConfirmChunkUploadsRequest) (*pb.ConfirmChunkUploadsResponse, error) {
	return &pb.ConfirmChunkUploadsResponse{MissingHashes: s.confirmMissing}, nil
}

func (s *chunkTestServer) serveChunkHTTP(w http.ResponseWriter, r *http.Request) {
	hash := path.Base(r.URL.Path)
	switch r.URL.Query().Get("op") {
	case "put":
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unable to read body", http.StatusBadRequest)
			return
		}
		s.put(hash, data)
		w.WriteHeader(http.StatusNoContent)
	case "get":
		data, ok := s.storedChunk(hash)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
	default:
		http.Error(w, "invalid op", http.StatusBadRequest)
	}
}

// startChunkTestServer serves gRPC (for presign RPCs) and the signed HTTP
// chunk endpoints on one listener, mirroring how nipad exposes both.
func startChunkTestServer(t *testing.T, srv *chunkTestServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	gs := grpc.NewServer()
	pb.RegisterNipaServiceServer(gs, srv)

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	httpSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
				gs.ServeHTTP(w, r)
				return
			}
			srv.serveChunkHTTP(w, r)
		}),
		Protocols: protocols,
	}
	go func() { _ = httpSrv.Serve(lis) }()
	t.Cleanup(func() {
		_ = httpSrv.Close()
		gs.Stop()
	})
	return lis.Addr().String()
}

func testChunkHash(seed byte) serverDomain.Hash {
	var h serverDomain.Hash
	h[0] = seed
	return h
}

func TestClient_UploadChunks_Success(t *testing.T) {
	h1 := testChunkHash(0x01)
	h2 := testChunkHash(0x02)

	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	uploaded, skipped, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h1, Data: []byte("aaaa")},
		{Hash: h2, Data: []byte("bbbb")},
	})
	require.NoError(t, err)
	require.Equal(t, 2, uploaded)
	require.Equal(t, 0, skipped)
	require.Equal(t, "Bearer tok", srv.uploadAuth, "presign RPCs must carry the access token")
	require.Len(t, srv.lastUploadRefs, 2)
	require.Equal(t, int64(4), srv.lastUploadRefs[0].GetSizeBytes())

	got1, ok := srv.storedChunk(h1.String())
	require.True(t, ok)
	require.Equal(t, []byte("aaaa"), got1)
	got2, ok := srv.storedChunk(h2.String())
	require.True(t, ok)
	require.Equal(t, []byte("bbbb"), got2)
}

func TestClient_UploadChunks_SkipsStored(t *testing.T) {
	h1 := testChunkHash(0x01)
	h2 := testChunkHash(0x02)

	srv := newChunkTestServer()
	srv.put(h1.String(), []byte("aaaa"))
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	uploaded, skipped, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h1, Data: []byte("aaaa")},
		{Hash: h2, Data: []byte("bbbb")},
	})
	require.NoError(t, err)
	require.Equal(t, 1, uploaded)
	require.Equal(t, 1, skipped)
}

func TestClient_UploadChunks_ReportsEachChunk(t *testing.T) {
	h1 := testChunkHash(0x01)
	h2 := testChunkHash(0x02)

	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	var gotObjects int
	var gotBytes int64
	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h1, Data: []byte("aaaa")},
		{Hash: h2, Data: []byte("bbbb")},
	}, func(_ *serverDomain.ChunkData) {
		gotObjects++
		gotBytes += 4
	})
	require.NoError(t, err)
	require.Equal(t, 2, gotObjects, "the per-chunk callback must fire for every uploaded chunk")
	require.Equal(t, int64(8), gotBytes)
}

func TestClient_UploadChunks_NoChunks(t *testing.T) {
	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	uploaded, skipped, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, nil)
	require.NoError(t, err)
	require.Equal(t, 0, uploaded)
	require.Equal(t, 0, skipped)
	require.Empty(t, srv.stored)
}

func TestClient_UploadChunks_ServerError(t *testing.T) {
	srv := newChunkTestServer()
	srv.uploadErr = status.Error(codes.InvalidArgument, "bad chunk")
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("x")},
	})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_UploadChunks_MissingAfterConfirm(t *testing.T) {
	h1 := testChunkHash(0x01)

	srv := newChunkTestServer()
	srv.confirmMissing = []string{h1.String()}
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: h1, Data: []byte("aaaa")},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "upload incomplete")
}

func TestClient_UploadChunks_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("x")},
	})
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestClient_DownloadChunks_Success(t *testing.T) {
	h1 := testChunkHash(0x01)
	h2 := testChunkHash(0x02)

	srv := newChunkTestServer()
	srv.put(h1.String(), []byte("aaaa"))
	srv.put(h2.String(), []byte("bbbb"))
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got := make(map[serverDomain.Hash][]byte)
	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}, Paths: []string{"assets/"}}, []serverDomain.Hash{h1, h2}, func(h serverDomain.Hash, data []byte) error {
		got[h] = data
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, []byte("aaaa"), got[h1])
	require.Equal(t, []byte("bbbb"), got[h2])
	require.Equal(t, "Bearer tok", srv.downloadAuth, "presign RPCs must carry the access token")
	require.Equal(t, []string{"1"}, srv.lastDownload.GetCommitIds())
	require.Equal(t, []string{"assets/"}, srv.lastDownload.GetPaths())
}

func TestClient_DownloadChunks_ManyChunks(t *testing.T) {
	const n = 3000
	srv := newChunkTestServer()
	hashes := make([]serverDomain.Hash, 0, n)
	for i := 0; i < n; i++ {
		var h serverDomain.Hash
		binary.BigEndian.PutUint32(h[:4], uint32(i))
		hashes = append(hashes, h)
		srv.put(h.String(), bytes.Repeat([]byte{byte(i)}, 1024))
	}
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	var gotObjects int
	var gotBytes int64
	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, hashes, func(_ serverDomain.Hash, data []byte) error {
		gotObjects++
		gotBytes += int64(len(data))
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, n, gotObjects)
	require.Equal(t, int64(n*1024), gotBytes)
}

func TestClient_DownloadChunks_SinkErrorStops(t *testing.T) {
	h := testChunkHash(0x01)

	srv := newChunkTestServer()
	srv.put(h.String(), []byte("aaaa"))
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	unexpected := fmt.Errorf("sink failed")
	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{h}, func(serverDomain.Hash, []byte) error {
		return unexpected
	})
	require.ErrorIs(t, err, unexpected)
}

func TestClient_DownloadChunks_Unavailable(t *testing.T) {
	h := testChunkHash(0x01)

	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{h}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestClient_DownloadChunks_ServerError(t *testing.T) {
	srv := newChunkTestServer()
	srv.downloadErr = status.Error(codes.NotFound, "chunk missing")
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{testChunkHash(0x01)}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_DownloadChunks_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{testChunkHash(0x01)}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}
