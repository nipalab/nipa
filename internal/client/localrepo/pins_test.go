package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPins_PinListUnpin(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.PinPaths([]string{"/art/hero.png", "levels\\", "", "."}))
	require.NoError(t, lr.PinPaths([]string{"art/hero.png"}))

	pins, err := lr.ListPins()
	require.NoError(t, err)
	require.Equal(t, []string{"art/hero.png", "levels"}, pins)

	require.NoError(t, lr.UnpinPaths([]string{"/art/hero.png", "missing"}))
	pins, err = lr.ListPins()
	require.NoError(t, err)
	require.Equal(t, []string{"levels"}, pins)

	require.NoError(t, lr.UnpinPaths(nil))
}

func TestPins_RejectsParentSegments(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.Error(t, lr.PinPaths([]string{"a/../b"}))
	require.Error(t, lr.UnpinPaths([]string{"../b"}))
}

func TestPins_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()
	require.Error(t, lr.PinPaths([]string{"a"}))
	require.Error(t, lr.UnpinPaths([]string{"a"}))
	_, err := lr.ListPins()
	require.Error(t, err)
}
