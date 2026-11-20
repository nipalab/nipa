package fsperm

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMask(t *testing.T) {
	for _, tc := range []struct{ mode, umask, want os.FileMode }{
		{0o644, 0o022, 0o644},
		{0o644, 0o077, 0o600},
		{0o755, 0o077, 0o700},
		{0o444, 0o027, 0o440},
		{0o755, 0o002, 0o755},
		{0o644, 0o277, 0o600},
		{0o644, 0, 0o644},
	} {
		require.Equal(t, tc.want, mask(tc.mode, tc.umask), "%o with umask %o", tc.mode, tc.umask)
	}
	require.Equal(t, mask(0o644, umask), Mask(0o644))
}

func TestReadUmask_MatchesWhatTheKernelApplies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no umask")
	}
	f, err := os.OpenFile(t.TempDir()+"/probe", os.O_CREATE|os.O_WRONLY, 0o666)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o666)&^umask, info.Mode().Perm())
	require.Equal(t, umask, readUmask(), "reading the umask leaves it unchanged")
}
