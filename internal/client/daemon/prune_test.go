package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
)

const testQuiet = 20 * time.Millisecond

func countingPruner(coord *coordinator, results ...bool) (*cachePruner, *atomic.Int32) {
	var calls atomic.Int32
	p := newCachePruner("root", coord, testQuiet, func() (bool, error) {
		n := int(calls.Add(1))
		if n <= len(results) {
			return results[n-1], nil
		}
		return false, nil
	})
	coord.activity = p.touch
	return p, &calls
}

func TestCachePruner_RunsOnceAfterQuietPeriod(t *testing.T) {
	coord := newCoordinator()
	p, calls := countingPruner(coord)
	defer p.close()

	p.request()
	p.request()
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(5 * testQuiet)
	require.EqualValues(t, 1, calls.Load())
}

func TestCachePruner_IdleWithoutRequest(t *testing.T) {
	coord := newCoordinator()
	p, calls := countingPruner(coord)
	defer p.close()

	release, _, err := coord.Acquire(context.Background(), false)
	require.NoError(t, err)
	release()
	time.Sleep(5 * testQuiet)
	require.Zero(t, calls.Load())
}

func TestCachePruner_ActivityPostponesPrune(t *testing.T) {
	coord := newCoordinator()
	p, calls := countingPruner(coord)
	defer p.close()

	p.request()
	deadline := time.Now().Add(8 * testQuiet)
	for time.Now().Before(deadline) {
		release, _, err := coord.Acquire(context.Background(), false)
		require.NoError(t, err)
		release()
		time.Sleep(testQuiet / 4)
	}
	require.Zero(t, calls.Load(), "each admission restarts the quiet period")
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
}

func TestCachePruner_WaitsWhileOperationRuns(t *testing.T) {
	for _, exclusive := range []bool{false, true} {
		coord := newCoordinator()
		p, calls := countingPruner(coord)

		release, _, err := coord.Acquire(context.Background(), exclusive)
		require.NoError(t, err)
		p.request()
		time.Sleep(6 * testQuiet)
		require.Zero(t, calls.Load(), "no prune while an operation holds the root")
		release()
		require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
		p.close()
	}
}

func TestCachePruner_RetriesWhenRepositoryBusy(t *testing.T) {
	coord := newCoordinator()
	p, calls := countingPruner(coord, true)
	defer p.close()

	p.request()
	require.Eventually(t, func() bool { return calls.Load() == 2 }, time.Second, time.Millisecond)
}

func TestCachePruner_LogsErrors(t *testing.T) {
	coord := newCoordinator()
	var calls atomic.Int32
	p := newCachePruner("root", coord, testQuiet, func() (bool, error) {
		calls.Add(1)
		return false, errors.New("disk")
	})
	defer p.close()

	p.request()
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		release, ok := coord.tryAcquireExclusive()
		if ok {
			release()
		}
		return ok
	}, time.Second, time.Millisecond, "the prune released the slot")
}

func TestCachePruner_CloseCancelsPending(t *testing.T) {
	coord := newCoordinator()
	p, calls := countingPruner(coord)
	p.request()
	p.close()
	p.request()
	time.Sleep(5 * testQuiet)
	require.Zero(t, calls.Load())
}

func TestCachePruner_DefaultQuiet(t *testing.T) {
	p := newCachePruner("root", newCoordinator(), 0, nil)
	require.Equal(t, defaultPruneQuiet, p.quiet)
}

func TestCoordinator_TryAcquireExclusive(t *testing.T) {
	coord := newCoordinator()
	release, ok := coord.tryAcquireExclusive()
	require.True(t, ok)
	_, ok = coord.tryAcquireExclusive()
	require.False(t, ok)
	release()

	shared, _, err := coord.Acquire(context.Background(), false)
	require.NoError(t, err)
	_, ok = coord.tryAcquireExclusive()
	require.False(t, ok)
	shared()
}

func objectFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(root, ".nipa", "objects"), func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	require.NoError(t, err)
	return n
}

func TestRegistry_PrunesWatchedRootAfterQuietPeriod(t *testing.T) {
	root := newTestCloneWithContent(t, "art/hero.bin", "\x00raw binary content\x01")
	require.NotZero(t, objectFiles(t, root))
	reg := newRegistry(Runners{})
	reg.pruneQuiet = testQuiet
	rp, err := reg.watch(root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reg.unwatch(root) })

	release, _, err := rp.coord.Acquire(context.Background(), false)
	require.NoError(t, err)
	rp.pruner.request()
	time.Sleep(6 * testQuiet)
	require.NotZero(t, objectFiles(t, root), "kept while a read holds the root")
	release()

	require.Eventually(t, func() bool { return objectFiles(t, root) == 0 }, 2*time.Second, 5*time.Millisecond)
}

func TestServer_ReadBlobSchedulesPrune(t *testing.T) {
	root := newTestCloneWithContent(t, "art/hero.bin", "\x00raw binary content\x01")
	srv := newOpServer(t, RepoOps{Diff: &stubDiffRunner{readBlob: func(_ context.Context, _ string, _ usecase.BlobRequest, fn func(diff.Entry, io.Reader) error) error {
		return fn(diff.Entry{Path: "art/hero.bin"}, strings.NewReader("x"))
	}}})
	srv.repos.pruneQuiet = testQuiet
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	require.NoError(t, srv.ReadBlob(&daemonpb.ReadBlobRequest{Root: root, Blob: &daemonpb.BlobRef{Source: "snapshot", Path: "art/hero.bin"}}, blobStream()))
	require.NotZero(t, objectFiles(t, root), "no prune right after the read")
	require.Eventually(t, func() bool { return objectFiles(t, root) == 0 }, 2*time.Second, 5*time.Millisecond)
}
