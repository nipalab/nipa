package update

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"v1.2.3":         "1.2.3",
		"1.2.3":          "1.2.3",
		" v0.4.0-rc.1 ":  "0.4.0-rc.1",
		"v1.2":           "1.2.0",
		"":               "",
		"dev-abc123":     "",
		"v1.2.3-dirty":   "",
		"1.2.3.4":        "",
		"vfoo":           "",
		"v1.2.3+build.5": "1.2.3",
	} {
		require.Equal(t, want, Canonical(in), in)
	}
}

func TestOlder(t *testing.T) {
	require.True(t, Older("v0.1.0", "0.2.0"))
	require.True(t, Older("0.2.0-rc.1", "0.2.0"))
	require.False(t, Older("v0.2.0", "0.2.0"))
	require.False(t, Older("v0.3.0", "0.2.0"))
	require.False(t, Older("dev-abc", "0.2.0"))
	require.False(t, Older("v0.1.0", ""))
}

func TestFeedHonoursEnv(t *testing.T) {
	t.Setenv(EnvFeedURL, "")
	require.Equal(t, FeedURL, Feed())
	t.Setenv(EnvFeedURL, " https://updates.example/{{channel}}.json ")
	require.Equal(t, "https://updates.example/{{channel}}.json", Feed())
}

func TestKey(t *testing.T) {
	old := PublicKey
	t.Cleanup(func() { PublicKey = old })

	PublicKey = ""
	_, err := Key()
	require.ErrorContains(t, err, "no update signing key")

	PublicKey = "AAEC"
	key, err := Key()
	require.NoError(t, err)
	require.Equal(t, []byte{0, 1, 2}, key)

	PublicKey = "%%"
	_, err = Key()
	require.Error(t, err)
}

func TestTarget(t *testing.T) {
	exe := filepath.Join("/Applications", "Nipa.app", "Contents", "MacOS", "nipa-desktop")
	if runtime.GOOS == "darwin" {
		require.Equal(t, filepath.Join("/Applications", "Nipa.app"), Target(exe))
	} else {
		require.Equal(t, exe, Target(exe))
	}
	require.Equal(t, filepath.Join("/Applications", "Nipa.app"), bundleOf(exe))
	require.Equal(t, filepath.Clean("/opt/nipa/nipa-desktop"), bundleOf("/opt/nipa/nipa-desktop"))
}

func TestReplaceable(t *testing.T) {
	dir := t.TempDir()
	require.True(t, Replaceable(filepath.Join(dir, "nipa-desktop")))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "the probe file is removed")
	require.False(t, Replaceable(filepath.Join(dir, "missing", "nipa-desktop")))
}
