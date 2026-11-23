package e2e

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// TestEndToEnd_PushOverStoredContentWithoutARow pushes a file whose chunk is
// already in the free server's store without a metadata row (an earlier push
// stored it and never confirmed). Presign issues a target without looking at
// the store, the signed PUT dedupes and answers with a receipt, and confirm
// links the chunk on it.
func TestEndToEnd_PushOverStoredContentWithoutARow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	host, uc := startTestServerWithUsecases(t, openTestDB(t), func(deps *serverapp.UsecaseDeps) { deps.ChunkStore = store })
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())
	_, project, err := uc.Common.ResolveBySlug(ctx, e2eOrgSlug, e2eProjectSlug)
	require.NoError(t, err)

	const content = "stored by a push that never confirmed\n"
	_, chunks, err := chunker.EncodeBytes([]byte(content), "orphan.txt", false, "")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	require.NoError(t, store.Put(ctx, project.ID, chunks[0].Hash, chunks[0].Data))

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	writeFile(t, dir, "orphan.txt", content)
	stagePath(t, dir, "orphan.txt")
	require.NoError(t, pusher.Run(ctx, dir, "push over an unconfirmed chunk"))

	checkout := filepath.Join(t.TempDir(), "checkout")
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.Equal(t, content, readFile(t, checkout, "orphan.txt"))
}

// readCountingLocalStore is the free local store with its content reads
// counted.
type readCountingLocalStore struct {
	*storage.LocalStore
	reads atomic.Int64
}

func (s *readCountingLocalStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.LocalStore.Get(ctx, projectID, hash)
}

func (s *readCountingLocalStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	return s.LocalStore.Open(ctx, projectID, hash)
}

// TestEndToEnd_PushConfirmsWithReceipts pushes to the free server, whose
// uploads always go through its signed PUT path: every PUT answers with a
// receipt, so confirm reads nothing back. A clone verifies the bytes.
func TestEndToEnd_PushConfirmsWithReceipts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	local, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	store := &readCountingLocalStore{LocalStore: local}
	host, _ := startTestServerWithUsecases(t, openTestDB(t), func(deps *serverapp.UsecaseDeps) { deps.ChunkStore = store })
	grpcClient, auth, _ := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	const small = "receipted by the free server\n"
	big := strings.Repeat("free server proxy receipts ", 200_000)
	writeFile(t, dir, "small.txt", small)
	writeFile(t, dir, "big.txt", big)
	stagePath(t, dir, "small.txt")
	stagePath(t, dir, "big.txt")
	store.reads.Store(0)
	require.NoError(t, pusher.Run(ctx, dir, "receipted push"))
	require.Zero(t, store.reads.Load(), "confirm accepts the receipts instead of reading back")

	checkout := filepath.Join(t.TempDir(), "checkout")
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.Equal(t, small, readFile(t, checkout, "small.txt"))
	require.Equal(t, big, readFile(t, checkout, "big.txt"))
}
