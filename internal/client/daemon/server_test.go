package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type testServer struct {
	*Server
	client daemonpb.NipaDaemonClient
	done   chan error
}

func startTestServer(t *testing.T, opts Options) *testServer {
	t.Helper()
	if opts.EndpointPath == "" {
		opts.EndpointPath = filepath.Join(t.TempDir(), "daemon.json")
	}
	srv, err := NewServer(opts)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("server exited before ready: %v", err)
	}

	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", srv.Endpoint().Port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	return &testServer{Server: srv, client: daemonpb.NewNipaDaemonClient(conn), done: done}
}

func authed(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, TokenHeader, token)
}

func TestServer_PingRequiresToken(t *testing.T) {
	srv := startTestServer(t, Options{Version: "1.2.3"})

	_, err := srv.client.Ping(context.Background(), &daemonpb.PingRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err), "missing token must be rejected")

	res, err := srv.client.Ping(authed(context.Background(), srv.Endpoint().Token), &daemonpb.PingRequest{})
	require.NoError(t, err)
	require.Equal(t, "1.2.3", res.GetVersion())
	require.Equal(t, int32(os.Getpid()), res.GetPid())
}

func TestServer_Login(t *testing.T) {
	var gotHost, gotUser, gotPass string
	srv := startTestServer(t, Options{
		Login: func(_ context.Context, host, username, password string) error {
			gotHost, gotUser, gotPass = host, username, password
			return nil
		},
	})

	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err := srv.client.Login(ctx, &daemonpb.LoginRequest{
		Host:     "nipa.example.com",
		Username: "alice",
		Password: "hunter2",
	})
	require.NoError(t, err)
	require.Equal(t, "nipa.example.com", gotHost)
	require.Equal(t, "alice", gotUser)
	require.Equal(t, "hunter2", gotPass)
}

func TestServer_LoginErrorIsMapped(t *testing.T) {
	srv := startTestServer(t, Options{
		Login: func(context.Context, string, string, string) error {
			return clientDomain.NewTokenError("bad credentials")
		},
	})

	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err := srv.client.Login(ctx, &daemonpb.LoginRequest{Host: "h", Username: "u", Password: "p"})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "bad credentials")
}

func TestServer_LoginWithoutHandler(t *testing.T) {
	srv := startTestServer(t, Options{})

	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err := srv.client.Login(ctx, &daemonpb.LoginRequest{Host: "h", Username: "u", Password: "p"})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestServer_ShutdownRemovesEndpoint(t *testing.T) {
	srv := startTestServer(t, Options{})

	_, err := ReadEndpoint(srv.endpointPath)
	require.NoError(t, err, "the endpoint file is published while serving")

	ctx := authed(context.Background(), srv.Endpoint().Token)
	_, err = srv.client.Shutdown(ctx, &daemonpb.ShutdownRequest{})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, err := ReadEndpoint(srv.endpointPath)
		return err != nil
	}, 5*time.Second, 10*time.Millisecond, "the endpoint file must be removed on shutdown")
}

func TestServer_ServeRejectsLiveDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pid 1 liveness check is unix-specific")
	}
	path := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, WriteEndpoint(path, Endpoint{PID: 1, Port: 1, Token: "other"}))

	srv, err := NewServer(Options{EndpointPath: path})
	require.NoError(t, err)
	defer func() {
		srv.Stop()
		_ = srv.listener.Close()
	}()

	err = srv.Serve(context.Background())
	require.ErrorContains(t, err, "already running")
}

func TestNewServer_PortFromEnv(t *testing.T) {
	t.Setenv(EnvPort, "0")
	srv, err := NewServer(Options{EndpointPath: filepath.Join(t.TempDir(), "daemon.json")})
	require.NoError(t, err)
	defer func() { _ = srv.listener.Close() }()
	require.NotZero(t, srv.Endpoint().Port)
}

func TestNewServer_DefaultEndpointPath(t *testing.T) {
	srv, err := NewServer(Options{})
	require.NoError(t, err)
	defer func() { _ = srv.listener.Close() }()

	want, err := EndpointPath()
	require.NoError(t, err)
	require.Equal(t, want, srv.endpointPath)
}

func TestNewServer_PortInUse(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = busy.Close() }()
	port := busy.Addr().(*net.TCPAddr).Port

	_, err = NewServer(Options{Port: port})
	require.Error(t, err)
}

func TestServer_ServeFailsOnUnwritableEndpoint(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "daemon.json")
	require.NoError(t, os.Mkdir(blocked, 0o700))

	srv, err := NewServer(Options{EndpointPath: blocked})
	require.NoError(t, err)
	defer func() {
		srv.Stop()
		_ = srv.listener.Close()
	}()

	require.Error(t, srv.Serve(context.Background()), "publishing the endpoint must fail cleanly")
}

func TestServer_GRPCStopExitsServe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	srv, err := NewServer(Options{EndpointPath: path})
	require.NoError(t, err)

	srv.grpc.Stop()
	require.Error(t, srv.Serve(context.Background()))
	_, statErr := ReadEndpoint(path)
	require.ErrorIs(t, statErr, os.ErrNotExist, "the endpoint is cleaned up when gRPC stops")
}

func TestServer_StreamRequiresToken(t *testing.T) {
	srv := startTestServer(t, Options{})

	stream, err := srv.client.Update(context.Background(), &daemonpb.UpdateRequest{Root: "/nope"})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestNewServer_InvalidEnvPort(t *testing.T) {
	t.Setenv(EnvPort, "nope")
	_, err := NewServer(Options{EndpointPath: filepath.Join(t.TempDir(), "daemon.json")})
	require.ErrorContains(t, err, EnvPort)

	t.Setenv(EnvPort, "70000")
	_, err = NewServer(Options{EndpointPath: filepath.Join(t.TempDir(), "daemon.json")})
	require.ErrorContains(t, err, EnvPort)
}
