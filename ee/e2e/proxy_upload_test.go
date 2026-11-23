package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// proxyCountingStore is the S3 store with every server-side read, existence
// check, overwrite and presigned upload counted.
type proxyCountingStore struct {
	*s3store.Store
	reads      atomic.Int64
	exists     atomic.Int64
	overwrites atomic.Int64
	presigns   atomic.Int64
}

func (s *proxyCountingStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	s.reads.Add(1)
	return s.Store.Get(ctx, projectID, hash)
}

func (s *proxyCountingStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	s.reads.Add(1)
	return s.Store.Open(ctx, projectID, hash)
}

func (s *proxyCountingStore) Exists(ctx context.Context, projectID snow.ID, hash domain.Hash) (bool, error) {
	s.exists.Add(1)
	return s.Store.Exists(ctx, projectID, hash)
}

func (s *proxyCountingStore) Overwrite(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	s.overwrites.Add(1)
	return s.Store.Overwrite(ctx, projectID, hash, data)
}

func (s *proxyCountingStore) PresignUpload(ctx context.Context, projectID snow.ID, hash domain.Hash, size int64, expires time.Duration) (storage.UploadTarget, error) {
	s.presigns.Add(1)
	return s.Store.PresignUpload(ctx, projectID, hash, size, expires)
}

// TestEnterpriseProxyUploads pushes through MinIO with CHUNK_UPLOAD_MODE=proxy:
// every chunk goes through the server's signed PUT path, which writes the
// bucket itself (replacing garbage a direct upload left at a key) and returns
// a receipt, so confirm reads nothing back. A clone then fetches the bytes
// directly from the bucket and checks them.
func TestEnterpriseProxyUploads(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	store := &proxyCountingStore{Store: newS3ChunkStore(t).(*s3store.Store)}
	host, _ := startEnterpriseServerWith(t, dbConn, store, mustSnowNode(t, 0), serverOptions{uploadMode: config.ChunkUploadModeProxy})
	repo, pusher := connectClient(t, host)
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))

	nonce := make([]byte, 8)
	_, err = rand.Read(nonce)
	require.NoError(t, err)
	unique := hex.EncodeToString(nonce)
	fresh := "proxied through the server " + unique + "\n"
	poisoned := "proxied over garbage " + unique + "\n"
	big := make([]byte, 3<<20)
	_, err = rand.Read(big)
	require.NoError(t, err)

	_, chunks, err := chunker.EncodeBytes([]byte(poisoned), "poisoned.txt", false, "")
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	poisonedHash, poisonedData := chunks[0].Hash, chunks[0].Data
	project := defaultProjectID(t, dbConn)
	require.NoError(t, store.Put(ctx, project, poisonedHash, []byte("garbage left by a direct upload")))

	writeFile(t, target, "fresh.txt", fresh)
	writeFile(t, target, "poisoned.txt", poisoned)
	writeFile(t, target, "big.bin", string(big))
	for _, path := range []string{"fresh.txt", "poisoned.txt", "big.bin"} {
		stagePath(t, target, path)
	}
	store.reads.Store(0)
	store.exists.Store(0)
	require.NoError(t, pusher.Run(ctx, target, "proxied push"))

	require.Zero(t, store.reads.Load(), "receipts replace every confirm read-back")
	require.Zero(t, store.exists.Load(), "neither presign nor the proxied PUT checks for objects")
	require.Zero(t, store.presigns.Load(), "no direct upload URL is handed out in proxy mode")
	require.Positive(t, store.overwrites.Load())

	got, err := store.Store.Get(ctx, project, poisonedHash)
	require.NoError(t, err)
	require.Equal(t, poisonedData, got, "the proxied write replaced the garbage")

	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.Equal(t, fresh, readFile(t, checkout, "fresh.txt"))
	require.Equal(t, poisoned, readFile(t, checkout, "poisoned.txt"))
	require.Equal(t, string(big), readFile(t, checkout, "big.bin"))
}
