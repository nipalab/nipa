package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// requireCacheSettled checks the object cache of a clone is what the full
// sweep leaves: every chunk a tracked file keeps (compressed text, or raw
// content under a pin) is cached, and the sweep finds nothing to delete. A
// targeted prune that ran before must therefore have deleted exactly what the
// sweep would have.
func requireCacheSettled(t *testing.T, dir, after string) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	require.NoError(t, lr.Init(dir))
	defer func() { _ = lr.Close() }()
	snapshot, err := lr.Snapshot()
	require.NoError(t, err)
	pins, err := lr.ListPins()
	require.NoError(t, err)
	var keep []serverDomain.Hash
	for _, f := range snapshot.Files {
		pinned := false
		for _, pin := range pins {
			pinned = pinned || serverDomain.PrefixCovers(pin, f.Path)
		}
		if f.Encoding == "zstd" || pinned {
			keep = append(keep, f.Chunks...)
		}
	}
	missing, err := lr.MissingChunks(keep)
	require.NoError(t, err)
	require.Empty(t, missing, "after %s: a kept chunk was deleted", after)
	result, err := clientusecase.PruneCache(lr)
	require.NoError(t, err)
	require.False(t, result.Skipped)
	require.Zero(t, result.Objects, "after %s: the full sweep still finds orphans", after)
}

// smallCommandCosts runs lock, unlock, a 20-file push on a fresh feature
// branch, a switch 20 files apart (delta) and blame in a clone of total text
// files plus one binary, and returns the repository reads each made.
func smallCommandCosts(t *testing.T, total int) map[string]string {
	t.Helper()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, _ := newBenchClient(t, host)

	seed := cloneWorktree(t, grpcClient, auth, host, "main")
	paths := make([]string, total)
	for i := range paths {
		paths[i] = smallUpdatePath(i)
		writeFile(t, seed, paths[i], fmt.Sprintf(benchTreeFilePayload, i))
	}
	const binary = "assets/model.bin"
	writeBinaryContent(t, seed, binary, append([]byte{0x00, 0x01}, make([]byte, 4096)...))
	stagePaths(t, seed, append(append([]string(nil), paths...), binary))
	seedPush := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	require.NoError(t, seedPush.Run(ctx, seed, "seed"))
	blamed := paths[1]
	for i := 0; i < 3; i++ {
		writeFile(t, seed, blamed, fmt.Sprintf("version %d\nshared line\n", i))
		stagePaths(t, seed, []string{blamed})
		require.NoError(t, seedPush.Run(ctx, seed, fmt.Sprintf("blame %d", i)))
	}

	work := cloneWorktree(t, grpcClient, auth, host, "main")
	costs := map[string]string{}
	run := func(name string, op func(repo *costRepo)) {
		repo := &costRepo{LocalRepo: localrepo.NewLocalRepo(), c: &repoCost{}}
		op(repo)
		require.NoError(t, repo.Close())
		costs[name] = repo.c.String()
		requireCacheSettled(t, work, name)
	}

	run("lock", func(repo *costRepo) {
		_, err := clientusecase.NewFileLock(auth, grpcClient, repo).Lock(ctx, work, binary, clientusecase.LockOptions{})
		require.NoError(t, err)
	})
	run("unlock", func(repo *costRepo) {
		require.NoError(t, clientusecase.NewFileLock(auth, grpcClient, repo).Unlock(ctx, work, binary, ""))
	})
	_, err := clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).CreateBranch(ctx, work, host, e2eOrgSlug, e2eProjectSlug, "feature")
	require.NoError(t, err)
	twenty := make([]string, 20)
	for k := range twenty {
		twenty[k] = paths[(k*37+5)%total]
		writeFile(t, work, twenty[k], fmt.Sprintf("feature change %d\n", k))
	}
	stagePaths(t, work, twenty)
	run("push 20", func(repo *costRepo) {
		require.NoError(t, clientusecase.NewPush(auth, grpcClient, repo).Run(ctx, work, "twenty"))
	})
	run("switch main", func(repo *costRepo) {
		require.NoError(t, clientusecase.NewUpdate(auth, grpcClient, repo).Switch(ctx, work, "main"))
	})
	run("switch feature", func(repo *costRepo) {
		require.NoError(t, clientusecase.NewUpdate(auth, grpcClient, repo).Switch(ctx, work, "feature"))
	})
	run("blame", func(repo *costRepo) {
		res, err := clientusecase.NewBlame(auth, grpcClient, repo).Run(ctx, work, blamed, "", nil)
		require.NoError(t, err)
		require.Len(t, res.Lines, 2)
	})
	require.Equal(t, workingTree(t, work), workingTree(t, cloneWorktree(t, grpcClient, auth, host, "feature")))
	return costs
}

// Lock, unlock, a 20-file push, a switch 20 files apart and blame read no
// whole table and list no object cache: at most one chunk scan inside SQLite
// (the prune's lookup), and the same reads whatever the size of the clone.
// Each command also leaves the object cache exactly as the full sweep would.
func TestEndToEnd_SmallCommandCostsFollowTheChange(t *testing.T) {
	t.Parallel()
	small := smallCommandCosts(t, 200)
	large := smallCommandCosts(t, 1200)
	require.Equal(t, small, large)
	want := map[string]string{
		"lock":           "snapshot rows 0, stat_cache rows 0, cache listings 0, chunk scans 0, by-path rows 2",
		"unlock":         "snapshot rows 0, stat_cache rows 0, cache listings 0, chunk scans 1, by-path rows 1",
		"push 20":        "snapshot rows 0, stat_cache rows 0, cache listings 0, chunk scans 1, by-path rows 40",
		"switch main":    "snapshot rows 0, stat_cache rows 0, cache listings 0, chunk scans 1, by-path rows 40",
		"switch feature": "snapshot rows 0, stat_cache rows 0, cache listings 0, chunk scans 1, by-path rows 40",
		"blame":          "snapshot rows 0, stat_cache rows 0, cache listings 0, chunk scans 1, by-path rows 0",
	}
	require.Equal(t, want, large)
}
