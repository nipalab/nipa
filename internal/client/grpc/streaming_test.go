package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
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
		"",
		"",
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
		[]*serverDomain.PushFile{{Path: "run.sh", Mode: 0o755, FileHash: serverDomain.Hash{}, ChunkHashes: []serverDomain.Hash{{0x01}}}}, nil, "", "")
	require.NoError(t, err)
	require.Equal(t, pb.FileMode_FILE_MODE_EXECUTABLE, fs.lastPushReq.GetFiles()[0].GetMode())
}

func TestClient_Push_ServerError(t *testing.T) {
	addr := startTestServer(t, &fakeServer{pushErr: status.Error(codes.FailedPrecondition, "branch moved")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.Push(context.Background(), "org", "project", "main", "stale", "m", nil, nil, "", "")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "branch moved", domErr.Message)
}

func TestClient_Push_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.Push(context.Background(), "org", "project", "main", "", "m", nil, nil, "", "")
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestAuthedContext_NoToken(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{getTokenErr: status.Error(codes.NotFound, "no token")})

	_, err := c.authedContext(context.Background())
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}
