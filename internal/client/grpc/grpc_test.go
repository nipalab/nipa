package grpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

type stubSession struct {
	accessToken  string
	getTokenErr  error
	refreshToken string
	refreshErr   error
	refreshCalls int
}

func (s *stubSession) AccessToken(_ context.Context, _ string) (string, error) {
	return s.accessToken, s.getTokenErr
}

func (s *stubSession) Refresh(_ context.Context, _ string) (string, error) {
	s.refreshCalls++
	return s.refreshToken, s.refreshErr
}

type fakeServer struct {
	pb.UnimplementedNipaServiceServer
	loginErr         error
	refreshErr       error
	accessToken      string
	refreshToken     string
	expiresIn        int32
	lastUsername     string
	lastPassword     string
	lastRefreshToken string
	defaultBranchErr error
	defaultBranch    *pb.Branch
	treeManifestErr  error
	treeManifest     *pb.TreeManifest
	lastBranch       string
	lastRecursive    bool
	lastPath         string
}

func (f *fakeServer) GetDefaultBranch(_ context.Context, _ *pb.GetDefaultBranchRequest) (*pb.GetBranchResponse, error) {
	if f.defaultBranchErr != nil {
		return nil, f.defaultBranchErr
	}
	return &pb.GetBranchResponse{Branch: f.defaultBranch}, nil
}

func (f *fakeServer) GetTreeManifest(_ context.Context, req *pb.GetTreeManifestRequest) (*pb.GetTreeManifestResponse, error) {
	f.lastBranch = req.GetBranch()
	f.lastRecursive = req.GetRecursive()
	f.lastPath = req.GetPath()
	if f.treeManifestErr != nil {
		return nil, f.treeManifestErr
	}
	return &pb.GetTreeManifestResponse{Branch: "main", RootTree: f.treeManifest}, nil
}

func (f *fakeServer) LoginWithUsernamePassword(_ context.Context, req *pb.LoginUsernamePasswordRequest) (*pb.LoginResponse, error) {
	f.lastUsername = req.GetUsername()
	f.lastPassword = req.GetPassword()
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	return &pb.LoginResponse{
		AccessToken:  f.accessToken,
		RefreshToken: f.refreshToken,
		ExpiresIn:    f.expiresIn,
	}, nil
}

func (f *fakeServer) LoginWithRefreshToken(_ context.Context, req *pb.LoginWithRefreshRequest) (*pb.LoginResponse, error) {
	f.lastRefreshToken = req.GetRefreshToken()
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	return &pb.LoginResponse{
		AccessToken:  f.accessToken,
		RefreshToken: f.refreshToken,
		ExpiresIn:    f.expiresIn,
	}, nil
}

func startTestServer(t *testing.T, srv pb.NipaServiceServer) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	gs := grpc.NewServer()
	pb.RegisterNipaServiceServer(gs, srv)
	go func() {
		_ = gs.Serve(lis)
	}()
	t.Cleanup(gs.Stop)
	return lis.Addr().String()
}

func TestNewClient(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	require.NotNil(t, c)
	require.Nil(t, c.transport.clientConn)
}

func TestClient_Close_NoConnection(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})
	require.NoError(t, c.Close())
}

func TestClient_ConnectAndClose(t *testing.T) {
	addr := startTestServer(t, &fakeServer{accessToken: "tok", refreshToken: "ref", expiresIn: 1800})
	c := NewClient(NewTransport(), &stubSession{})

	require.NoError(t, c.Connect(context.Background(), addr))
	require.Equal(t, addr, c.transport.url)
	require.NotNil(t, c.transport.clientConn)

	require.NoError(t, c.Close())
}

func TestClient_LoginWithUsernamePassword(t *testing.T) {
	addr := startTestServer(t, &fakeServer{accessToken: "access", refreshToken: "refresh", expiresIn: 1800})
	c := NewClient(NewTransport(), &stubSession{accessToken: "access"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.LoginWithUsernamePassword(context.Background(), addr, "apin", "secret")
	require.NoError(t, err)
	require.Equal(t, &domain.LoginResult{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresIn:    1800,
		Host:         addr,
	}, got)
}

func TestClient_LoginWithUsernamePassword_ServerError(t *testing.T) {
	addr := startTestServer(t, &fakeServer{loginErr: status.Error(codes.InvalidArgument, "bad credentials")})
	c := NewClient(NewTransport(), &stubSession{})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.LoginWithUsernamePassword(context.Background(), addr, "apin", "secret")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "bad credentials", domErr.Message)
}

func TestClient_LoginWithRefreshToken(t *testing.T) {
	addr := startTestServer(t, &fakeServer{accessToken: "access", refreshToken: "refresh", expiresIn: 1800})
	c := NewClient(NewTransport(), &stubSession{accessToken: "access"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.LoginWithRefreshToken(context.Background(), addr, "refresh")
	require.NoError(t, err)
	require.Equal(t, &domain.LoginResult{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresIn:    1800,
		Host:         addr,
	}, got)
}

func TestClient_LoginWithRefreshToken_ServerError(t *testing.T) {
	addr := startTestServer(t, &fakeServer{refreshErr: status.Error(codes.InvalidArgument, "invalid refresh token")})
	c := NewClient(NewTransport(), &stubSession{})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.LoginWithRefreshToken(context.Background(), addr, "expired")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "invalid refresh token", domErr.Message)
}

func TestClient_LoginWithUsernamePassword_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})

	_, err := c.LoginWithUsernamePassword(context.Background(), "host", "apin", "secret")
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestClient_LoginWithRefreshToken_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{})

	_, err := c.LoginWithRefreshToken(context.Background(), "host", "token")
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestClient_LoginMethodPassthrough(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	interceptor := c.unaryAuthInterceptor()

	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		_, ok := metadata.FromOutgoingContext(ctx)
		require.False(t, ok, "authorization metadata should not be present on login methods")
		return nil
	}

	err := interceptor(context.Background(), "/greet.NipaService/LoginWithUsernamePassword", nil, nil, nil, invoker)
	require.NoError(t, err)
}

func TestClient_AuthenticatedRequest(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "abc123"})
	interceptor := c.unaryAuthInterceptor()

	var gotAuth string
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		md, _ := metadata.FromOutgoingContext(ctx)
		gotAuth = md.Get("authorization")[0]
		return nil
	}

	err := interceptor(context.Background(), "/greet.NipaService/GetBranch", nil, nil, nil, invoker)
	require.NoError(t, err)
	require.Equal(t, "Bearer abc123", gotAuth)
}

func TestClient_AccessTokenError(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{getTokenErr: status.Error(codes.NotFound, "no token")})
	interceptor := c.unaryAuthInterceptor()

	err := interceptor(context.Background(), "/greet.NipaService/GetBranch", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		return nil
	})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestClient_RefreshAndRetry(t *testing.T) {
	session := &stubSession{accessToken: "old-token", refreshToken: "new-token"}
	c := NewClient(NewTransport(), session)
	interceptor := c.unaryAuthInterceptor()

	firstCall := true
	var gotAuth string
	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		if firstCall {
			firstCall = false
			return status.Error(codes.Unauthenticated, "token expired")
		}
		md, _ := metadata.FromOutgoingContext(ctx)
		gotAuth = md.Get("authorization")[0]
		return nil
	}

	err := interceptor(context.Background(), "/greet.NipaService/GetBranch", nil, nil, nil, invoker)
	require.NoError(t, err)
	require.Equal(t, 1, session.refreshCalls)
	require.Equal(t, "Bearer new-token", gotAuth)
}

func TestClient_RefreshFails(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{
		accessToken: "old-token",
		refreshErr:  status.Error(codes.Unavailable, "network down"),
	})
	interceptor := c.unaryAuthInterceptor()

	invoker := func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		return status.Error(codes.Unauthenticated, "token expired")
	}

	err := interceptor(context.Background(), "/greet.NipaService/GetBranch", nil, nil, nil, invoker)
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestClient_GetDefaultBranch_NotFound(t *testing.T) {
	addr := startTestServer(t, &fakeServer{defaultBranchErr: status.Error(codes.NotFound, `project "sample" not found`)})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.GetDefaultBranch(context.Background(), "default", "sample")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `project "sample" not found`, domErr.Message)
}

func TestClient_GetDefaultBranch_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.GetDefaultBranch(context.Background(), "default", "sample")
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestToDomainError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{"not found", status.Error(codes.NotFound, `project "sample" not found`), 404, `project "sample" not found`},
		{"invalid argument", status.Error(codes.InvalidArgument, "bad credentials"), 400, "bad credentials"},
		{"unauthenticated", status.Error(codes.Unauthenticated, "invalid token"), 401, "invalid token"},
		{"permission denied", status.Error(codes.PermissionDenied, "no permission"), 403, "no permission"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := toDomainError(tt.err)
			require.Error(t, err)

			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
			require.Equal(t, tt.code, domErr.Code)
			require.Equal(t, tt.msg, domErr.Message)
		})
	}
}

func TestToDomainError_UnmappedCodePassthrough(t *testing.T) {
	err := toDomainError(status.Error(codes.Unavailable, "network down"))
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestToDomainError_Nil(t *testing.T) {
	require.NoError(t, toDomainError(nil))
}

func TestToDomainError_NonStatusError(t *testing.T) {
	raw := errors.New("raw error")
	require.Equal(t, raw, toDomainError(raw))
}

func TestTransport_Connect_Reconnect(t *testing.T) {
	addr1 := startTestServer(t, &fakeServer{accessToken: "tok"})
	addr2 := startTestServer(t, &fakeServer{accessToken: "tok"})

	tr := NewTransport()
	require.NoError(t, tr.Connect(addr1))
	require.Equal(t, addr1, tr.url)
	conn1 := tr.clientConn

	require.NoError(t, tr.Connect(addr2))
	require.Equal(t, addr2, tr.url)
	require.NotSame(t, conn1, tr.clientConn, "connecting to a different url should replace the connection")
}

func TestClient_GetDefaultBranch_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	commitID := snow.ID(7).Base36()
	fs := &fakeServer{defaultBranch: &pb.Branch{
		Id:          snow.ID(42).Base36(),
		Name:        "main",
		IsProtected: true,
		IsDefault:   true,
		CommitId:    &commitID,
		CreatedAt:   timestamppb.New(now),
		UpdatedAt:   timestamppb.New(now.Add(time.Hour)),
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.GetDefaultBranch(context.Background(), "default", "sample")
	require.NoError(t, err)
	require.Equal(t, snow.ID(42), got.ID)
	require.Equal(t, "main", got.Name)
	require.True(t, got.IsProtected)
	require.True(t, got.IsDefault)
	require.NotNil(t, got.CommitID)
	require.Equal(t, snow.ID(7), *got.CommitID)
	require.Equal(t, now, got.CreatedAt)
	require.Equal(t, now.Add(time.Hour), got.UpdatedAt)
}

func TestClient_GetTreeNodeManifest_Success(t *testing.T) {
	var hash1 serverDomain.Hash
	hash1[0] = 0x01
	var hash2 serverDomain.Hash
	hash2[31] = 0x02
	var chunkHash serverDomain.Hash
	chunkHash[15] = 0xab

	fs := &fakeServer{treeManifest: &pb.TreeManifest{
		Path: "root",
		Files: []*pb.FileNode{{
			Path:        "README.md",
			Mode:        pb.FileMode_FILE_MODE_READ_WRITE,
			SizeBytes:   12,
			ChunkHashes: []string{hash1.String(), "not-a-valid-hash", hash2.String()},
		}},
		SubTrees: []*pb.TreeManifest{{
			Path: "assets",
			Files: []*pb.FileNode{{
				Path:        "logo.png",
				Mode:        pb.FileMode_FILE_MODE_EXECUTABLE,
				SizeBytes:   99,
				IsBinary:    true,
				ChunkHashes: []string{chunkHash.String()},
			}},
		}},
	}}
	addr := startTestServer(t, fs)
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	got, err := c.GetTreeNodeManifest(context.Background(), "default", "sample", "main", "src/sedotan")
	require.NoError(t, err)
	require.Equal(t, "main", fs.lastBranch, "client should send the requested branch")
	require.True(t, fs.lastRecursive, "client should request a recursive manifest")
	require.Equal(t, "src/sedotan", fs.lastPath, "client should send the requested path")

	require.Equal(t, "root", got.Name)
	require.Len(t, got.FileChildren, 1)
	file := got.FileChildren[0]
	require.Equal(t, "README.md", file.Name)
	require.Equal(t, int(pb.FileMode_FILE_MODE_READ_WRITE), file.Mode)
	require.Equal(t, int64(12), file.SizeBytes)
	require.Len(t, file.Chunks, 2, "invalid chunk hashes should be skipped")
	require.Equal(t, hash1, file.Chunks[0].Hash)
	require.Equal(t, hash2, file.Chunks[1].Hash)

	require.Len(t, got.TreeChildren, 1)
	assets := got.TreeChildren[0]
	require.Equal(t, "assets", assets.Name)
	require.Len(t, assets.FileChildren, 1)
	require.Equal(t, "logo.png", assets.FileChildren[0].Name)
	require.Equal(t, int(pb.FileMode_FILE_MODE_EXECUTABLE), assets.FileChildren[0].Mode)
	require.True(t, assets.FileChildren[0].IsBinary)
}

func TestClient_GetTreeNodeManifest_NotFound(t *testing.T) {
	addr := startTestServer(t, &fakeServer{
		treeManifestErr: status.Error(codes.NotFound, `path "missing" not found in branch "main"`),
	})
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})
	require.NoError(t, c.Connect(context.Background(), addr))

	_, err := c.GetTreeNodeManifest(context.Background(), "default", "sample", "main", "missing")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `path "missing" not found in branch "main"`, domErr.Message)
}

func TestClient_GetTreeNodeManifest_NotConnected(t *testing.T) {
	c := NewClient(NewTransport(), &stubSession{accessToken: "tok"})

	_, err := c.GetTreeNodeManifest(context.Background(), "default", "sample", "main", "src")
	require.Error(t, err)
	require.Equal(t, "not connected to a nipa server", err.Error())
}

func TestToServerBranch(t *testing.T) {
	require.Nil(t, toServerBranch(nil))

	now := time.Now().UTC().Truncate(time.Microsecond)
	commitID := snow.ID(7).Base36()
	got := toServerBranch(&pb.Branch{
		Id:          snow.ID(42).Base36(),
		Name:        "main",
		IsProtected: true,
		IsDefault:   true,
		CommitId:    &commitID,
		CreatedAt:   timestamppb.New(now),
		UpdatedAt:   timestamppb.New(now.Add(time.Hour)),
	})
	require.Equal(t, snow.ID(42), got.ID)
	require.Equal(t, "main", got.Name)
	require.True(t, got.IsProtected)
	require.True(t, got.IsDefault)
	require.NotNil(t, got.CommitID)
	require.Equal(t, snow.ID(7), *got.CommitID)
	require.Equal(t, now, got.CreatedAt)
	require.Equal(t, now.Add(time.Hour), got.UpdatedAt)
}

func TestToServerBranch_NoCommit(t *testing.T) {
	empty := ""
	got := toServerBranch(&pb.Branch{Id: snow.ID(1).Base36(), Name: "main", CommitId: &empty})
	require.NotNil(t, got)
	require.Nil(t, got.CommitID)
}

func TestToServerTreeNode(t *testing.T) {
	require.Nil(t, toServerTreeNode(nil))

	got := toServerTreeNode(&pb.TreeManifest{
		Path: "root",
		SubTrees: []*pb.TreeManifest{
			{Path: "assets"},
		},
	})
	require.Equal(t, "root", got.Name)
	require.Len(t, got.TreeChildren, 1)
	require.Equal(t, "assets", got.TreeChildren[0].Name)
	require.Empty(t, got.TreeChildren[0].TreeChildren)
	require.Empty(t, got.TreeChildren[0].FileChildren)
}

func TestToServerFile(t *testing.T) {
	require.Nil(t, toServerFile(nil))

	var h serverDomain.Hash
	h[0] = 0xbe

	got := toServerFile(&pb.FileNode{
		Path:        "main.go",
		Mode:        pb.FileMode_FILE_MODE_READ_ONLY,
		SizeBytes:   42,
		ChunkHashes: []string{h.String(), "not-a-valid-hash"},
	})
	require.Equal(t, "main.go", got.Name)
	require.Equal(t, int(pb.FileMode_FILE_MODE_READ_ONLY), got.Mode)
	require.Equal(t, int64(42), got.SizeBytes)
	require.Len(t, got.Chunks, 1)
	require.Equal(t, h, got.Chunks[0].Hash)
}

func TestDecodeHash(t *testing.T) {
	var h serverDomain.Hash
	for i := range h {
		h[i] = byte(i)
	}

	got, err := decodeHash(h.String())
	require.NoError(t, err)
	require.Equal(t, h, got)

	_, err = decodeHash("abcd")
	require.Error(t, err)

	_, err = decodeHash(strings.Repeat("z", 64))
	require.Error(t, err)
}