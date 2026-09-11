// Package storage abstracts where chunk content lives on the server.
//
// Chunks are immutable and content-addressed by their BLAKE3 hash, so content
// is keyed purely by hash and Put is idempotent. The default backend is local:
// chunk content is written to the server's filesystem, uploaded by clients
// over gRPC.
package storage

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
)

// ChunkStore persists chunk content keyed by content hash.
type ChunkStore interface {
	// Put stores chunk content. The caller guarantees data hashes to hash;
	// implementations should skip the write when the chunk is already present.
	Put(ctx context.Context, hash domain.Hash, data []byte) error

	// Get returns the chunk content, or a domain NotFound error when absent.
	Get(ctx context.Context, hash domain.Hash) ([]byte, error)

	// Exists reports whether the chunk content is already present.
	Exists(ctx context.Context, hash domain.Hash) (bool, error)

	// Close releases any resources held by the store.
	Close() error
}
