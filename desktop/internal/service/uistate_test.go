package service

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/desktop/internal/workspace"
)

type failingUI struct{}

func (failingUI) UI() (map[string]string, error) { return nil, errors.New("boom") }
func (failingUI) SaveUI(map[string]string) error { return errors.New("boom") }

func TestUIState(t *testing.T) {
	s := &Service{}
	got, err := s.UIState()
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, s.SaveUIState(map[string]string{"a": "1"}))

	s.Layout = workspace.NewStore(filepath.Join(t.TempDir(), "desktop.json"))
	require.NoError(t, s.SaveUIState(map[string]string{"nipa.selectedRepo": "/work/a"}))
	got, err = s.UIState()
	require.NoError(t, err)
	require.Equal(t, map[string]string{"nipa.selectedRepo": "/work/a"}, got)

	require.ErrorContains(t, s.SaveUIState(map[string]string{"big": strings.Repeat("x", maxUIValueBytes)}), "too large")
	many := map[string]string{}
	for i := range maxUIEntries + 1 {
		many[strconv.Itoa(i)] = ""
	}
	require.ErrorContains(t, s.SaveUIState(many), "too many")

	s.Layout = failingUI{}
	_, err = s.UIState()
	require.Error(t, err)
	require.Error(t, s.SaveUIState(map[string]string{}))
}
