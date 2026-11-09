package e2e

import (
	"context"
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

func TestEndToEnd_CloneAndUpdateWriteBinariesDirectly(t *testing.T) {
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
	_, err := locker.Lock(ctx, dir, "assets/level.bin", "")
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

func materializeTemps(t *testing.T, root string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "*", ".nipa-materialize-*"))
	require.NoError(t, err)
	return matches
}
