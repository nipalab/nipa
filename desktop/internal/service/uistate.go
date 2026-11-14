package service

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
