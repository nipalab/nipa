package e2e

import (
	"context"
	"fmt"
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
// stored it and never confirmed). Presign puts it in the bundle without
// looking at the store, the bundle PUT dedupes and answers with a receipt,
// and confirm links the chunk on it.
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

// readCountingLocalStore is the free local store with its content reads and
// writes counted.
type readCountingLocalStore struct {
	*storage.LocalStore
	reads atomic.Int64
	puts  atomic.Int64
}

func (s *readCountingLocalStore) Put(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	s.puts.Add(1)
	return s.LocalStore.Put(ctx, projectID, hash, data)
}

func (s *readCountingLocalStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.LocalStore.Get(ctx, projectID, hash)
}

func (s *readCountingLocalStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	return s.LocalStore.Open(ctx, projectID, hash)
}

// TestEndToEnd_PushUploadsBundlesWithReceipts pushes to the free server,
// whose uploads always go in bundles through its signed bundle path: one
// request per upload window carries every chunk, the server writes each one
// and answers with receipts, so confirm reads nothing back. A clone verifies
// the bytes.
func TestEndToEnd_PushUploadsBundlesWithReceipts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	local, err := storage.NewLocalStore(t.TempDir())
	require.NoError(t, err)
	store := &readCountingLocalStore{LocalStore: local}
	host, _ := startTestServerWithUsecases(t, openTestDB(t), func(deps *serverapp.UsecaseDeps) { deps.ChunkStore = store })
	grpcClient, auth, counter := newBenchClient(t, host)
	pusher := clientusecase.NewPush(auth, grpcClient, localrepo.NewLocalRepo())

	dir := cloneWorktree(t, grpcClient, auth, host, "main")
	const small = "receipted by the free server\n"
	big := strings.Repeat("free server bundle receipts ", 200_000)
	files := map[string]string{"small.txt": small, "big.txt": big}
	for i := 0; i < 6; i++ {
		files[fmt.Sprintf("asset-%d.txt", i)] = fmt.Sprintf("asset %d\n", i)
	}
	for path, content := range files {
		writeFile(t, dir, path, content)
		stagePath(t, dir, path)
	}
	store.reads.Store(0)
	store.puts.Store(0)
	putsBefore, _ := counter.snapshot()
	require.NoError(t, pusher.Run(ctx, dir, "bundled push"))
	puts, _ := counter.snapshot()
	require.Equal(t, 1, puts-putsBefore, "one bundle request for the whole window")
	require.Equal(t, int64(len(files)), store.puts.Load(), "one store write per chunk")
	require.Zero(t, store.reads.Load(), "confirm accepts the receipts instead of reading back")

	checkout := filepath.Join(t.TempDir(), "checkout")
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, clientusecase.NewRepo(auth, grpcClient, localrepo.NewLocalRepo()).Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	for path, content := range files {
		require.Equal(t, content, readFile(t, checkout, path), path)
	}
}
