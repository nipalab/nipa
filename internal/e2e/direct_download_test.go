package e2e

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type storeCountingRepo struct {
	*localrepo.LocalRepo
	stored atomic.Int64
}

func (r *storeCountingRepo) StoreChunks(chunks []*serverDomain.ChunkData) error {
	for _, c := range chunks {
		r.stored.Add(int64(len(c.Data)))
	}
	return r.LocalRepo.StoreChunks(chunks)
}

func (r *storeCountingRepo) StoreChunkFrom(hash serverDomain.Hash, body io.Reader) (int64, error) {
	n, err := r.LocalRepo.StoreChunkFrom(hash, body)
	r.stored.Add(n)
	return n, err
}

func TestEndToEnd_CloneAndUpdateWriteBinariesDirectly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	const binSize = 12 << 20
	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeBenchFile(t, dir, "assets/level.bin", binSize, false)
	writeBenchFile(t, dir, "assets/hero.png", binSize, false)
	writeBenchFile(t, dir, "docs/notes.txt", 256<<10, true)
	for _, p := range []string{"assets/level.bin", "assets/hero.png", "docs/notes.txt"} {
		stagePath(t, dir, p)
	}
	require.NoError(t, pusher.Run(ctx, dir, "seed assets"))

	lr := &storeCountingRepo{LocalRepo: localrepo.NewLocalRepo()}
	target := filepath.Join(t.TempDir(), "checkout")
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, clientusecase.NewRepo(auth, grpcClient, lr).Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
	require.Less(t, lr.stored.Load(), int64(1<<20), "raw binaries bypass the object cache on clone")
	for _, p := range []string{"assets/level.bin", "assets/hero.png", "docs/notes.txt"} {
		require.Equal(t, hashBenchFile(t, filepath.Join(dir, filepath.FromSlash(p))), hashBenchFile(t, filepath.Join(target, filepath.FromSlash(p))))
	}
	require.Empty(t, materializeTemps(t, target))

	locker := clientusecase.NewFileLock(auth, grpcClient, localrepo.NewLocalRepo())
	_, err := locker.Lock(ctx, dir, "assets/level.bin", clientusecase.LockOptions{})
	require.NoError(t, err)
	writeBenchFile(t, dir, "assets/level.bin", binSize+4096, false)
	stagePath(t, dir, "assets/level.bin")
	require.NoError(t, pusher.Run(ctx, dir, "teammate edit"))

	updateRepo := &storeCountingRepo{LocalRepo: localrepo.NewLocalRepo()}
	require.NoError(t, clientusecase.NewUpdate(auth, grpcClient, updateRepo).Run(ctx, target))
	require.Equal(t, hashBenchFile(t, filepath.Join(dir, "assets", "level.bin")), hashBenchFile(t, filepath.Join(target, "assets", "level.bin")))
	require.Empty(t, materializeTemps(t, target))
	require.Zero(t, updateRepo.stored.Load(), "the changed binary is written straight into its file on update")
	require.Less(t, benchDirSize(t, filepath.Join(target, ".nipa", "objects"), ""), int64(4<<20))
}

// TestEndToEnd_RestoreRebuildsBinariesDirectly restores edited and deleted
// raw binaries, at the root and in nested folders, against the real server:
// the pinned commit's manifest must be usable for every one of them, so not a
// byte passes through the object cache.
func TestEndToEnd_RestoreRebuildsBinariesDirectly(t *testing.T) {
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	const binSize = 6 << 20
	paths := []string{"root.bin", "assets/level.bin", "assets/deep/hero.png", "audio/music.bin"}
	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	for _, p := range paths {
		writeBenchFile(t, dir, p, binSize, false)
		stagePath(t, dir, p)
	}
	require.NoError(t, pusher.Run(ctx, dir, "seed binaries"))

	target := filepath.Join(t.TempDir(), "checkout")
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
	for i, p := range paths {
		fp := filepath.Join(target, filepath.FromSlash(p))
		if i == len(paths)-1 {
			require.NoError(t, os.Remove(fp))
			continue
		}
		f, err := os.OpenFile(fp, os.O_WRONLY, 0)
		require.NoError(t, err)
		_, err = f.WriteAt([]byte("local edit here!"), binSize-16)
		require.NoError(t, err)
		require.NoError(t, f.Close())
	}

	lr := &storeCountingRepo{LocalRepo: localrepo.NewLocalRepo()}
	wc, err := clientusecase.NewWorkingCopy(lr, target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, wc.WithDownloader(auth, grpcClient).Restore(ctx, paths))

	for _, p := range paths {
		require.Equal(t, hashBenchFile(t, filepath.Join(dir, filepath.FromSlash(p))), hashBenchFile(t, filepath.Join(target, filepath.FromSlash(p))), p)
	}
	require.Zero(t, lr.stored.Load(), "restored binaries never pass through the object cache")
	require.Empty(t, materializeTemps(t, target))
	status, err := wc.Status(ctx)
	require.NoError(t, err)
	require.Empty(t, status.Modified)
	require.Empty(t, status.Missing)
	require.Empty(t, status.Deleted)
}

func materializeTemps(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "*", ".nipa-materialize-*"))
	require.NoError(t, err)
	return matches
}
