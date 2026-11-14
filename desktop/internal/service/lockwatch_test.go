package service

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type memWatches struct {
	paths map[string][]string
	err   error
}

func (m *memWatches) LockWatches(root string) ([]string, error) {
	return slices.Clone(m.paths[root]), m.err
}

func (m *memWatches) SetLockWatch(root, path string, watch bool) error {
	if m.err != nil {
		return m.err
	}
	if m.paths == nil {
		m.paths = map[string][]string{}
	}
	m.paths[root] = slices.DeleteFunc(m.paths[root], func(p string) bool { return p == path })
	if watch {
		m.paths[root] = append(m.paths[root], path)
	}
	return nil
}

func TestService_LockWatchRoundTrip(t *testing.T) {
	s := &Service{}
	got, err := s.LockWatches(ctx, "/w")
	require.NoError(t, err)
	require.Empty(t, got)
	require.Error(t, s.WatchLock(ctx, "/w", "a.psd"))

	s.Watches = &memWatches{}
	require.NoError(t, s.WatchLock(ctx, "/w", "/art/a.psd/"))
	got, err = s.LockWatches(ctx, "/w")
	require.NoError(t, err)
	require.Equal(t, []string{"art/a.psd"}, got)
	require.Error(t, s.WatchLock(ctx, "/w", "/"))
	require.NoError(t, s.UnwatchLock(ctx, "/w", "art/a.psd"))
	got, _ = s.LockWatches(ctx, "/w")
	require.Empty(t, got)
}

func TestService_WatchedLockNotifications(t *testing.T) {
	w := &memWatches{paths: map[string][]string{"/w": {"art/a.psd", "art/b.psd", "audio/x.wav"}}}
	s := &Service{Watches: w}

	require.Nil(t, s.watchedLockNotifications("g", "/w", TeamEvent{Kind: "pushed", Path: "art"}))
	got := s.watchedLockNotifications("g", "/w", TeamEvent{Kind: daemon.EventLockReleased, Actor: "Bob", Path: "art"})
	require.Len(t, got, 2)
	require.Equal(t, "g: art/a.psd is free", got[0].Title)
	require.Equal(t, Navigation{Root: "/w", Tab: "locks", Path: "art/a.psd", Action: "lock"}, got[0].Target)
	require.Equal(t, []string{"audio/x.wav"}, w.paths["/w"])
	require.Nil(t, s.watchedLockNotifications("g", "/w", TeamEvent{Kind: daemon.EventLockReleased, Path: "art"}))
	require.Len(t, s.watchedLockNotifications("g", "/w", TeamEvent{Kind: daemon.EventLockReleased, Path: "audio/x.wav"}), 1)

	w.err = errors.New("disk")
	require.Nil(t, s.watchedLockNotifications("g", "/w", TeamEvent{Kind: daemon.EventLockReleased, Path: "x"}))
	w.err = nil
	w.paths["/w"] = []string{"x"}
	failing := &failingSet{memWatches: w}
	s.Watches = failing
	require.Empty(t, s.watchedLockNotifications("g", "/w", TeamEvent{Kind: daemon.EventLockReleased, Path: "x"}))
	require.Nil(t, (&Service{}).watchedLockNotifications("g", "/w", TeamEvent{Kind: daemon.EventLockReleased, Path: "x"}))
}

type failingSet struct{ *memWatches }

func (f *failingSet) SetLockWatch(string, string, bool) error { return errors.New("write") }

func TestService_ActivityNotifiesWatchedLock(t *testing.T) {
	h := newHarness()
	h.daemon.events = make(chan *daemonpb.RepoActivity, 4)
	h.daemon.eventReqs = make(chan *daemonpb.WatchEventsRequest, 4)
	h.svc.Watches = &memWatches{paths: map[string][]string{"/work/game": {"hero.psd"}}}
	rec := newActivityRecorder()
	rec.attach(h.svc)
	_, err := h.svc.AddRepo(ctx, "/work/game")
	require.NoError(t, err)
	waitFor(t, h.daemon.eventReqs)

	h.daemon.events <- &daemonpb.RepoActivity{Events: []*daemonpb.TeamEvent{{Kind: daemon.EventLockReleased, Actor: "Bob", Path: "hero.psd"}}}
	require.Equal(t, "game: hero.psd is free", waitFor(t, rec.notes).Title)
	require.Empty(t, rec.notes, "the generic release notice is replaced")
}

func TestNavigationFromDataCarriesLockAction(t *testing.T) {
	n := Notification{Target: Navigation{Root: "/w", Tab: "locks", Path: "a", Action: "lock"}}
	require.Equal(t, n.Target, NavigationFromData(n.Data()))
}
