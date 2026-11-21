package localrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func markerCount(t *testing.T, lr *LocalRepo) int {
	t.Helper()
	entries, err := os.ReadDir(lr.writesDir())
	require.NoError(t, err)
	return len(entries)
}

// leaveDeadMarker plants what a killed process leaves: a marker nobody locks.
func leaveDeadMarker(t *testing.T, lr *LocalRepo, name string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(lr.writesDir(), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lr.writesDir(), name), nil, 0o644))
}

func TestBeginWorkingWrite_CleanRunsLeaveNothing(t *testing.T) {
	lr := newTestLocalRepo(t)

	end, interrupted, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.False(t, interrupted)
	require.Equal(t, 1, markerCount(t, lr))
	end()
	end()
	require.Zero(t, markerCount(t, lr), "a finished write removes its marker")

	end, interrupted, err = lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.False(t, interrupted, "a write that ended is not an interrupted one")
	end()
}

func TestBeginWorkingWrite_ReportsAKilledWrite(t *testing.T) {
	lr := newTestLocalRepo(t)
	leaveDeadMarker(t, lr, "w-dead")

	end, interrupted, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.True(t, interrupted)
	require.Equal(t, 1, markerCount(t, lr), "the dead marker is cleared, ours remains")
	end()

	end, interrupted, err = lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.False(t, interrupted, "it is reported once")
	end()
}

func TestBeginWorkingWrite_WaitsForRunningWrites(t *testing.T) {
	lr := newTestLocalRepo(t)
	running, _, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	leaveDeadMarker(t, lr, "w-dead")

	end, interrupted, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.False(t, interrupted, "no sweep while another write may own temp files")
	end()
	running()

	end, interrupted, err = lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.True(t, interrupted, "the dead marker was kept for a quiet moment")
	end()
}

func TestMarkInterruptedWrite(t *testing.T) {
	lr := newTestLocalRepo(t)
	require.NoError(t, lr.MarkInterruptedWrite())
	require.NoError(t, lr.MarkInterruptedWrite())

	end, interrupted, err := lr.BeginWorkingWrite()
	require.NoError(t, err)
	require.True(t, interrupted)
	end()
}

func TestWriteMarkers_Errors(t *testing.T) {
	closed := NewLocalRepo()
	_, _, err := closed.BeginWorkingWrite()
	require.Error(t, err)
	require.Error(t, closed.MarkInterruptedWrite())

	lr := newTestLocalRepo(t)
	require.NoError(t, os.WriteFile(lr.writesDir(), []byte("not a directory"), 0o644))
	_, _, err = lr.BeginWorkingWrite()
	require.Error(t, err)
	require.Error(t, lr.MarkInterruptedWrite())

	require.Equal(t, markerGone, markerState(filepath.Join(t.TempDir(), "missing")))
}
