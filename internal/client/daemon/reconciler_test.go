package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconciler_RefreshesStatCache(t *testing.T) {
	root := newTestClone(t)
	rp, err := openRepo(root, Runners{})
	require.NoError(t, err)
	src := newFakeSource()
	rp.startWatchingWith(src, 10*time.Millisecond)
	t.Cleanup(func() { _ = rp.close() })

	writeWorkingFile(t, root, "untracked.txt", "new")
	writeWorkingFile(t, root, "tracked.txt", "hello")
	src.emit("untracked.txt")
	src.emit("tracked.txt")

	require.Eventually(t, func() bool {
		entries, err := rp.localRepo.LoadStatCache()
		if err != nil {
			return false
		}
		_, ok := entries["tracked.txt"]
		return ok
	}, 2*time.Second, 10*time.Millisecond, "the reconciler must cache the refreshed fingerprint")

	entries, err := rp.localRepo.LoadStatCache()
	require.NoError(t, err)
	require.NotContains(t, entries, "untracked.txt", "untracked paths are never reconciled")
}

func TestReconciler_CloseStopsIt(t *testing.T) {
	root := newTestClone(t)
	rp, err := openRepo(root, Runners{})
	require.NoError(t, err)
	src := newFakeSource()
	rp.startWatchingWith(src, 10*time.Millisecond)

	require.NotNil(t, rp.watcher)
	require.NotNil(t, rp.reconciler)
	require.NoError(t, rp.close(), "close must stop the watcher and reconciler without hanging")
}

// TestRepo_StatusDetectsWithoutWatcher pins the accelerator guarantee: with no
// watcher at all, read-through status still detects changes.
func TestRepo_StatusDetectsWithoutWatcher(t *testing.T) {
	root := newTestClone(t)
	rp, err := openRepo(root, Runners{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = rp.close() })

	writeWorkingFile(t, root, "tracked.txt", "hello")
	st, err := rp.wc.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"tracked.txt"}, st.Modified)
}
