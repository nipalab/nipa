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
	Repos []string `json:"repos"`
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
	if len(kept) == len(st.Repos) {
		return nil
	}
	st.Repos = kept
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
