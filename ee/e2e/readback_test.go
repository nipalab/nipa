package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	eedb "github.com/nipalab/nipa/ee/db"
	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
)

// existsCountingStore is the S3 store with its existence checks counted; the
// embedded store keeps every other capability (direct transfers, streaming).
type existsCountingStore struct {
	*s3store.Store
	exists atomic.Int64
}

func (s *existsCountingStore) Exists(ctx context.Context, hash domain.Hash) (bool, error) {
	s.exists.Add(1)
	return s.Store.Exists(ctx, hash)
}

// TestEnterprisePushOverObjectsWithoutRows pushes content whose keys already
// hold objects the server never confirmed: presign hands out create-only
// targets without checking the store, the PUTs answer 412, and confirm reads
// the objects back. The valid object is linked as is; the garbage is deleted,
// uploaded again on the retry, and the push succeeds.
func TestEnterprisePushOverObjectsWithoutRows(t *testing.T) {
	ctx := context.Background()
	dbConn, err := eedb.Open(testPGDSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbConn.Close() })
	require.NoError(t, eedb.MigrateUp(dbConn))

	store := &existsCountingStore{Store: newS3ChunkStore(t).(*s3store.Store)}
	host := startEnterpriseServer(t, dbConn, store, mustSnowNode(t, 0), nil)
	repo, pusher := connectClient(t, host)
	url := "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	target := filepath.Join(t.TempDir(), "work")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, target))

	nonce := make([]byte, 8)
	_, err = rand.Read(nonce)
	require.NoError(t, err)
	unique := hex.EncodeToString(nonce)
	planted := "already uploaded by a push that never confirmed " + unique + "\n"
	poisoned := "content whose key holds garbage " + unique + "\n"
	fresh := "never seen before " + unique + "\n"

	encodedChunk := func(path, content string) (domain.Hash, []byte) {
		_, chunks, err := chunker.EncodeBytes([]byte(content), path, false, "")
		require.NoError(t, err)
		require.Len(t, chunks, 1)
		return chunks[0].Hash, chunks[0].Data
	}
	plantedHash, plantedData := encodedChunk("planted.txt", planted)
	poisonedHash, poisonedData := encodedChunk("poisoned.txt", poisoned)
	require.NoError(t, store.Put(ctx, plantedHash, plantedData))
	require.NoError(t, store.Put(ctx, poisonedHash, []byte("garbage under someone else's hash")))

	writeFile(t, target, "fresh.txt", fresh)
	stagePath(t, target, "fresh.txt")
	store.exists.Store(0)
	require.NoError(t, pusher.Run(ctx, target, "fresh content"))
	require.Zero(t, store.exists.Load(), "a push of content the server never saw makes no existence checks")

	writeFile(t, target, "planted.txt", planted)
	writeFile(t, target, "poisoned.txt", poisoned)
	stagePath(t, target, "planted.txt")
	stagePath(t, target, "poisoned.txt")
	require.NoError(t, pusher.Run(ctx, target, "push over objects without rows"))

	got, err := store.Get(ctx, poisonedHash)
	require.NoError(t, err)
	require.Equal(t, poisonedData, got, "the garbage was replaced by the real content")
	got, err = store.Get(ctx, plantedHash)
	require.NoError(t, err)
	require.Equal(t, plantedData, got)

	checkout := filepath.Join(t.TempDir(), "checkout")
	require.NoError(t, repo.Clone(ctx, url, host, e2eOrgSlug, e2eProjectSlug, "", nil, checkout))
	require.Equal(t, fresh, readFile(t, checkout, "fresh.txt"))
	require.Equal(t, planted, readFile(t, checkout, "planted.txt"))
	require.Equal(t, poisoned, readFile(t, checkout, "poisoned.txt"))
}
