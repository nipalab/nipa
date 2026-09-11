package usecase

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/storage"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=chunk_mock_test.go -package=usecase
type chunkRepository interface {
	InsertChunkIfNotExists(ctx context.Context, hash domain.Hash, sizeBytes int64) error
}

// Chunk moves chunk content between clients and the server. Upload verifies
// the BLAKE3 hash server-side and dedupes; download reads stored content back.
type Chunk struct {
	chunkRepo  chunkRepository
	chunkStore storage.ChunkStore
}

func NewChunk(chunkRepo chunkRepository, chunkStore storage.ChunkStore) *Chunk {
	return &Chunk{
		chunkRepo:  chunkRepo,
		chunkStore: chunkStore,
	}
}

// Upload stores one chunk. It returns true when the content was newly stored,
// false when an identical chunk was already present. The chunk metadata row is
// recorded only after content exists, so a Push that references this hash can
// rely on it.
func (c *Chunk) Upload(ctx context.Context, hash domain.Hash, data []byte) (bool, error) {
	if got := chunker.Sum(data); got != hash {
		return false, domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", hash))
	}
	exists, err := c.chunkStore.Exists(ctx, hash)
	if err != nil {
		return false, domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", hash, err))
	}
	if exists {
		return false, nil
	}
	if err := c.chunkStore.Put(ctx, hash, data); err != nil {
		return false, domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
	}
	if err := c.chunkRepo.InsertChunkIfNotExists(ctx, hash, int64(len(data))); err != nil {
		return false, err
	}
	return true, nil
}

// Download returns the stored content for a chunk hash.
func (c *Chunk) Download(ctx context.Context, hash domain.Hash) ([]byte, error) {
	data, err := c.chunkStore.Get(ctx, hash)
	if err != nil {
		return nil, err
	}
	return data, nil
}
