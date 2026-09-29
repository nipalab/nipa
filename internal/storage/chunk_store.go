// Package storage abstracts where chunk content lives on the server.
//
// Chunks are immutable and content-addressed by their BLAKE3 hash, so content
// is keyed purely by hash and Put is idempotent. The default backend is local:
// chunk content is written to the server's filesystem, uploaded by clients
// over gRPC.
package storage

import (
	"context"
	"time"

	"github.com/nipalab/nipa/internal/domain"
)

// ChunkStore persists chunk content keyed by content hash.
type ChunkStore interface {
	// Put stores chunk content. The caller guarantees data hashes to hash;
	// implementations should skip the write when the chunk is already present.
	Put(ctx context.Context, hash domain.Hash, data []byte) error

	// Get returns the chunk content, or a domain NotFound error when absent.
	Get(ctx context.Context, hash domain.Hash) ([]byte, error)

	// Size returns the stored content size in bytes, or a domain NotFound
	// error when absent. It is the authoritative size for chunk metadata.
	Size(ctx context.Context, hash domain.Hash) (int64, error)

	// Exists reports whether the chunk content is already present.
	Exists(ctx context.Context, hash domain.Hash) (bool, error)

	// Close releases any resources held by the store.
	Close() error
}

// DirectTransferStore is implemented by backends that hand out their own
// presigned transfer URLs so clients can move chunk bytes directly to and from
// the backend instead of proxying them through the server. Returned URLs are
// absolute, carry their own authorization and expire after at most the given
// duration.
type DirectTransferStore interface {
	// PresignDownload returns an absolute URL for reading chunk content.
	PresignDownload(ctx context.Context, hash domain.Hash, expires time.Duration) (string, error)
}
