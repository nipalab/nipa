package daemon

import (
	"context"
	"errors"
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

func TestReconciler_SurvivesRefreshAndWatcherErrors(t *testing.T) {
	root := newTestClone(t)
	rp, err := openRepo(root, Runners{})
	require.NoError(t, err)
	src := newFakeSource()
	rp.startWatchingWith(src, 10*time.Millisecond)

	require.NoError(t, rp.localRepo.Close(), "every refresh now fails")

	// waitBatch queues a dirty path behind the exclusive slot, proving the
	// reconciler reached refresh, then lets the refresh run and fail.
	waitBatch := func() {
		t.Helper()
		holder, _, err := rp.coord.Acquire(context.Background(), true)
		require.NoError(t, err)
		writeWorkingFile(t, root, "tracked.txt", "hello")
		src.emit("tracked.txt")
		require.Eventually(t, func() bool { return rp.coord.queuedLen() == 1 }, 2*time.Second, time.Millisecond,
			"the reconciler must receive the batch")
		holder()
		require.Eventually(t, func() bool { return rp.coord.queuedLen() == 0 }, 2*time.Second, time.Millisecond)
	}
	waitBatch()
	src.fail(errors.New("queue overflow"))
	waitBatch()

	require.NoError(t, rp.close(), "the reconciler must survive errors and stop cleanly")
}

func TestReconciler_StopCancelsQueuedRefresh(t *testing.T) {
	root := newTestClone(t)
	rp, err := openRepo(root, Runners{})
	require.NoError(t, err)
	src := newFakeSource()
	rp.startWatchingWith(src, 10*time.Millisecond)

	holder, _, err := rp.coord.Acquire(context.Background(), true)
	require.NoError(t, err)

	writeWorkingFile(t, root, "tracked.txt", "hello")
	src.emit("tracked.txt")
	require.Eventually(t, func() bool { return rp.coord.queuedLen() == 1 }, 2*time.Second, time.Millisecond,
		"the reconciler waits behind the exclusive slot")

	done := make(chan struct{})
	go func() {
		_ = rp.close() // stop cancels the queued refresh and drains the loop
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("close did not cancel the queued refresh")
	}
	holder()
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
