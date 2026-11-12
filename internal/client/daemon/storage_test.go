package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func storageServer(t *testing.T) (*Server, string, *repo) {
	t.Helper()
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)
	return srv, root, rp
}

func TestDiskUsage_ReportsSnapshotCacheAndPins(t *testing.T) {
	srv, root, rp := storageServer(t)
	data := []byte("cached object")
	require.NoError(t, rp.localRepo.StoreChunk(chunker.Sum(data), data))
	require.NoError(t, rp.localRepo.PinPaths([]string{"art"}))
	require.NoError(t, rp.localRepo.SaveConfig(clientDomain.Config{Url: rp.cfg.Url, Branch: "main", Sparse: []string{"code"}}))

	res, err := srv.DiskUsage(context.Background(), &daemonpb.DiskUsageRequest{Root: root})
	require.NoError(t, err)
	require.EqualValues(t, 1, res.WorkingFiles)
	require.EqualValues(t, 5, res.WorkingBytes)
	require.EqualValues(t, 1, res.CacheObjects)
	require.EqualValues(t, len(data), res.CacheBytes)
	require.Equal(t, []string{"art"}, res.PinnedPaths)
	require.Equal(t, []string{"code"}, res.Sparse)
}

func TestDiskUsage_FullCheckoutHasEmptyLists(t *testing.T) {
	srv, root, _ := storageServer(t)
	res, err := srv.DiskUsage(context.Background(), &daemonpb.DiskUsageRequest{Root: root})
	require.NoError(t, err)
	require.NotNil(t, res.PinnedPaths)
	require.Empty(t, res.PinnedPaths)
	require.Empty(t, res.Sparse)
}

func TestDiskUsage_UnwatchedRoot(t *testing.T) {
	srv := newOpServer(t, RepoOps{})
	_, err := srv.DiskUsage(context.Background(), &daemonpb.DiskUsageRequest{Root: newTestClone(t)})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestDiskUsage_CacheError(t *testing.T) {
	srv, root, _ := storageServer(t)
	objects := filepath.Join(root, localrepo.ConfigDir, localrepo.ObjectsDir)
	require.NoError(t, os.RemoveAll(objects))
	require.NoError(t, os.WriteFile(objects, nil, 0o644))
	_, err := srv.DiskUsage(context.Background(), &daemonpb.DiskUsageRequest{Root: root})
	require.Error(t, err)
}

func TestGC_FreesUnreferencedObjects(t *testing.T) {
	srv, root, rp := storageServer(t)
	data := []byte("orphan object")
	require.NoError(t, rp.localRepo.StoreChunk(chunker.Sum(data), data))
	rp.pruner.request()

	res, err := srv.GC(context.Background(), &daemonpb.GCRequest{Root: root})
	require.NoError(t, err)
	require.False(t, res.Skipped)
	require.EqualValues(t, 1, res.Objects)
	require.EqualValues(t, len(data), res.Bytes)

	rp.pruner.mu.Lock()
	pending := rp.pruner.pending
	rp.pruner.mu.Unlock()
	require.False(t, pending, "a manual run settles the idle pruner")

	again, err := srv.GC(context.Background(), &daemonpb.GCRequest{Root: root})
	require.NoError(t, err)
	require.Zero(t, again.Objects)
}

func TestGC_SkipsWhileMergePending(t *testing.T) {
	srv, root, rp := storageServer(t)
	data := []byte("kept during merge")
	require.NoError(t, rp.localRepo.StoreChunk(chunker.Sum(data), data))
	require.NoError(t, rp.localRepo.SaveMergeState(&clientDomain.MergeState{SourceBranch: "feature"}))

	res, err := srv.GC(context.Background(), &daemonpb.GCRequest{Root: root})
	require.NoError(t, err)
	require.True(t, res.Skipped)
	require.Equal(t, usecase.PruneSkipPending, res.SkipReason)
	_, err = rp.localRepo.LoadChunk(chunker.Sum(data))
	require.NoError(t, err)
}

func TestGC_SkipsWhenAnotherProcessHoldsTheLock(t *testing.T) {
	srv, root, rp := storageServer(t)
	other := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, other.Init(root))
	defer func() { _ = other.Close() }()
	release, err := other.LockShared()
	require.NoError(t, err)
	defer release()
	rp.pruner.request()

	res, err := srv.GC(context.Background(), &daemonpb.GCRequest{Root: root})
	require.NoError(t, err)
	require.True(t, res.Skipped)
	require.Equal(t, usecase.PruneSkipBusy, res.SkipReason)
	rp.pruner.mu.Lock()
	pending := rp.pruner.pending
	rp.pruner.mu.Unlock()
	require.True(t, pending, "a skipped run leaves the pruner's request in place")
}

func TestGC_WaitsForRunningOperation(t *testing.T) {
	srv, root, rp := storageServer(t)
	release, _, err := rp.coord.Acquire(context.Background(), true)
	require.NoError(t, err)

	done := make(chan *daemonpb.GCResponse, 1)
	go func() {
		res, err := srv.GC(context.Background(), &daemonpb.GCRequest{Root: root})
		if err == nil {
			done <- res
		}
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("GC ran while the exclusive slot was held")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	res, ok := <-done
	require.True(t, ok)
	require.False(t, res.Skipped)
}

func TestGC_CancelledWhileQueued(t *testing.T) {
	srv, root, rp := storageServer(t)
	release, _, err := rp.coord.Acquire(context.Background(), true)
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = srv.GC(ctx, &daemonpb.GCRequest{Root: root})
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
}

func TestGC_UnwatchedRoot(t *testing.T) {
	srv := newOpServer(t, RepoOps{})
	_, err := srv.GC(context.Background(), &daemonpb.GCRequest{Root: newTestClone(t)})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCachePruner_SettleCancelsScheduledPrune(t *testing.T) {
	coord := newCoordinator()
	p, calls := countingPruner(coord)
	defer p.close()
	p.settle()
	p.request()
	p.settle()
	time.Sleep(5 * testQuiet)
	require.Zero(t, calls.Load())
}
