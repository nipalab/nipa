package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

const EventUpdate = "update:state"

const (
	UpdateIdle        = "idle"
	UpdateChecking    = "checking"
	UpdateUpToDate    = "upToDate"
	UpdateAvailable   = "available"
	UpdateDownloading = "downloading"
	UpdateReady       = "ready"
	UpdateFailed      = "error"
)

// Updater checks the release feed, downloads and verifies the new build and
// restarts into it (the Wails updater in main.go).
type Updater interface {
	// Check returns nil when the app is up to date.
	Check(ctx context.Context) (*Release, error)
	Download(ctx context.Context, progress func(written, total int64)) error
	Restart(ctx context.Context) error
}

type Release struct {
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"publishedAt"`
	// Page is the release page, opened when the app cannot update itself.
	Page string `json:"page"`
}

type UpdateState struct {
	Current string `json:"current"`
	// Enabled is false for development builds and builds without a feed key.
	Enabled bool `json:"enabled"`
	// InPlace is false for package-managed installs, which download the
	// release instead of replacing themselves.
	InPlace   bool     `json:"inPlace"`
	Phase     string   `json:"phase"`
	Release   *Release `json:"release,omitempty"`
	Written   int64    `json:"written"`
	Total     int64    `json:"total"`
	Error     string   `json:"error,omitempty"`
	CheckedAt string   `json:"checkedAt,omitempty"`
}

// Updates configures in-app updates; a nil Updater disables them.
type Updates struct {
	Current string
	Updater Updater
	InPlace bool
	OpenURL func(string) error
}

type updates struct {
	cfg      Updates
	op       sync.Mutex
	mu       sync.Mutex
	state    UpdateState
	notified string
}

var errUpdatesDisabled = errors.New("updates are not available in this build")

func (u *updates) config() Updates {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.cfg
}

func (s *Service) updater() *updates {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upd == nil {
		s.upd = &updates{}
		s.upd.state = UpdateState{Phase: UpdateIdle}
	}
	return s.upd
}

// ConfigureUpdates is called once from main before the app runs.
func ConfigureUpdates(s *Service, cfg Updates) {
	u := s.updater()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.cfg = cfg
	u.state.Current = cfg.Current
	u.state.Enabled = cfg.Updater != nil
	u.state.InPlace = cfg.InPlace
}

// UpdateStatus returns the current update state.
func (s *Service) UpdateStatus() UpdateState {
	u := s.updater()
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

func (s *Service) setUpdate(change func(*UpdateState)) UpdateState {
	u := s.updater()
	u.mu.Lock()
	change(&u.state)
	st := u.state
	u.mu.Unlock()
	s.emit(EventUpdate, st)
	return st
}

// CheckForUpdate asks the release feed for a newer version.
func (s *Service) CheckForUpdate(ctx context.Context) (UpdateState, error) {
	u := s.updater()
	cfg := u.config()
	if cfg.Updater == nil {
		return s.UpdateStatus(), errUpdatesDisabled
	}
	u.op.Lock()
	defer u.op.Unlock()
	if st := s.UpdateStatus(); st.Phase == UpdateReady {
		return st, nil
	}
	s.setUpdate(func(st *UpdateState) { st.Phase, st.Error = UpdateChecking, "" })
	rel, err := cfg.Updater.Check(ctx)
	now := time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		st := s.setUpdate(func(st *UpdateState) {
			st.Phase, st.Error, st.CheckedAt = UpdateFailed, err.Error(), now
		})
		return st, err
	}
	return s.setUpdate(func(st *UpdateState) {
		st.CheckedAt, st.Release, st.Written, st.Total = now, rel, 0, 0
		st.Phase = UpdateUpToDate
		if rel != nil {
			st.Phase = UpdateAvailable
		}
	}), nil
}

// InstallUpdate downloads and verifies the available release; the new
// version runs after RestartToUpdate. Installs that cannot replace
// themselves open the release page instead.
func (s *Service) InstallUpdate(ctx context.Context) (UpdateState, error) {
	u := s.updater()
	cfg := u.config()
	if cfg.Updater == nil {
		return s.UpdateStatus(), errUpdatesDisabled
	}
	u.op.Lock()
	defer u.op.Unlock()
	st := s.UpdateStatus()
	if st.Release == nil {
		return st, errors.New("no update is available")
	}
	if st.Phase == UpdateReady {
		return st, nil
	}
	if !cfg.InPlace {
		return st, s.OpenUpdatePage()
	}
	s.setUpdate(func(st *UpdateState) { st.Phase, st.Error, st.Written, st.Total = UpdateDownloading, "", 0, 0 })
	err := cfg.Updater.Download(ctx, func(written, total int64) {
		s.setUpdate(func(st *UpdateState) { st.Written, st.Total = written, total })
	})
	if err != nil {
		return s.setUpdate(func(st *UpdateState) { st.Phase, st.Error = UpdateFailed, err.Error() }), err
	}
	return s.setUpdate(func(st *UpdateState) { st.Phase = UpdateReady }), nil
}

// RestartToUpdate quits and relaunches the app on the downloaded version.
func (s *Service) RestartToUpdate(ctx context.Context) error {
	u := s.updater()
	cfg := u.config()
	if cfg.Updater == nil {
		return errUpdatesDisabled
	}
	if s.UpdateStatus().Phase != UpdateReady {
		return errors.New("the update has not been downloaded yet")
	}
	return cfg.Updater.Restart(ctx)
}

// OpenUpdatePage opens the available release's page in the browser.
func (s *Service) OpenUpdatePage() error {
	cfg := s.updater().config()
	st := s.UpdateStatus()
	if st.Release == nil || st.Release.Page == "" {
		return errors.New("no release page to open")
	}
	if cfg.OpenURL == nil {
		return errors.New("opening links is not available")
	}
	return cfg.OpenURL(st.Release.Page)
}

// WatchUpdates checks the feed after first and then every interval while
// automatic updates are on, downloads what it finds when the app can
// replace itself and notifies once per version.
func WatchUpdates(ctx context.Context, s *Service, first, every time.Duration) {
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if !s.prefs().ManualUpdates {
			s.autoUpdate(ctx)
		}
		timer.Reset(every)
	}
}

func (s *Service) autoUpdate(ctx context.Context) {
	st, err := s.CheckForUpdate(ctx)
	if err != nil || st.Release == nil {
		return
	}
	if st.InPlace && st.Phase == UpdateAvailable {
		if st, err = s.InstallUpdate(ctx); err != nil {
			return
		}
	}
	u := s.updater()
	u.mu.Lock()
	seen := u.notified == st.Release.Version
	u.notified = st.Release.Version
	u.mu.Unlock()
	if seen || s.Notifier == nil {
		return
	}
	n := Notification{
		ID:     "update-" + st.Release.Version,
		Title:  "Nipa " + st.Release.Version + " is available",
		Body:   "Download it from Settings.",
		Target: Navigation{Tab: "settings"},
	}
	if st.Phase == UpdateReady {
		n.Title = "Nipa " + st.Release.Version + " is ready"
		n.Body = "Restart Nipa to finish updating."
	}
	_ = s.Notifier(n)
}
