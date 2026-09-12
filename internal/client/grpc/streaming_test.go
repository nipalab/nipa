package grpc

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func (f *fakeServer) Push(_ context.Context, req *pb.PushRequest) (*pb.PushResponse, error) {
	f.lastPushReq = req
	if f.pushErr != nil {
		return nil, f.pushErr
	}
	return f.pushResp, nil
}

func (f *fakeServer) UploadChunks(stream pb.NipaService_UploadChunksServer) error {
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		if auth := md.Get("authorization"); len(auth) > 0 {
			f.uploadStreamAuth = auth[0]
		}
	}
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if f.uploadErr != nil {
			return f.uploadErr
		}
		f.uploadedChunks = append(f.uploadedChunks, req)
	}
	return stream.SendAndClose(f.uploadResp)
}

func (f *fakeServer) DownloadChunks(stream pb.NipaService_DownloadChunksServer) error {
	if md, ok := metadata.FromIncomingContext(stream.Context()); ok {
		if auth := md.Get("authorization"); len(auth) > 0 {
			f.downloadStreamAuth = auth[0]
		}
	}
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		f.downloadRequests = append(f.downloadRequests, req.GetHash())
		if f.downloadErr != nil {
			return f.downloadErr
		}
		if data, ok := f.downloadData[req.GetHash()]; ok {
			if err := stream.Send(&pb.DownloadChunk{Hash: req.GetHash(), Data: data}); err != nil {
				return err
			}
		}
	}
}

func TestClient_Push_Success(t *testing.T) {
	var chunkHash serverDomain.Hash
	chunkHash[0] = 0xab
	var fileHash serverDomain.Hash
	fileHash[31] = 0xcd

	fs := &fakeServer{pushResp: &pb.PushResponse{
		CommitId:   snow.ID(7).Base36(),
		CommitHash: fileHash.String(),
		TreeHash:   chunkHash.String(),
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.Push(context.Background(), "org", "project", "main", "base", "message",
		[]*serverDomain.PushFile{{
			Path:        "a.txt",
			Mode:        0o644,
			SizeBytes:   5,
			IsBinary:    false,
			FileHash:    fileHash,
			ChunkHashes: []serverDomain.Hash{chunkHash},
		}},
		[]string{"old.txt"},
	)
	require.NoError(t, err)
	require.Equal(t, snow.ID(7), got.CommitID)
	require.Equal(t, fileHash, got.CommitHash)
	require.Equal(t, chunkHash, got.TreeHash)

	req := fs.lastPushReq
	require.Equal(t, "org", req.GetContext().GetOrg())
	require.Equal(t, "project", req.GetContext().GetProject())
	require.Equal(t, "main", req.GetBranch())
	require.Equal(t, "base", req.GetBaseTreeHash())
	require.Equal(t, "message", req.GetMessage())
	require.Equal(t, []string{"old.txt"}, req.GetRemovedFiles())

	require.Len(t, req.GetFiles(), 1)
	gotFile := req.GetFiles()[0]
	require.Equal(t, "a.txt", gotFile.GetPath())
	require.Equal(t, pb.FileMode_FILE_MODE_READ_WRITE, gotFile.GetMode())
	require.Equal(t, int64(5), gotFile.GetSizeBytes())
	require.Equal(t, fileHash.String(), gotFile.GetFileHash())
	require.Equal(t, []string{chunkHash.String()}, gotFile.GetChunkHashes())
}

func TestClient_Push_ExecutableMode(t *testing.T) {
	fs := &fakeServer{pushResp: &pb.PushResponse{
		CommitId:   snow.ID(1).Base36(),
		CommitHash: serverDomain.Hash{}.String(),
		TreeHash:   serverDomain.Hash{}.String(),
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m",
		[]*serverDomain.PushFile{{Path: "run.sh", Mode: 0o755, FileHash: serverDomain.Hash{}, ChunkHashes: []serverDomain.Hash{{0x01}}}}, nil)
	require.NoError(t, err)
	require.Equal(t, pb.FileMode_FILE_MODE_EXECUTABLE, fs.lastPushReq.GetFiles()[0].GetMode())
}

func TestClient_Push_ServerError(t *testing.T) {
	addr := startTestServer(t, &fakeServer{pushErr: status.Error(codes.FailedPrecondition, "branch moved")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.Push(context.Background(), "org", "project", "main", "stale", "m", nil, nil)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "branch moved", domErr.Message)
}

func TestClient_Push_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil)
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestClient_UploadChunks_Success(t *testing.T) {
	var h1, h2 serverDomain.Hash
	h1[0] = 0x01
	h2[0] = 0x02

	fs := &fakeServer{
		uploadResp: &pb.UploadChunksResponse{Uploaded: 2, Skipped: 0},
	}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	uploaded, skipped, err := c.UploadChunks(context.Background(), []*serverDomain.ChunkData{
		{Hash: h1, Data: []byte("aaaa")},
		{Hash: h2, Data: []byte("bbbb")},
	})
	require.NoError(t, err)
	require.Equal(t, 2, uploaded)
	require.Equal(t, 0, skipped)
	require.Equal(t, "Bearer tok", fs.uploadStreamAuth, "streaming RPCs must carry the access token")

	require.Len(t, fs.uploadedChunks, 2)
	require.Equal(t, h1.String(), fs.uploadedChunks[0].GetHash())
	require.Equal(t, []byte("aaaa"), fs.uploadedChunks[0].GetData())
	require.Equal(t, h2.String(), fs.uploadedChunks[1].GetHash())
	require.Equal(t, []byte("bbbb"), fs.uploadedChunks[1].GetData())
}

func TestClient_UploadChunks_NoChunks(t *testing.T) {
	fs := &fakeServer{uploadResp: &pb.UploadChunksResponse{}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	uploaded, skipped, err := c.UploadChunks(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 0, uploaded)
	require.Equal(t, 0, skipped)
	require.Empty(t, fs.uploadedChunks)
}

func TestClient_UploadChunks_ServerError(t *testing.T) {
	fs := &fakeServer{
		uploadResp: &pb.UploadChunksResponse{},
		uploadErr:  status.Error(codes.InvalidArgument, "bad chunk"),
	}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, _, err := c.UploadChunks(context.Background(), []*serverDomain.ChunkData{
		{Hash: serverDomain.Hash{0x01}, Data: []byte("x")},
	})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestClient_UploadChunks_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, _, err := c.UploadChunks(context.Background(), nil)
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestClient_DownloadChunks_Success(t *testing.T) {
	var h1, h2 serverDomain.Hash
	h1[0] = 0x01
	h2[0] = 0x02

	fs := &fakeServer{downloadData: map[string][]byte{
		h1.String(): []byte("aaaa"),
		h2.String(): []byte("bbbb"),
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.DownloadChunks(context.Background(), []serverDomain.Hash{h1, h2})
	require.NoError(t, err)
	require.Equal(t, []byte("aaaa"), got[h1])
	require.Equal(t, []byte("bbbb"), got[h2])
	require.Equal(t, "Bearer tok", fs.downloadStreamAuth, "streaming RPCs must carry the access token")
	require.Equal(t, []string{h1.String(), h2.String()}, fs.downloadRequests)
}

func TestClient_DownloadChunks_ReportsEachChunk(t *testing.T) {
	var h1, h2 serverDomain.Hash
	h1[0] = 0x01
	h2[0] = 0x02

	fs := &fakeServer{downloadData: map[string][]byte{
		h1.String(): []byte("aaaa"),
		h2.String(): []byte("bbbb"),
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	var gotObject int
	var gotBytes int64
	_, err := c.DownloadChunks(context.Background(), []serverDomain.Hash{h1, h2}, func(_ serverDomain.Hash, data []byte) {
		gotObject++
		gotBytes += int64(len(data))
	})
	require.NoError(t, err)
	require.Equal(t, 2, gotObject, "the per-chunk callback must fire for every received chunk")
	require.Equal(t, int64(8), gotBytes)
}

func TestClient_DownloadChunks_ServerError(t *testing.T) {
	fs := &fakeServer{downloadErr: status.Error(codes.NotFound, "chunk missing")}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.DownloadChunks(context.Background(), []serverDomain.Hash{{0x01}})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_DownloadChunks_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.DownloadChunks(context.Background(), []serverDomain.Hash{{0x01}})
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestAuthedContext_NoToken(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{getTokenErr: status.Error(codes.NotFound, "no token")})

	_, err := c.authedContext(context.Background())
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
