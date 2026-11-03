package service

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/desktop/internal/connector"
	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type fakeDaemon struct {
	daemonpb.NipaDaemonClient
	watchErr  map[string]error
	unwatched []string
	watches   int
	changes   chan *daemonpb.RepoChange
}

func (f *fakeDaemon) WatchRepo(_ context.Context, req *daemonpb.WatchRepoRequest, _ ...grpc.CallOption) (*daemonpb.WatchRepoResponse, error) {
	if err := f.watchErr[req.GetRoot()]; err != nil {
		return nil, err
	}
	f.watches++
	root := filepath.Clean(req.GetRoot())
	if filepath.Base(root) == "sub" {
		root = filepath.Dir(root)
	}
	return &daemonpb.WatchRepoResponse{Repo: &daemonpb.RepoInfo{
		Root: root, Url: "https://nipa.test/org/" + filepath.Base(root), Branch: "main", Sparse: []string{"art"},
	}}, nil
}

func (f *fakeDaemon) WatchChanges(ctx context.Context, _ *daemonpb.WatchChangesRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.RepoChange], error) {
	if f.changes == nil {
		return nil, status.Error(codes.Unimplemented, "no changes")
	}
	return &chanStream[daemonpb.RepoChange]{ctx: ctx, ch: f.changes}, nil
}

func (f *fakeDaemon) UnwatchRepo(_ context.Context, req *daemonpb.UnwatchRepoRequest, _ ...grpc.CallOption) (*daemonpb.UnwatchRepoResponse, error) {
	f.unwatched = append(f.unwatched, req.GetRoot())
	return &daemonpb.UnwatchRepoResponse{}, nil
}

type fakeSession struct {
	client *fakeDaemon
	closed int
}

func (s *fakeSession) Client() daemonpb.NipaDaemonClient { return s.client }
func (s *fakeSession) Info() DaemonInfo                  { return DaemonInfo{Version: "v1", PID: 42, Port: 7000} }
func (s *fakeSession) Close() error                      { s.closed++; return nil }

type memStore struct {
	repos  []string
	err    error
	addErr error
}

func (m *memStore) Repos() ([]string, error) { return slices.Clone(m.repos), m.err }
func (m *memStore) Add(root string) error {
	if m.addErr != nil {
		return m.addErr
	}
	if !slices.Contains(m.repos, root) {
		m.repos = append(m.repos, root)
	}
	return nil
}
func (m *memStore) Remove(root string) error {
	m.repos = slices.DeleteFunc(m.repos, func(r string) bool { return r == root })
	return m.err
}

type harness struct {
	svc     *Service
	daemon  *fakeDaemon
	store   *memStore
	dials   int
	session *fakeSession
	dialErr error
	picked  string
	pickErr error
}

func newHarness() *harness {
	h := &harness{daemon: &fakeDaemon{watchErr: map[string]error{}}, store: &memStore{}}
	h.svc = New(func(context.Context) (Session, error) {
		h.dials++
		if h.dialErr != nil {
			return nil, h.dialErr
		}
		h.session = &fakeSession{client: h.daemon}
		return h.session, nil
	}, h.store, func() (string, error) { return h.picked, h.pickErr })
	return h
}

var ctx = context.Background()

func TestService_ConnectCachesSession(t *testing.T) {
	h := newHarness()
	info, err := h.svc.Connect(ctx)
	require.NoError(t, err)
	require.Equal(t, "v1", info.Version)
	_, err = h.svc.Connect(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, h.dials)

	require.NoError(t, h.svc.ServiceShutdown())
	require.Equal(t, 1, h.session.closed)
	require.NoError(t, h.svc.ServiceShutdown())
}

func TestService_ConnectError(t *testing.T) {
	h := newHarness()
	h.dialErr = connector.ErrBinaryNotFound
	_, err := h.svc.Connect(ctx)
	require.ErrorIs(t, err, connector.ErrBinaryNotFound)
}

func TestService_ReposKeepsBrokenClones(t *testing.T) {
	h := newHarness()
	h.store.repos = []string{"/work/game", "/work/gone"}
	h.daemon.watchErr["/work/gone"] = status.Error(codes.InvalidArgument, "/work/gone is not a nipa clone")

	repos, err := h.svc.Repos(ctx)
	require.NoError(t, err)
	require.Len(t, repos, 2)
	require.Equal(t, Repo{Root: "/work/game", Name: "game", URL: "https://nipa.test/org/game", Branch: "main", Sparse: []string{"art"}}, repos[0])
	require.Equal(t, "gone", repos[1].Name)
	require.Equal(t, "/work/gone is not a nipa clone", repos[1].Error)
}

func TestService_ReposReconnectsAfterDaemonLoss(t *testing.T) {
	h := newHarness()
	h.store.repos = []string{"/work/game"}
	h.daemon.watchErr["/work/game"] = status.Error(codes.Unavailable, "connection refused")

	_, err := h.svc.Repos(ctx)
	require.EqualError(t, err, "connection refused")
	require.Equal(t, 1, h.session.closed)

	delete(h.daemon.watchErr, "/work/game")
	repos, err := h.svc.Repos(ctx)
	require.NoError(t, err)
	require.Len(t, repos, 1)
	require.Equal(t, 2, h.dials)
}

func TestService_ReposErrors(t *testing.T) {
	h := newHarness()
	h.store.err = errors.New("disk full")
	_, err := h.svc.Repos(ctx)
	require.EqualError(t, err, "disk full")

	h = newHarness()
	h.store.repos = []string{"/a"}
	h.dialErr = errors.New("no daemon")
	_, err = h.svc.Repos(ctx)
	require.EqualError(t, err, "no daemon")
}

func TestService_ReposDialsOnceWhenDaemonCannotStart(t *testing.T) {
	h := newHarness()
	h.store.repos = []string{"/a", "/b", "/c"}
	h.dialErr = errors.New("nipa daemon did not start")

	_, err := h.svc.Repos(ctx)
	require.EqualError(t, err, "nipa daemon did not start")
	require.Equal(t, 1, h.dials)
}

func TestService_AddAndPickRepo(t *testing.T) {
	h := newHarness()
	repo, err := h.svc.AddRepo(ctx, "/work/game/sub")
	require.NoError(t, err)
	require.Equal(t, "/work/game", repo.Root)
	require.Equal(t, []string{"/work/game"}, h.store.repos)

	picked, err := h.svc.PickRepo(ctx)
	require.NoError(t, err)
	require.Nil(t, picked, "a cancelled dialog adds nothing")

	h.picked = "/work/tools"
	picked, err = h.svc.PickRepo(ctx)
	require.NoError(t, err)
	require.Equal(t, "tools", picked.Name)
	require.Equal(t, []string{"/work/game", "/work/tools"}, h.store.repos)

	h.pickErr = errors.New("no display")
	_, err = h.svc.PickRepo(ctx)
	require.EqualError(t, err, "no display")
}

func TestService_AddRepoErrors(t *testing.T) {
	h := newHarness()
	h.daemon.watchErr["/tmp"] = status.Error(codes.InvalidArgument, "/tmp is not inside a nipa clone")
	_, err := h.svc.AddRepo(ctx, "/tmp")
	require.EqualError(t, err, "/tmp is not inside a nipa clone")
	require.Empty(t, h.store.repos)

	h.picked = "/tmp"
	_, err = h.svc.PickRepo(ctx)
	require.Error(t, err)

	h.store.addErr = errors.New("read-only")
	_, err = h.svc.AddRepo(ctx, "/work/game")
	require.EqualError(t, err, "read-only")
}

func TestService_RemoveRepo(t *testing.T) {
	h := newHarness()
	h.store.repos = []string{"/work/game", "/work/tools"}
	require.NoError(t, h.svc.RemoveRepo(ctx, "/work/game"))
	require.Equal(t, []string{"/work/tools"}, h.store.repos)
	require.Equal(t, []string{"/work/game"}, h.daemon.unwatched)

	h.dialErr = errors.New("no daemon")
	h.svc = New(h.svc.dial, h.store, h.svc.pick)
	require.NoError(t, h.svc.RemoveRepo(ctx, "/work/tools"))
	require.Empty(t, h.store.repos)
}

func TestCleanError(t *testing.T) {
	plain := errors.New("plain")
	require.Same(t, plain, cleanError(plain))
	require.EqualError(t, cleanError(status.Error(codes.NotFound, "missing")), "missing")
}

func TestConnectorDialerWithRealDaemon(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "daemon.json")
	srv, err := daemon.NewServer(daemon.Options{EndpointPath: endpoint, Version: "real"})
	require.NoError(t, err)
	serveCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(serveCtx) }()
	defer func() { cancel(); <-done }()
	<-srv.Ready()

	svc := New(ConnectorDialer(connector.Options{EndpointPath: endpoint}), &memStore{}, nil)
	info, err := svc.Connect(ctx)
	require.NoError(t, err)
	require.Equal(t, "real", info.Version)
	require.Equal(t, srv.Endpoint().Port, info.Port)
	require.False(t, info.Started)

	_, err = svc.AddRepo(ctx, t.TempDir())
	require.Error(t, err, "a plain directory is not a clone")
	require.NoError(t, svc.ServiceShutdown())

	t.Setenv(connector.EnvNipaBinary, "")
	failing := New(ConnectorDialer(connector.Options{EndpointPath: filepath.Join(t.TempDir(), "none.json"), Binary: filepath.Join(t.TempDir(), "missing")}), &memStore{}, nil)
	_, err = failing.Connect(ctx)
	require.Error(t, err)
}
