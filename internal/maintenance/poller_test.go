package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/snow"
)

type fakeLoader struct {
	mu      sync.Mutex
	windows []Window
	err     error
	calls   int
}

func (f *fakeLoader) set(windows []Window, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windows, f.err = windows, err
}

func (f *fakeLoader) load(context.Context) ([]Window, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]Window(nil), f.windows...), nil
}

func (f *fakeLoader) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestPoller(t *testing.T, file string) (*Service, *fakeLoader, *Poller) {
	t.Helper()
	svc := NewService().WithClock(fixedClock(testNow))
	loader := &fakeLoader{}
	return svc, loader, NewPoller(svc, loader.load, file, time.Millisecond).WithClock(fixedClock(testNow))
}

func TestPollerLoadErrorKeepsSnapshot(t *testing.T) {
	ctx := context.Background()
	svc, loader, poller := newTestPoller(t, "")
	var hooked []error
	poller.WithErrorHook(func(err error) { hooked = append(hooked, err) })

	loader.set([]Window{serverWindow(1, ReadOnly, testNow), projectWindow(2, 10, "acme", "game", Full, testNow)}, nil)
	require.NoError(t, poller.Poll(ctx))
	require.Equal(t, testNow, svc.Snapshot().LoadedAt)

	loader.set(nil, errors.New("db down"))
	err := poller.Poll(ctx)
	require.ErrorContains(t, err, "db down")
	w, scope := svc.For("acme", "game")
	require.Equal(t, Full, w.Mode)
	require.Equal(t, ScopeProject, scope)
	w, _ = svc.For("", "")
	require.Equal(t, ReadOnly, w.Mode)

	poller.pollAndLog(ctx)
	require.Len(t, hooked, 1)
}

func TestPollerFileAppearsAndDisappears(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "maintenance")
	svc, loader, poller := newTestPoller(t, file)
	loader.set([]Window{serverWindow(1, ReadOnly, testNow), projectWindow(2, 10, "acme", "game", ReadOnly, testNow), orgWindow(3, 20, "acme", ReadOnly, testNow)}, nil)

	require.NoError(t, poller.Poll(ctx))
	require.Equal(t, SourceDB, svc.Snapshot().Source)

	require.NoError(t, os.WriteFile(file, nil, 0o600))
	require.NoError(t, poller.Poll(ctx))
	snap := svc.Snapshot()
	require.Equal(t, SourceFile, snap.Source)
	require.Equal(t, Full, snap.Server.Mode)
	require.Len(t, snap.ByID, 3, "stored windows stay indexed")
	require.Equal(t, ReadOnly, snap.Orgs["acme"].Mode)
	w, scope := svc.For("acme", "game")
	require.Equal(t, Full, w.Mode)
	require.Equal(t, ScopeServer, scope)

	require.NoError(t, os.WriteFile(file, []byte(`{"mode":"read_only","message":"from file"}`), 0o600))
	require.NoError(t, poller.Poll(ctx))
	require.Equal(t, "from file", svc.Snapshot().Server.Message)

	require.NoError(t, os.WriteFile(file, []byte(`{"mode":"nope"}`), 0o600))
	err := poller.Poll(ctx)
	require.Error(t, err)
	snap = svc.Snapshot()
	require.Equal(t, SourceFile, snap.Source)
	require.Equal(t, Full, snap.Server.Mode, "an unreadable file closes the server")
	require.Contains(t, snap.FileError, "nope")

	require.NoError(t, os.Remove(file))
	require.NoError(t, poller.Poll(ctx))
	snap = svc.Snapshot()
	require.Equal(t, SourceDB, snap.Source)
	require.Empty(t, snap.FileError)
	require.Equal(t, snow.ID(1), snap.Server.ID)
}

func TestPollerFileReadError(t *testing.T) {
	dir := t.TempDir()
	svc, _, poller := newTestPoller(t, dir)
	err := poller.Poll(context.Background())
	require.ErrorContains(t, err, "read maintenance file")
	require.Equal(t, SourceFile, svc.Snapshot().Source)
	require.Equal(t, Full, svc.Snapshot().Server.Mode)
}

func TestPollerSlugChange(t *testing.T) {
	ctx := context.Background()
	svc, loader, poller := newTestPoller(t, "")
	loader.set([]Window{projectWindow(2, 10, "acme", "game", Full, testNow)}, nil)
	require.NoError(t, poller.Poll(ctx))
	w, _ := svc.For("acme", "game")
	require.Equal(t, Full, w.Mode)

	loader.set([]Window{projectWindow(2, 10, "acme", "renamed", Full, testNow), orgWindow(3, 20, "acme2", ReadOnly, testNow)}, nil)
	require.NoError(t, poller.Poll(ctx))
	w, _ = svc.For("acme", "game")
	require.Equal(t, Off, w.Mode)
	w, scope := svc.For("acme", "renamed")
	require.Equal(t, Full, w.Mode)
	require.Equal(t, ScopeProject, scope)

	loader.set([]Window{orgWindow(3, 20, "acme-renamed", ReadOnly, testNow)}, nil)
	require.NoError(t, poller.Poll(ctx))
	w, _ = svc.For("acme", "")
	require.Equal(t, Off, w.Mode)
	w, scope = svc.For("acme-renamed", "")
	require.Equal(t, ReadOnly, w.Mode)
	require.Equal(t, ScopeOrg, scope)
}

func TestPollerResumeHook(t *testing.T) {
	ctx := context.Background()
	svc, loader, poller := newTestPoller(t, "")
	hookErr := errors.New("node id taken")
	svc.OnResume(func(context.Context) error { return hookErr })

	loader.set([]Window{serverWindow(1, Frozen, testNow)}, nil)
	require.NoError(t, poller.Poll(ctx))
	require.True(t, svc.Frozen())

	loader.set(nil, nil)
	require.ErrorIs(t, poller.Poll(ctx), hookErr)
	require.True(t, svc.Frozen(), "a failed hook keeps frozen")

	hookErr = nil
	require.NoError(t, poller.Poll(ctx))
	require.False(t, svc.Frozen())
}

func TestPollerStartStop(t *testing.T) {
	svc, loader, poller := newTestPoller(t, "")
	loader.set([]Window{serverWindow(1, ReadOnly, testNow)}, nil)
	poller.Start()
	require.Eventually(t, func() bool { return loader.count() >= 3 }, time.Second, time.Millisecond)
	require.NoError(t, poller.Stop(context.Background()))
	w, _ := svc.For("", "")
	require.Equal(t, ReadOnly, w.Mode)
	require.NoError(t, poller.Stop(context.Background()))
}

func TestPollerStopHonoursContext(t *testing.T) {
	svc := NewService()
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	load := func(context.Context) ([]Window, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil, nil
	}
	poller := NewPoller(svc, load, "", 0)
	require.Equal(t, DefaultPollInterval, poller.interval)
	poller.Start()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, poller.Stop(ctx), context.DeadlineExceeded)
	close(release)
}

func TestPollerStopWithoutStart(t *testing.T) {
	_, _, poller := newTestPoller(t, "")
	require.NoError(t, poller.Stop(context.Background()))
}
