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

	// PresignUpload returns an absolute PUT target for one chunk of the given
	// exact size. The target writes the shared content-addressed key, so it
	// must be create-only: the request is signed with If-None-Match: * (the
	// backend rejects a write when the object exists, even if the target is
	// replayed) and with the exact content length.
	PresignUpload(ctx context.Context, hash domain.Hash, size int64, expires time.Duration) (UploadTarget, error)

	// DeleteChunk removes chunk content, used to drop an object whose bytes
	// do not match its hash.
	DeleteChunk(ctx context.Context, hash domain.Hash) error
}

// UploadTarget is an absolute PUT URL plus the request headers the client must
// send with it; the backend verifies them because they are part of the
// signature.
type UploadTarget struct {
	URL     string
	Headers map[string]string
}
