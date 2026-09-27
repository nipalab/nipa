package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// EnvPort pins the daemon loopback port; 0 picks a free port.
const EnvPort = "NIPA_DAEMON_PORT"

// LoginFunc stores credentials for host so the daemon owns the session.
type LoginFunc func(ctx context.Context, host, username, password string) error

// Options configures a daemon Server.
type Options struct {
	EndpointPath string // discovery file; defaults to EndpointPath()
	Port         int    // loopback port; 0 uses NIPA_DAEMON_PORT or a free port
	Version      string // defaults to Version
	Login        LoginFunc
	Runners      Runners // per-root long-operation usecase factories
}

// Server hosts the loopback gRPC API for GUI clients. It owns no working-copy
// state yet: phase 2 only covers discovery, auth and lifecycle.
type Server struct {
	daemonpb.UnimplementedNipaDaemonServer

	endpointPath string
	version      string
	token        string
	pid          int
	login        LoginFunc

	listener net.Listener
	grpc     *grpc.Server
	repos    *registry
	ready    chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
}

var _ daemonpb.NipaDaemonServer = (*Server)(nil)

// NewServer binds the loopback listener and prepares the gRPC service. The
// endpoint file is published by Serve, so clients only discover daemons that
// are actually serving.
func NewServer(opts Options) (*Server, error) {
	endpointPath := opts.EndpointPath
	if endpointPath == "" {
		path, err := EndpointPath()
		if err != nil {
			return nil, err
		}
		endpointPath = path
	}
	port := opts.Port
	if port == 0 {
		if raw := os.Getenv(EnvPort); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 0 || parsed > 65535 {
				return nil, clientDomain.NewUserError(fmt.Sprintf("invalid %s %q", EnvPort, raw))
			}
			port = parsed
		}
	}
	version := opts.Version
	if version == "" {
		version = Version
	}
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}

	s := &Server{
		endpointPath: endpointPath,
		version:      version,
		token:        token,
		pid:          os.Getpid(),
		login:        opts.Login,
		listener:     listener,
		repos:        newRegistry(opts.Runners),
		ready:        make(chan struct{}),
		stop:         make(chan struct{}),
	}
	s.grpc = grpc.NewServer(
		grpc.UnaryInterceptor(s.unaryAuth),
		grpc.StreamInterceptor(s.streamAuth),
	)
	daemonpb.RegisterNipaDaemonServer(s.grpc, s)
	return s, nil
}

// Endpoint describes the bound loopback endpoint.
func (s *Server) Endpoint() Endpoint {
	port := 0
	if addr, ok := s.listener.Addr().(*net.TCPAddr); ok {
		port = addr.Port
	}
	return Endpoint{PID: s.pid, Port: port, Token: s.token, Version: s.version}
}

// Ready is closed once the endpoint file is published and gRPC is serving.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

// Serve publishes the endpoint and serves until ctx is cancelled, Stop is
// called, or the gRPC server stops on its own. The endpoint file is removed
// only if it still holds this daemon's token.
func (s *Server) Serve(ctx context.Context) error {
	if existing, err := ReadEndpoint(s.endpointPath); err == nil {
		if existing.PID != s.pid && ProcessAlive(existing.PID) {
			return clientDomain.NewUserError(fmt.Sprintf("nipa daemon already running (pid %d, port %d)", existing.PID, existing.Port))
		}
	}
	if err := WriteEndpoint(s.endpointPath, s.Endpoint()); err != nil {
		return err
	}
	close(s.ready)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.grpc.Serve(s.listener) }()

	select {
	case <-ctx.Done():
	case <-s.stop:
	case err := <-serveErr:
		_ = RemoveEndpoint(s.endpointPath, s.token)
		s.repos.closeAll()
		return err
	}

	s.grpc.GracefulStop()
	<-serveErr
	s.repos.closeAll()
	return RemoveEndpoint(s.endpointPath, s.token)
}

// Stop requests a graceful shutdown; it is safe to call multiple times and
// from RPC handlers.
func (s *Server) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// Ping reports liveness and version; clients use it as the discovery handshake.
func (s *Server) Ping(context.Context, *daemonpb.PingRequest) (*daemonpb.PingResponse, error) {
	return &daemonpb.PingResponse{Version: s.version, Pid: int32(s.pid)}, nil
}

// Login stores credentials for host via the configured LoginFunc. The daemon
// never prompts: rejected credentials surface as RPC errors and the GUI owns
// the dialog.
func (s *Server) Login(ctx context.Context, req *daemonpb.LoginRequest) (*daemonpb.LoginResponse, error) {
	if s.login == nil {
		return nil, status.Error(codes.FailedPrecondition, "login is not configured")
	}
	if err := s.login(ctx, req.GetHost(), req.GetUsername(), req.GetPassword()); err != nil {
		return nil, toStatusError(err)
	}
	return &daemonpb.LoginResponse{}, nil
}

// Shutdown stops the daemon gracefully.
func (s *Server) Shutdown(context.Context, *daemonpb.ShutdownRequest) (*daemonpb.ShutdownResponse, error) {
	s.Stop()
	return &daemonpb.ShutdownResponse{}, nil
}

func (s *Server) unaryAuth(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if !s.tokenValid(ctx) {
		return nil, status.Error(codes.Unauthenticated, "invalid daemon token")
	}
	return handler(ctx, req)
}

func (s *Server) streamAuth(
	srv any,
	stream grpc.ServerStream,
	_ *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	if !s.tokenValid(stream.Context()) {
		return status.Error(codes.Unauthenticated, "invalid daemon token")
	}
	return handler(srv, stream)
}

func (s *Server) tokenValid(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	for _, presented := range md.Get(TokenHeader) {
		if VerifyToken(s.token, presented) {
			return true
		}
	}
	return false
}
