package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type stubPinRunner struct {
	pinned, unpinned []string
	roots            []string
	pinErr, unpinErr error
}

func (s *stubPinRunner) Pin(_ context.Context, root, path string) error {
	s.roots = append(s.roots, root)
	s.pinned = append(s.pinned, path)
	return s.pinErr
}

func (s *stubPinRunner) Unpin(_ context.Context, root, path string) error {
	s.roots = append(s.roots, root)
	s.unpinned = append(s.unpinned, path)
	return s.unpinErr
}

func pinnedProxyServer(t *testing.T, conn ProxyConn, pins PinRunner) (*Server, string) {
	t.Helper()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Proxy: conn, Pins: pins})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root
}

func lockingConn(lockErr error) *fakeProxyConn {
	return &fakeProxyConn{client: &fakeService{
		lockFile: func(_ context.Context, req *pb.LockFileRequest) (*pb.LockFileResponse, error) {
			if lockErr != nil {
				return nil, lockErr
			}
			return &pb.LockFileResponse{Lock: &pb.FileLockDetail{Path: "assets/" + req.GetPath()}}, nil
		},
	}}
}

func TestServer_ProxyLockAndUnlockPin(t *testing.T) {
	pins := &stubPinRunner{}
	srv, root := pinnedProxyServer(t, lockingConn(nil), pins)

	res, err := srv.ProxyLockFile(context.Background(), &daemonpb.ProxyLockFileRequest{Root: root, Request: &pb.LockFileRequest{Path: "logo.png"}})
	require.NoError(t, err)
	require.Empty(t, res.GetWarning())
	require.Equal(t, []string{"assets/logo.png"}, pins.pinned, "the server's lock path is pinned")

	_, err = srv.ProxyUnlockFile(context.Background(), &daemonpb.ProxyUnlockFileRequest{Root: root, Request: &pb.UnlockFileRequest{Path: "assets/logo.png"}})
	require.NoError(t, err)
	require.Equal(t, []string{"assets/logo.png"}, pins.unpinned)
	require.Equal(t, []string{root, root}, pins.roots)
}

func TestServer_ProxyLockPinFailureIsWarning(t *testing.T) {
	pins := &stubPinRunner{pinErr: errors.New("offline"), unpinErr: errors.New("db")}
	srv, root := pinnedProxyServer(t, lockingConn(nil), pins)

	res, err := srv.ProxyLockFile(context.Background(), &daemonpb.ProxyLockFileRequest{Root: root, Request: &pb.LockFileRequest{Path: "logo.png"}})
	require.NoError(t, err)
	require.NotNil(t, res.GetResponse().GetLock())
	require.Contains(t, res.GetWarning(), "offline")

	_, err = srv.ProxyUnlockFile(context.Background(), &daemonpb.ProxyUnlockFileRequest{Root: root, Request: &pb.UnlockFileRequest{Path: "assets/logo.png"}})
	require.NoError(t, err, "a local unpin failure never fails a server unlock")
}

func TestServer_ProxyLockFailureDoesNotPin(t *testing.T) {
	pins := &stubPinRunner{}
	srv, root := pinnedProxyServer(t, lockingConn(errors.New("locked by someone")), pins)

	_, err := srv.ProxyLockFile(context.Background(), &daemonpb.ProxyLockFileRequest{Root: root, Request: &pb.LockFileRequest{Path: "logo.png"}})
	require.Error(t, err)
	require.Empty(t, pins.pinned)
}

func TestServer_ProxyLockWithoutPinsFallsBackToRequestPath(t *testing.T) {
	conn := &fakeProxyConn{client: &fakeService{
		lockFile: func(context.Context, *pb.LockFileRequest) (*pb.LockFileResponse, error) {
			return &pb.LockFileResponse{}, nil
		},
	}}
	pins := &stubPinRunner{}
	srv, root := pinnedProxyServer(t, conn, pins)
	_, err := srv.ProxyLockFile(context.Background(), &daemonpb.ProxyLockFileRequest{Root: root, Request: &pb.LockFileRequest{Path: "x.psd"}})
	require.NoError(t, err)
	require.Equal(t, []string{"x.psd"}, pins.pinned)

	bare, bareRoot := watchedProxyServer(t, conn)
	_, err = bare.ProxyLockFile(context.Background(), &daemonpb.ProxyLockFileRequest{Root: bareRoot, Request: &pb.LockFileRequest{Path: "x.psd"}})
	require.NoError(t, err)
}

func (f *fakeService) UnlockFile(context.Context, *pb.UnlockFileRequest, ...grpc.CallOption) (*pb.UnlockFileResponse, error) {
	return &pb.UnlockFileResponse{}, nil
}
