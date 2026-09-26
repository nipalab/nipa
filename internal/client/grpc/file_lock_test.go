package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeFileLockServer struct {
	pb.UnimplementedNipaServiceServer

	lockReq  *pb.LockFileRequest
	lockResp *pb.LockFileResponse
	lockErr  error

	unlockReq *pb.UnlockFileRequest
	unlockErr error

	listResp *pb.ListFileLocksResponse
	listErr  error
}

func (f *fakeFileLockServer) LockFile(_ context.Context, req *pb.LockFileRequest) (*pb.LockFileResponse, error) {
	f.lockReq = req
	if f.lockErr != nil {
		return nil, f.lockErr
	}
	return f.lockResp, nil
}

func (f *fakeFileLockServer) UnlockFile(_ context.Context, req *pb.UnlockFileRequest) (*pb.UnlockFileResponse, error) {
	f.unlockReq = req
	if f.unlockErr != nil {
		return nil, f.unlockErr
	}
	return &pb.UnlockFileResponse{}, nil
}

func (f *fakeFileLockServer) ListFileLocks(_ context.Context, _ *pb.ListFileLocksRequest) (*pb.ListFileLocksResponse, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResp, nil
}

func fileLockDetail() *pb.FileLockDetail {
	mrNumber := int64(7)
	return &pb.FileLockDetail{
		Id:                 snow.ID(5).Base36(),
		Path:               "assets/orc.png",
		Branch:             "main",
		Global:             true,
		HeldBy:             snow.ID(9).Base36(),
		HeldByName:         "bob",
		MergeRequestNumber: &mrNumber,
		AcquiredAt:         timestamppb.New(time.Unix(100, 0)),
	}
}

func newFileLockTestClient(t *testing.T, srv pb.NipaServiceServer) *Client {
	t.Helper()
	addr := startTestServer(t, srv)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))
	return c
}

func TestClient_LockFile_Success(t *testing.T) {
	fs := &fakeFileLockServer{lockResp: &pb.LockFileResponse{Lock: fileLockDetail()}}
	c := newFileLockTestClient(t, fs)

	got, err := c.LockFile(context.Background(), "default", "sample", "assets/orc.png", "main")
	require.NoError(t, err)
	require.NotNil(t, fs.lockReq)
	require.Equal(t, "default", fs.lockReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.lockReq.GetContext().GetProject())
	require.Equal(t, "assets/orc.png", fs.lockReq.GetPath())
	require.Equal(t, "main", fs.lockReq.GetBranch())

	require.Equal(t, snow.ID(5).Base36(), got.ID)
	require.Equal(t, "assets/orc.png", got.Path)
	require.Equal(t, "main", got.Branch)
	require.True(t, got.Global)
	require.Equal(t, snow.ID(9).Base36(), got.HeldBy)
	require.Equal(t, "bob", got.HeldByName)
	require.NotNil(t, got.MergeRequestNumber)
	require.Equal(t, int64(7), *got.MergeRequestNumber)
	require.Equal(t, time.Unix(100, 0).UTC(), got.AcquiredAt.UTC())
}

func TestClient_LockFile_Error(t *testing.T) {
	addr := startTestServer(t, &fakeFileLockServer{lockErr: status.Error(codes.FailedPrecondition, "locked by bob")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.LockFile(context.Background(), "default", "sample", "a.png", "")
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, "locked by bob", domErr.Message)
}

func TestClient_LockFile_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	_, err := c.LockFile(context.Background(), "default", "sample", "a.png", "")
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_UnlockFile_Success(t *testing.T) {
	fs := &fakeFileLockServer{}
	c := newFileLockTestClient(t, fs)

	require.NoError(t, c.UnlockFile(context.Background(), "default", "sample", "assets/orc.png", "main"))
	require.NotNil(t, fs.unlockReq)
	require.Equal(t, "default", fs.unlockReq.GetContext().GetOrg())
	require.Equal(t, "sample", fs.unlockReq.GetContext().GetProject())
	require.Equal(t, "assets/orc.png", fs.unlockReq.GetPath())
	require.Equal(t, "main", fs.unlockReq.GetBranch())
}

func TestClient_UnlockFile_Error(t *testing.T) {
	addr := startTestServer(t, &fakeFileLockServer{unlockErr: status.Error(codes.NotFound, "no lock")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	err := c.UnlockFile(context.Background(), "default", "sample", "a.png", "")
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestClient_UnlockFile_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	err := c.UnlockFile(context.Background(), "default", "sample", "a.png", "")
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestClient_ListFileLocks_Success(t *testing.T) {
	fs := &fakeFileLockServer{listResp: &pb.ListFileLocksResponse{Locks: []*pb.FileLockDetail{fileLockDetail()}}}
	c := newFileLockTestClient(t, fs)

	locks, err := c.ListFileLocks(context.Background(), "default", "sample")
	require.NoError(t, err)
	require.Len(t, locks, 1)
	require.Equal(t, snow.ID(5).Base36(), locks[0].ID)
}

func TestClient_ListFileLocks_Error(t *testing.T) {
	addr := startTestServer(t, &fakeFileLockServer{listErr: status.Error(codes.PermissionDenied, "no permission")})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.ListFileLocks(context.Background(), "default", "sample")
	require.Error(t, err)

	var domErr *clientDomain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestClient_ListFileLocks_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	_, err := c.ListFileLocks(context.Background(), "default", "sample")
	require.EqualError(t, err, "not connected to a nipa server")
}

func TestToClientFileLock_Nil(t *testing.T) {
	require.Nil(t, toClientFileLock(nil))
}
