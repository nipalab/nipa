package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/desktop/internal/workspace"
)

type fakeUpdater struct {
	mu          sync.Mutex
	release     *Release
	checkErr    error
	downloadErr error
	restartErr  error
	checks      int
	downloads   int
	restarts    int
}

func (f *fakeUpdater) Check(context.Context) (*Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.release, f.checkErr
}

func (f *fakeUpdater) Download(_ context.Context, progress func(int64, int64)) error {
	f.mu.Lock()
	f.downloads++
	err := f.downloadErr
	f.mu.Unlock()
	progress(5, 10)
	progress(10, 10)
	return err
}

func (f *fakeUpdater) Restart(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return f.restartErr
}

type updateHarness struct {
	svc    *Service
	upd    *fakeUpdater
	mu     sync.Mutex
	events []UpdateState
	notes  []Notification
	opened []string
	prefs  *memPrefs
}

func newUpdateHarness(inPlace bool) *updateHarness {
	h := &updateHarness{svc: New(nil, nil, nil), upd: &fakeUpdater{}, prefs: newPrefs()}
	h.svc.Prefs = h.prefs
	h.svc.Events = func(name string, data any) {
		if name != EventUpdate {
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.events = append(h.events, data.(UpdateState))
	}
	h.svc.Notifier = func(n Notification) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.notes = append(h.notes, n)
		return nil
	}
	ConfigureUpdates(h.svc, Updates{
		Current: "0.1.0",
		Updater: h.upd,
		InPlace: inPlace,
		OpenURL: func(u string) error { h.opened = append(h.opened, u); return nil },
	})
	return h
}

func (h *updateHarness) phases() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, e := range h.events {
		if len(out) == 0 || out[len(out)-1] != e.Phase {
			out = append(out, e.Phase)
		}
	}
	return out
}

func (h *updateHarness) notifications() []Notification {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Notification(nil), h.notes...)
}

var release = &Release{Version: "0.2.0", Notes: "notes", Page: "https://example/releases/v0.2.0"}

func TestUpdates_DisabledWithoutUpdater(t *testing.T) {
	svc := New(nil, nil, nil)
	st := svc.UpdateStatus()
	require.False(t, st.Enabled)
	require.Equal(t, UpdateIdle, st.Phase)

	ConfigureUpdates(svc, Updates{Current: ""})
	_, err := svc.CheckForUpdate(context.Background())
	require.ErrorIs(t, err, errUpdatesDisabled)
	_, err = svc.InstallUpdate(context.Background())
	require.ErrorIs(t, err, errUpdatesDisabled)
	require.ErrorIs(t, svc.RestartToUpdate(context.Background()), errUpdatesDisabled)
	require.Error(t, svc.OpenUpdatePage())
}

func TestUpdates_CheckInstallRestart(t *testing.T) {
	h := newUpdateHarness(true)
	ctx := context.Background()

	st := h.svc.UpdateStatus()
	require.True(t, st.Enabled)
	require.True(t, st.InPlace)
	require.Equal(t, "0.1.0", st.Current)

	st, err := h.svc.CheckForUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, UpdateUpToDate, st.Phase)
	require.Nil(t, st.Release)
	require.NotEmpty(t, st.CheckedAt)

	_, err = h.svc.InstallUpdate(ctx)
	require.ErrorContains(t, err, "no update is available")
	require.ErrorContains(t, h.svc.RestartToUpdate(ctx), "not been downloaded")

	h.upd.release = release
	st, err = h.svc.CheckForUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, UpdateAvailable, st.Phase)
	require.Equal(t, release, st.Release)

	st, err = h.svc.InstallUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, UpdateReady, st.Phase)
	require.Equal(t, int64(10), st.Written)
	require.Equal(t, int64(10), st.Total)

	st, err = h.svc.InstallUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, UpdateReady, st.Phase)
	require.Equal(t, 1, h.upd.downloads, "a ready update is not downloaded again")

	st, err = h.svc.CheckForUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, UpdateReady, st.Phase, "checking keeps the downloaded update")
	require.Equal(t, 2, h.upd.checks)

	require.NoError(t, h.svc.RestartToUpdate(ctx))
	require.Equal(t, 1, h.upd.restarts)
	require.Equal(t, []string{UpdateChecking, UpdateUpToDate, UpdateChecking, UpdateAvailable, UpdateDownloading, UpdateReady}, h.phases())
}

func TestUpdates_Failures(t *testing.T) {
	h := newUpdateHarness(true)
	ctx := context.Background()

	h.upd.checkErr = errors.New("feed offline")
	st, err := h.svc.CheckForUpdate(ctx)
	require.ErrorContains(t, err, "feed offline")
	require.Equal(t, UpdateFailed, st.Phase)
	require.Equal(t, "feed offline", st.Error)

	h.upd.checkErr = nil
	h.upd.release = release
	h.upd.downloadErr = errors.New("signature did not verify")
	st, err = h.svc.CheckForUpdate(ctx)
	require.NoError(t, err)
	require.Empty(t, st.Error, "a new check clears the old error")
	st, err = h.svc.InstallUpdate(ctx)
	require.ErrorContains(t, err, "signature")
	require.Equal(t, UpdateFailed, st.Phase)
	require.Equal(t, release, st.Release, "the release stays so the user can retry")

	h.upd.downloadErr = nil
	st, err = h.svc.InstallUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, UpdateReady, st.Phase)

	h.upd.restartErr = errors.New("helper did not start")
	require.ErrorContains(t, h.svc.RestartToUpdate(ctx), "helper")
}

func TestUpdates_NotInPlaceOpensReleasePage(t *testing.T) {
	h := newUpdateHarness(false)
	ctx := context.Background()
	h.upd.release = release
	_, err := h.svc.CheckForUpdate(ctx)
	require.NoError(t, err)

	st, err := h.svc.InstallUpdate(ctx)
	require.NoError(t, err)
	require.Equal(t, UpdateAvailable, st.Phase)
	require.Equal(t, 0, h.upd.downloads)
	require.Equal(t, []string{release.Page}, h.opened)

	require.NoError(t, h.svc.OpenUpdatePage())
	require.Len(t, h.opened, 2)

	ConfigureUpdates(h.svc, Updates{Current: "0.1.0", Updater: h.upd})
	require.ErrorContains(t, h.svc.OpenUpdatePage(), "not available")

	h.upd.release = &Release{Version: "0.3.0"}
	_, err = h.svc.CheckForUpdate(ctx)
	require.NoError(t, err)
	require.ErrorContains(t, h.svc.OpenUpdatePage(), "no release page")
}

func TestUpdates_AutoUpdateDownloadsAndNotifiesOnce(t *testing.T) {
	h := newUpdateHarness(true)
	h.upd.release = release
	h.svc.autoUpdate(context.Background())
	h.svc.autoUpdate(context.Background())

	require.Equal(t, UpdateReady, h.svc.UpdateStatus().Phase)
	require.Equal(t, 1, h.upd.downloads)
	notes := h.notifications()
	require.Len(t, notes, 1)
	require.Equal(t, "Nipa 0.2.0 is ready", notes[0].Title)
	require.Equal(t, "settings", notes[0].Target.Tab)
}

func TestUpdates_AutoUpdateNotInPlaceOnlyNotifies(t *testing.T) {
	h := newUpdateHarness(false)
	h.upd.release = release
	h.svc.autoUpdate(context.Background())
	require.Equal(t, 0, h.upd.downloads)
	notes := h.notifications()
	require.Len(t, notes, 1)
	require.Equal(t, "Nipa 0.2.0 is available", notes[0].Title)
	require.Empty(t, h.opened, "the page is never opened without the user")
}

func TestUpdates_AutoUpdateQuietOnFailures(t *testing.T) {
	h := newUpdateHarness(true)
	h.svc.autoUpdate(context.Background())
	h.upd.checkErr = errors.New("offline")
	h.svc.autoUpdate(context.Background())
	h.upd.checkErr = nil
	h.upd.release = release
	h.upd.downloadErr = errors.New("disk full")
	h.svc.autoUpdate(context.Background())
	require.Empty(t, h.notifications())

	h.svc.Notifier = nil
	h.upd.downloadErr = nil
	h.svc.autoUpdate(context.Background())
	require.Equal(t, UpdateReady, h.svc.UpdateStatus().Phase)
}

func TestWatchUpdates_HonoursManualUpdates(t *testing.T) {
	h := newUpdateHarness(true)
	h.upd.release = release
	manual := workspace.DefaultSettings()
	manual.ManualUpdates = true
	require.NoError(t, h.prefs.SaveSettings(manual))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchUpdates(ctx, h.svc, time.Millisecond, time.Millisecond)
	}()
	time.Sleep(30 * time.Millisecond)
	h.upd.mu.Lock()
	require.Zero(t, h.upd.checks)
	h.upd.mu.Unlock()

	require.NoError(t, h.prefs.SaveSettings(workspace.DefaultSettings()))
	require.Eventually(t, func() bool { return h.svc.UpdateStatus().Phase == UpdateReady }, 2*time.Second, 5*time.Millisecond)
	cancel()
	<-done
}
