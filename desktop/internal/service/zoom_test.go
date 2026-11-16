package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetZoom(t *testing.T) {
	s := &Service{}
	require.NoError(t, s.SetZoom(1.25))

	var got []float64
	s.ZoomFunc = func(level float64) { got = append(got, level) }
	require.NoError(t, s.SetZoom(0.5))
	require.NoError(t, s.SetZoom(2))
	for _, bad := range []float64{0, 0.49, 2.01, math.NaN(), math.Inf(1)} {
		require.ErrorContains(t, s.SetZoom(bad), "zoom must be between")
	}
	require.Equal(t, []float64{0.5, 2}, got)
}

func TestStoredZoom(t *testing.T) {
	require.Equal(t, 1.0, StoredZoom(failingUI{}))
	require.Equal(t, 1.0, StoredZoom(languageUI{state: map[string]string{}}))
	require.Equal(t, 1.25, StoredZoom(languageUI{state: map[string]string{ZoomSettingKey: "1.25"}}))
	require.Equal(t, 1.0, StoredZoom(languageUI{state: map[string]string{ZoomSettingKey: "abc"}}))
	require.Equal(t, 1.0, StoredZoom(languageUI{state: map[string]string{ZoomSettingKey: "9"}}))
}
