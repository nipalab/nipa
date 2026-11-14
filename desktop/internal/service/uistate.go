package service

import (
	"os"

	"github.com/nipalab/nipa/desktop/internal/lang"
)

import "fmt"

type UIStore interface {
	UI() (map[string]string, error)
	SaveUI(map[string]string) error
}

const (
	maxUIEntries    = 5000
	maxUIValueBytes = 64 << 10
)

// UIState returns the stored interface state.
func (s *Service) UIState() (map[string]string, error) {
	if s.Layout == nil {
		return map[string]string{}, nil
	}
	return s.Layout.UI()
}

// SaveUIState replaces the stored interface state.
func (s *Service) SaveUIState(state map[string]string) error {
	if s.Layout == nil {
		return nil
	}
	if len(state) > maxUIEntries {
		return fmt.Errorf("too many interface settings (%d, max %d)", len(state), maxUIEntries)
	}
	for k, v := range state {
		if len(k)+len(v) > maxUIValueBytes {
			return fmt.Errorf("interface setting %q is too large", k)
		}
	}
	return s.Layout.SaveUI(state)
}

// language is the language of the strings the Go side shows: the choice kept
// in the interface state, else the operating system's.
func (s *Service) language() lang.Language {
	setting := ""
	if s.Layout != nil {
		if state, err := s.Layout.UI(); err == nil {
			setting = state[lang.SettingKey]
		}
	}
	return lang.Resolve(setting, os.Getenv)
}

// LanguageOf is language for callers outside the package; it is not a method
// so the Wails bindings do not expose it.
func LanguageOf(s *Service) lang.Language { return s.language() }
