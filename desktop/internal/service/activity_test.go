package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/desktop/internal/workspace"
	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type memPrefs struct {
	mu      sync.Mutex
	st      workspace.Settings
	err     error
	saveErr error
}

func newPrefs() *memPrefs { return &memPrefs{st: workspace.DefaultSettings()} }

func (m *memPrefs) Settings() (workspace.Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st, m.err
}

func (m *memPrefs) SaveSettings(st workspace.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.st = st
	return nil
}

type activityRecorder struct {
	mu      sync.Mutex
	events  []any
	notes   chan Notification
	lists   chan []RepoActivity
	emitted chan string
}

func newActivityRecorder() *activityRecorder {
	return &activityRecorder{notes: make(chan Notification, 16), lists: make(chan []RepoActivity, 16), emitted: make(chan string, 64)}
}

func (r *activityRecorder) attach(s *Service) {
	s.Events = func(name string, data any) {
		r.mu.Lock()
		r.events = append(r.events, data)
		r.mu.Unlock()
		r.emitted <- name
	}
	s.Notifier = func(n Notification) error {
		r.notes <- n
		return errors.New("ignored")
	}
	s.ActivityHook = func(list []RepoActivity) { r.lists <- list }
}

func waitFor[T any](t *testing.T, ch chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

func TestService_ActivityNotifiesAndFeedsTheTray(t *testing.T) {
	h := newHarness()
	h.daemon.events = make(chan *daemonpb.RepoActivity, 4)
	h.daemon.eventReqs = make(chan *daemonpb.WatchEventsRequest, 4)
	prefs := newPrefs()
	prefs.st.PollSeconds = 30
	prefs.st.Notify.Pushes = false
	h.svc.Prefs = prefs
	rec := newActivityRecorder()
	rec.attach(h.svc)

	_, err := h.svc.AddRepo(ctx, "/work/game")
	require.NoError(t, err)
	req := waitFor(t, h.daemon.eventReqs)
	require.Equal(t, "/work/game", req.GetRoot())
	require.EqualValues(t, 30, req.GetIntervalSeconds())

	h.daemon.events <- &daemonpb.RepoActivity{
		Incoming:       3,
		ReviewRequests: 1,
		Events: []*daemonpb.TeamEvent{
			{Kind: daemon.EventPushed, Actor: "Alice", Branch: "main", CommitId: "c9", Message: "Paint", Count: 3},
			{Kind: daemon.EventLockReleased, Actor: "Bob", Path: "hero.psd"},
			{Kind: daemon.EventReviewRequested, Number: 7, Message: "New hero"},
		},
	}
	list := waitFor(t, rec.lists)
	require.Equal(t, []RepoActivity{{Root: "/work/game", Name: "game", Incoming: 3, ReviewRequests: 1, Events: []TeamEvent{}}}, list)
	require.Equal(t, list, h.svc.Activity())

	lock := waitFor(t, rec.notes)
	require.Equal(t, "game: lock released", lock.Title)
	require.Equal(t, "Bob released hero.psd", lock.Body)
	require.Equal(t, Navigation{Root: "/work/game", Tab: "locks"}, lock.Target)
	review := waitFor(t, rec.notes)
	require.Equal(t, "game: review requested", review.Title)
	require.Equal(t, "!7 New hero", review.Body)
	require.Equal(t, Navigation{Root: "/work/game", Tab: "mrs", Number: 7}, review.Target)
	require.Empty(t, rec.notes, "pushes are switched off")

	rec.mu.Lock()
	var sent *RepoActivity
	for _, e := range rec.events {
		if a, ok := e.(RepoActivity); ok {
			sent = &a
		}
	}
	rec.mu.Unlock()
	require.NotNil(t, sent)
	require.Len(t, sent.Events, 3)
	require.Equal(t, TeamEvent{Kind: "pushed", Actor: "Alice", Branch: "main", CommitID: "c9", Message: "Paint", Count: 3}, sent.Events[0])

	_, err = h.svc.SaveSettings(workspace.Settings{PollSeconds: 120, KeepInTray: true})
	require.NoError(t, err)
	require.EqualValues(t, 120, waitFor(t, h.daemon.eventReqs).GetIntervalSeconds())

	require.NoError(t, h.svc.RemoveRepo(ctx, "/work/game"))
	require.Empty(t, waitFor(t, rec.lists))
	require.Empty(t, h.svc.Activity())
}

func TestService_ActivityWithoutNotifierOrEventsSupport(t *testing.T) {
	h := newHarness()
	h.daemon.events = make(chan *daemonpb.RepoActivity, 1)
	lists := make(chan []RepoActivity, 4)
	h.svc.ActivityHook = func(l []RepoActivity) { lists <- l }
	_, err := h.svc.AddRepo(ctx, "/work/game")
	require.NoError(t, err)
	h.daemon.events <- &daemonpb.RepoActivity{Error: "offline", Events: []*daemonpb.TeamEvent{{Kind: daemon.EventPushed}}}
	require.Equal(t, "offline", waitFor(t, lists)[0].Error)

	old := newHarness()
	_, err = old.svc.AddRepo(ctx, "/work/game")
	require.NoError(t, err)
	require.Empty(t, old.svc.Activity())
}

func TestService_RestartActivityWithoutSession(t *testing.T) {
	h := newHarness()
	h.svc.restartActivity()
	require.Zero(t, h.dials)
}

func TestService_ActivityIgnoredAfterCancel(t *testing.T) {
	h := newHarness()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	h.svc.recordActivity(cancelled, "/work/game", &daemonpb.RepoActivity{Incoming: 1})
	require.Empty(t, h.svc.Activity())
}

func TestNotificationFor(t *testing.T) {
	n, ok := notificationFor("game", "/r", TeamEvent{Kind: daemon.EventPushed, Actor: "A", Message: "m", Branch: "main", Count: 1, CommitID: "c"}, true, false, false)
	require.True(t, ok)
	require.Equal(t, "game: new commit on main", n.Title)
	require.Equal(t, "A: m", n.Body)
	require.Equal(t, "push:/r:c", n.ID)
	require.Equal(t, Navigation{Root: "/r", Tab: "history"}, n.Target)

	n, _ = notificationFor("game", "/r", TeamEvent{Kind: daemon.EventPushed, Branch: "main", Count: 4}, true, false, false)
	require.Equal(t, "game: 4 new commits on main", n.Title)

	for _, kind := range []string{daemon.EventLockReleased, daemon.EventReviewRequested, "other"} {
		_, ok := notificationFor("game", "/r", TeamEvent{Kind: kind}, true, false, false)
		require.False(t, ok, kind)
	}
}

func TestNotificationData(t *testing.T) {
	n := Notification{Target: Navigation{Root: "/r", Tab: "mrs", Number: 4}}
	require.Equal(t, Navigation{Root: "/r", Tab: "mrs", Number: 4}, NavigationFromData(n.Data()))
	require.Equal(t, Navigation{Root: "/r", Tab: "mrs", Number: 5}, NavigationFromData(map[string]any{"root": "/r", "tab": "mrs", "number": 5.0}))
	require.Equal(t, Navigation{Tab: "changes", Number: 6}, NavigationFromData(map[string]any{"number": 6}))
	require.Equal(t, Navigation{Tab: "changes"}, NavigationFromData(nil))
}

func TestService_ActivityIgnoredForUnfollowedRoot(t *testing.T) {
	h := newHarness()
	h.svc.recordActivity(ctx, "/work/removed", &daemonpb.RepoActivity{Incoming: 1})
	require.Empty(t, h.svc.Activity())
}
