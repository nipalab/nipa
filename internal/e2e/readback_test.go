package e2e

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/storage"
)

// TestEndToEnd_PushOverStoredContentWithoutARow pushes a file whose chunk is
// already in the free server's store without a metadata row (an earlier push
// stored it and never confirmed). Presign issues a target without looking at
// the store, the signed PUT dedupes, and confirm reads the bytes back and
// links them.
func TestEndToEnd_PushOverStoredContentWithoutARow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	host, _ := startTestServerWithUsecases(t, openTestDB(t), func(deps *serverapp.UsecaseDeps) { deps.ChunkStore = store })
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	const content = "stored by a push that never confirmed\n"
	_, chunks, err := chunker.EncodeBytes([]byte(content), "orphan.txt", false, "")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.NoError(t, store.Put(ctx, chunks[0].Hash, chunks[0].Data))

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, dir, "orphan.txt", content)
	stagePath(t, dir, "orphan.txt")
	require.NoError(t, pusher.Run(ctx, dir, "push over an unconfirmed chunk"))

	checkout := filepath.Join(t.TempDir(), "checkout")
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.Equal(t, content, readFile(t, checkout, "orphan.txt"))
}
