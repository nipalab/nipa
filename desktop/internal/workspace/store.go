// Package workspace persists the clones the desktop app shows. The daemon only
// keeps watched roots in memory, so the app re-watches these at startup.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

type state struct {
	Repos    []string          `json:"repos"`
	Settings *Settings         `json:"settings,omitempty"`
	Window   *Window           `json:"window,omitempty"`
	UI       map[string]string `json:"ui,omitempty"`
	// LockWatches are the lock paths each clone waits on, by clone root.
	LockWatches map[string][]string `json:"lockWatches,omitempty"`
}

// Settings are the app preferences kept next to the clone list.
type Settings struct {
	// KeepInTray hides the window on close; the app keeps watching from the
	// tray until quit from its menu.
	KeepInTray   bool           `json:"keepInTray"`
	StartAtLogin bool           `json:"startAtLogin"`
	PollSeconds  int            `json:"pollSeconds"`
	Notify       NotifySettings `json:"notify"`
	Tools        []Tool         `json:"tools"`
	// ManualUpdates turns off the background update check and download.
	ManualUpdates bool `json:"manualUpdates"`
}

type NotifySettings struct {
	Pushes  bool `json:"pushes"`
	Locks   bool `json:"locks"`
	Reviews bool `json:"reviews"`
	// LockRequests tells the holder that someone asked for a lock; on unless
	// a saved file says otherwise.
	LockRequests bool `json:"lockRequests"`
}

func (n *NotifySettings) UnmarshalJSON(data []byte) error {
	type plain NotifySettings
	in := plain{LockRequests: true}
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	*n = NotifySettings(in)
	return nil
}

// Tool opens files with one extension (".blend") in a program instead of the
// system default.
type Tool struct {
	Extension string `json:"extension"`
	Command   string `json:"command"`
}

const (
	DefaultPollSeconds = 60
	MinPollSeconds     = 15
	MaxPollSeconds     = 3600
)

func DefaultSettings() Settings {
	return Settings{
		KeepInTray:  true,
		PollSeconds: DefaultPollSeconds,
		Notify:      NotifySettings{Pushes: true, Locks: true, Reviews: true, LockRequests: true},
		Tools:       []Tool{},
	}
}

type Store struct {
	path string
	mu   sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

// DefaultPath is ~/.config/nipa/desktop.json (platform config dir).
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nipa", "desktop.json"), nil
}

func (s *Store) Repos() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	return st.Repos, nil
}

func (s *Store) Add(root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	if slices.Contains(st.Repos, root) {
		return nil
	}
	st.Repos = append(st.Repos, root)
	return s.save(st)
}

func (s *Store) Remove(root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(st.Repos, func(r string) bool { return r == root })
	_, watched := st.LockWatches[root]
	if len(kept) == len(st.Repos) && !watched {
		return nil
	}
	st.Repos = kept
	delete(st.LockWatches, root)
	return s.save(st)
}

// LockWatches returns the lock paths waited on for root.
func (s *Store) LockWatches(root string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	return append([]string{}, st.LockWatches[root]...), nil
}

// SetLockWatch adds or removes one watched lock path for root.
func (s *Store) SetLockWatch(root, path string, watch bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	paths := st.LockWatches[root]
	has := slices.Contains(paths, path)
	if has == watch {
		return nil
	}
	if watch {
		paths = append(paths, path)
	} else {
		paths = slices.DeleteFunc(paths, func(p string) bool { return p == path })
	}
	if st.LockWatches == nil {
		st.LockWatches = map[string][]string{}
	}
	if len(paths) == 0 {
		delete(st.LockWatches, root)
	} else {
		st.LockWatches[root] = paths
	}
	return s.save(st)
}

func (s *Store) Settings() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Settings{}, err
	}
	if st.Settings == nil {
		return DefaultSettings(), nil
	}
	out := *st.Settings
	if out.Tools == nil {
		out.Tools = []Tool{}
	}
	return out, nil
}

func (s *Store) SaveSettings(settings Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	st.Settings = &settings
	return s.save(st)
}

func (s *Store) UI() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	if st.UI == nil {
		return map[string]string{}, nil
	}
	return st.UI, nil
}

func (s *Store) SaveUI(ui map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	st.UI = ui
	return s.save(st)
}

func (s *Store) Window() (Window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil || st.Window == nil {
		return Window{}, false
	}
	return *st.Window, true
}

func (s *Store) SaveWindow(w Window) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return err
	}
	st.Window = &w
	return s.save(st)
}

func (s *Store) load() (state, error) {
	var st state
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return state{Repos: []string{}}, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if st.Repos == nil {
		st.Repos = []string{}
	}
	return st, nil
}

func (s *Store) save(st state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".desktop-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}
