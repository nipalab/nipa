package grpc

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
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
}

func (f *fakeServer) GetDefaultBranch(_ context.Context, _ *pb.GetDefaultBranchRequest) (*pb.GetBranchResponse, error) {
	if f.defaultBranchErr != nil {
		return nil, f.defaultBranchErr
	}
	return &pb.GetBranchResponse{Branch: f.defaultBranch}, nil
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