package grpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"sync/atomic"
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

	mu              sync.Mutex
	stored          map[string][]byte
	uploadErr       error
	downloadErr     error
	confirmErr      error
	confirmMissing  []string
	uploadAuth      string
	downloadAuth    string
	lastUploadRefs  []*pb.ChunkRef
	lastDownload    *pb.GetChunkDownloadUrlsRequest
	uploadBadHash   bool
	downloadBadHash bool
	unknownUpload   bool
	putStatus       int
	getStatus       int
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
	if s.uploadBadHash {
		resp.Urls = append(resp.Urls, &pb.PresignedChunkUrl{Hash: "zz-not-hex", Url: "/api/chunks/org/proj/zz?op=put"})
	}
	if s.unknownUpload {
		resp.Urls = append(resp.Urls, &pb.PresignedChunkUrl{Hash: testChunkHash(0xee).String(), Url: "/api/chunks/org/proj/ee?op=put"})
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
	if s.downloadBadHash {
		resp.Urls = append(resp.Urls, &pb.PresignedChunkUrl{Hash: "zz-not-hex", Url: "/api/chunks/org/proj/zz?op=get"})
	}
	return resp, nil
}

func (s *chunkTestServer) ConfirmChunkUploads(_ context.Context, _ *pb.ConfirmChunkUploadsRequest) (*pb.ConfirmChunkUploadsResponse, error) {
	if s.confirmErr != nil {
		return nil, s.confirmErr
	}
	return &pb.ConfirmChunkUploadsResponse{MissingHashes: s.confirmMissing}, nil
}

func (s *chunkTestServer) serveChunkHTTP(w http.ResponseWriter, r *http.Request) {
	hash := path.Base(r.URL.Path)
	switch r.URL.Query().Get("op") {
	case "put":
		if s.putStatus != 0 {
			http.Error(w, "put failed", s.putStatus)
			return
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unable to read body", http.StatusBadRequest)
			return
		}
		s.put(hash, data)
		w.WriteHeader(http.StatusNoContent)
	case "get":
		if s.getStatus != 0 {
			http.Error(w, "get failed", s.getStatus)
			return
		}
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

func TestClient_UploadChunks_HTTPError(t *testing.T) {
	srv := newChunkTestServer()
	srv.putStatus = http.StatusInternalServerError
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("aaaa")},
	})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_UploadChunks_ConfirmError(t *testing.T) {
	srv := newChunkTestServer()
	srv.confirmErr = status.Error(codes.Internal, "confirm failed")
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("aaaa")},
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestClient_UploadChunks_InvalidHashFromServer(t *testing.T) {
	srv := newChunkTestServer()
	srv.uploadBadHash = true
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("aaaa")},
	})
	require.Error(t, err)
}

func TestClient_UploadChunks_UnknownChunkFromServer(t *testing.T) {
	srv := newChunkTestServer()
	srv.unknownUpload = true
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("aaaa")},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown chunk")
}

func TestClient_DownloadChunks_HTTPError(t *testing.T) {
	h := testChunkHash(0x01)

	srv := newChunkTestServer()
	srv.put(h.String(), []byte("aaaa"))
	srv.getStatus = http.StatusInternalServerError
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{h}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_DownloadChunks_NotFoundStatus(t *testing.T) {
	h := testChunkHash(0x01)

	srv := newChunkTestServer()
	srv.put(h.String(), []byte("aaaa"))
	srv.getStatus = http.StatusNotFound
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{h}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_DownloadChunks_InvalidHashFromServer(t *testing.T) {
	h := testChunkHash(0x01)

	srv := newChunkTestServer()
	srv.put(h.String(), []byte("aaaa"))
	srv.downloadBadHash = true
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{h}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)
}

func TestClient_Chunks_NoToken(t *testing.T) {
	srv := newChunkTestServer()
	addr := startChunkTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{getTokenErr: errors.New("no token")})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, []*serverDomain.ChunkData{
		{Hash: testChunkHash(0x01), Data: []byte("aaaa")},
	})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	err = c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj", CommitIDs: []string{"1"}}, []serverDomain.Hash{testChunkHash(0x01)}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestClient_ChunkHTTPError(t *testing.T) {
	tests := []struct {
		status int
		code   int
	}{
		{status: http.StatusNotFound, code: 404},
		{status: http.StatusForbidden, code: 400},
		{status: http.StatusUnauthorized, code: 400},
		{status: http.StatusInternalServerError, code: 400},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			res := &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(""))}
			var domErr *domain.Error
			require.ErrorAs(t, chunkHTTPError(res), &domErr)
			require.Equal(t, tt.code, domErr.Code)
		})
	}
}

func TestClient_HTTPURL(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})

	c.transport.url = ""
	require.Equal(t, "/api/chunks/x", c.httpURL("/api/chunks/x"))

	c.transport.url = "127.0.0.1:6745"
	require.Equal(t, "http://127.0.0.1:6745/api/chunks/x", c.httpURL("/api/chunks/x"))

	c.transport.url = "http://example.com:1234/"
	require.Equal(t, "http://example.com:1234/api/chunks/x", c.httpURL("/api/chunks/x"))

	require.Equal(t, "https://s3.example.com/bucket/key", c.httpURL("https://s3.example.com/bucket/key"))
}

func TestClient_ForEachChunkEmpty(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})

	require.NoError(t, c.putChunks(context.Background(), nil))
	require.NoError(t, c.getChunks(context.Background(), nil, func(serverDomain.Hash, []byte) error {
		return nil
	}))
}

func TestClient_PutChunksInvalidURL(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})

	err := c.putChunks(context.Background(), []chunkTransfer{{hash: testChunkHash(0x01), url: "\x7f", data: []byte("x")}})
	require.Error(t, err)
}

func TestClient_GetChunksInvalidURL(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})

	err := c.getChunks(context.Background(), []chunkTransfer{{hash: testChunkHash(0x01), url: "\x7f"}}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)
}

func TestClient_PutChunksDoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	c := NewClient(NewTransport(), &stubSession{})
	err := c.putChunks(context.Background(), []chunkTransfer{{hash: testChunkHash(0x01), url: srv.URL, data: []byte("x")}})
	require.Error(t, err)
}

func TestClient_GetChunksDoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	c := NewClient(NewTransport(), &stubSession{})
	err := c.getChunks(context.Background(), []chunkTransfer{{hash: testChunkHash(0x01), url: srv.URL}}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)
}

func TestClient_GetChunksReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()

	c := NewClient(NewTransport(), &stubSession{})
	err := c.getChunks(context.Background(), []chunkTransfer{{hash: testChunkHash(0x01), url: srv.URL}}, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.Error(t, err)
}

func TestClient_DownloadChunks_NoHashes(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	err := c.DownloadChunks(context.Background(), domain.ChunkScope{Org: "org", Project: "proj"}, nil, func(serverDomain.Hash, []byte) error {
		return nil
	})
	require.NoError(t, err)
}

func TestClient_GetChunksStopsDeliveryAfterSinkError(t *testing.T) {
	var served int32
	bothServed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data"))
		if atomic.AddInt32(&served, 1) == 2 {
			close(bothServed)
		}
	}))
	defer srv.Close()

	c := NewClient(NewTransport(), &stubSession{})
	sinkErr := errors.New("sink failed")
	var calls int32
	err := c.getChunks(context.Background(), []chunkTransfer{
		{hash: testChunkHash(0x01), url: srv.URL},
		{hash: testChunkHash(0x02), url: srv.URL},
	}, func(serverDomain.Hash, []byte) error {
		<-bothServed
		atomic.AddInt32(&calls, 1)
		return sinkErr
	})
	require.ErrorIs(t, err, sinkErr)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "delivery must stop after the sink fails")
}
