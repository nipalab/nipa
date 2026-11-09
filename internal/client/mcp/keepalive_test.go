package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeTicker struct {
	period  time.Duration
	ch      chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (f *fakeTicker) C() <-chan time.Time { return f.ch }
func (f *fakeTicker) Stop()               { f.once.Do(func() { close(f.stopped) }) }

type fakeTickers struct {
	mu      sync.Mutex
	tickers []*fakeTicker
}

func (f *fakeTickers) new(d time.Duration) ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTicker{period: d, ch: make(chan time.Time), stopped: make(chan struct{})}
	f.tickers = append(f.tickers, t)
	return t
}

func (f *fakeTickers) get(t *testing.T, i int) *fakeTicker {
	t.Helper()
	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.tickers) > i
	}, time.Second, time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tickers[i]
}

func requireStopped(t *testing.T, tk *fakeTicker) {
	t.Helper()
	select {
	case <-tk.stopped:
	case <-time.After(time.Second):
		t.Fatal("ticker was not stopped")
	}
}

type lockCall struct {
	path, branch string
	ttl          time.Duration
}

type recordingLockClient struct {
	mu      sync.Mutex
	calls   []lockCall
	lockErr error
	locks   []*domain.FileLock
	listErr error
}

func (r *recordingLockClient) Connect(context.Context, string) error { return nil }

func (r *recordingLockClient) LockFile(_ context.Context, _, _, path, branch string, ttl time.Duration) (*domain.FileLock, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, lockCall{path: path, branch: branch, ttl: ttl})
	if len(r.calls) > 1 && r.lockErr != nil {
		return nil, r.lockErr
	}
	expires := time.Now().Add(ttl)
	return &domain.FileLock{Path: path, Branch: branch, ExpiresAt: &expires}, nil
}

func (r *recordingLockClient) UnlockFile(context.Context, string, string, string, string) error {
	return nil
}

func (r *recordingLockClient) ListFileLocks(context.Context, string, string) ([]*domain.FileLock, error) {
	return r.locks, r.listErr
}

func (r *recordingLockClient) snapshot() []lockCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]lockCall(nil), r.calls...)
}

func connectServer(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func newKeepAliveServer(t *testing.T, client *recordingLockClient) (*server, *mcp.ClientSession, *fakeTickers) {
	t.Helper()
	tickers := &fakeTickers{}
	s, srv := newServer(Options{
		Allow:     AllCaps(),
		newTicker: tickers.new,
		NewUseCases: func() UseCases {
			return UseCases{Lock: usecase.NewFileLock(loggedInAuth(), client, localrepo.NewLocalRepo())}
		},
	})
	t.Cleanup(s.keepAlive.stop)
	return s, connectServer(t, srv), tickers
}

func TestLock_DefaultTTLRenewsUntilUnlock(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingLockClient{}
	s, session, tickers := newKeepAliveServer(t, client)

	res := call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "art/tex.png"})
	require.False(t, res.IsError, toolText(t, res))
	var lock output.Lock
	require.NoError(t, json.Unmarshal([]byte(toolText(t, res)), &lock))
	require.NotEmpty(t, lock.ExpiresAt)

	calls := client.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, lockCall{path: "art/tex.png", branch: "main", ttl: 10 * time.Minute}, calls[0])

	tk := tickers.get(t, 0)
	require.Equal(t, 10*time.Minute/3, tk.period)
	tk.ch <- time.Now()
	require.Eventually(t, func() bool { return len(client.snapshot()) == 2 }, time.Second, time.Millisecond)
	require.Equal(t, calls[0], client.snapshot()[1])

	res = call(t, session, "nipa_unlock", map[string]any{"repo": root, "path": "art/tex.png"})
	require.False(t, res.IsError, toolText(t, res))
	requireStopped(t, tk)
	require.Empty(t, s.keepAlive.keys())
}

func TestLock_CustomAndInvalidTTL(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingLockClient{}
	_, session, tickers := newKeepAliveServer(t, client)

	res := call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "a.png", "ttl": "1h", "branch": "feature"})
	require.False(t, res.IsError, toolText(t, res))
	require.Equal(t, time.Hour, client.snapshot()[0].ttl)
	require.Equal(t, "feature", client.snapshot()[0].branch)
	require.Equal(t, 20*time.Minute, tickers.get(t, 0).period)

	res = call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "a.png", "ttl": "soon"})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "invalid ttl")
}

func TestLock_RenewFailureStopsRenewing(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingLockClient{lockErr: errors.New("locked by bob")}
	s, session, tickers := newKeepAliveServer(t, client)

	res := call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "a.png"})
	require.False(t, res.IsError, toolText(t, res))
	tk := tickers.get(t, 0)
	tk.ch <- time.Now()
	requireStopped(t, tk)
	require.Eventually(t, func() bool { return len(s.keepAlive.keys()) == 0 }, time.Second, time.Millisecond)
}

func TestLock_RelockRestartsRenewal(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingLockClient{}
	s, session, tickers := newKeepAliveServer(t, client)

	call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "a.png"})
	call(t, session, "nipa_lock", map[string]any{"repo": root, "path": "a.png", "ttl": "5m"})
	requireStopped(t, tickers.get(t, 0))
	require.Equal(t, 5*time.Minute/3, tickers.get(t, 1).period)
	require.Len(t, s.keepAlive.keys(), 1)
}

func TestKeepAlive_StopEndsRenewal(t *testing.T) {
	tickers := &fakeTickers{}
	var renews int
	k := newKeepAlive(tickers.new, func(context.Context, keepAliveKey, time.Duration) error {
		renews++
		return nil
	})
	k.track(keepAliveKey{root: "/r", path: "a.png"}, time.Minute)
	k.track(keepAliveKey{root: "/r", path: "zero.png"}, 0)
	require.Len(t, k.keys(), 1)

	k.stop()
	requireStopped(t, tickers.get(t, 0))
	require.Empty(t, k.keys())
	require.Zero(t, renews)

	k.track(keepAliveKey{root: "/r", path: "b.png"}, time.Minute)
	require.Empty(t, k.keys(), "nothing is tracked after shutdown")
}

func TestKeepAlive_RetainDropsReleasedLocks(t *testing.T) {
	tickers := &fakeTickers{}
	k := newKeepAlive(tickers.new, func(context.Context, keepAliveKey, time.Duration) error { return nil })
	defer k.stop()
	k.track(keepAliveKey{root: "/r", path: "a.png"}, time.Minute)
	k.track(keepAliveKey{root: "/r", path: "art"}, time.Minute)
	k.track(keepAliveKey{root: "/other", path: "a.png"}, time.Minute)
	require.True(t, k.tracking("/r"))
	require.False(t, k.tracking("/none"))

	k.retain("/r", map[string]bool{"art": true})
	keys := k.keys()
	require.Len(t, keys, 2)
	require.ElementsMatch(t, []keepAliveKey{{root: "/r", path: "art"}, {root: "/other", path: "a.png"}}, keys)
	requireStopped(t, tickers.get(t, 0))
}

func TestForgetLanded(t *testing.T) {
	root := setupRepo(t, nil)
	client := &recordingLockClient{locks: []*domain.FileLock{{Path: "art"}}}
	s, _, _ := newKeepAliveServer(t, client)
	uc := UseCases{Lock: usecase.NewFileLock(loggedInAuth(), client, localrepo.NewLocalRepo())}

	s.forgetLanded(context.Background(), root, uc)

	s.keepAlive.track(keepAliveKey{root: root, path: "a.png"}, time.Minute)
	s.keepAlive.track(keepAliveKey{root: root, path: "art"}, time.Minute)

	client.listErr = errors.New("offline")
	s.forgetLanded(context.Background(), root, uc)
	require.Len(t, s.keepAlive.keys(), 2)

	client.listErr = nil
	s.forgetLanded(context.Background(), root, uc)
	require.Equal(t, []keepAliveKey{{root: root, path: "art"}}, s.keepAlive.keys())

	s.forgetLanded(context.Background(), root, UseCases{})
	require.Len(t, s.keepAlive.keys(), 1)
}

func TestRenewLock_NotConfigured(t *testing.T) {
	s, _ := newServer(Options{NewUseCases: func() UseCases { return UseCases{} }})
	defer s.keepAlive.stop()
	require.EqualError(t, s.renewLock(context.Background(), keepAliveKey{root: "/r", path: "a"}, time.Minute), "locks are not configured")

	s2, _ := newServer(Options{})
	defer s2.keepAlive.stop()
	require.Error(t, s2.renewLock(context.Background(), keepAliveKey{}, time.Minute))
}

func TestRealTicker(t *testing.T) {
	tk := newRealTicker(time.Millisecond)
	defer tk.Stop()
	select {
	case <-tk.C():
	case <-time.After(time.Second):
		t.Fatal("real ticker never fired")
	}
}
