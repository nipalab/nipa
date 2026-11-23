// Package storage abstracts where chunk content lives on the server.
//
// Chunks are immutable and content-addressed by their BLAKE3 hash inside one
// project: dedup is per project, so the same bytes in two projects are two
// objects. Content is keyed by (project, hash) and Put is idempotent. Every
// backend lays objects out the same way below its root or prefix:
//
//	p/<project base36>/<hash[:2]>/<hash[2:]>
//
// so a project's content is one prefix. Stores implementing PackStore also
// hold packs of many chunks at packs/<project base36>/<pack id base36>. The
// default backend is local: chunk
// content is written to the server's filesystem.
package storage

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// ChunkStore persists chunk content keyed by project and content hash.
type ChunkStore interface {
	// Put stores chunk content. The caller guarantees data hashes to hash;
	// implementations should skip the write when the chunk is already present.
	Put(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error

	// Get returns the chunk content, or a domain NotFound error when absent.
	Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error)

	// Size returns the stored content size in bytes, or a domain NotFound
	// error when absent. It is the authoritative size for chunk metadata.
	Size(ctx context.Context, projectID snow.ID, hash domain.Hash) (int64, error)

	// Exists reports whether the chunk content is already present.
	Exists(ctx context.Context, projectID snow.ID, hash domain.Hash) (bool, error)

	// Close releases any resources held by the store.
	Close() error
}

// ChunkOpener is implemented by stores that can stream chunk content instead of
// loading it whole, so a reader such as the confirm verification holds only a
// small buffer per chunk. Callers fall back to ChunkStore.Get for stores
// without it.
type ChunkOpener interface {
	// Open returns a reader over the chunk content, or a domain NotFound
	// error when absent. The caller must close it.
	Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error)
}

// ChunkOverwriter is implemented by stores that clients can also write
// directly (DirectTransferStore), where an untrusted upload may have left
// garbage at a key. Overwrite writes content the server verified itself
// unconditionally, with no existence check first: rewriting identical content
// is harmless and replaces whatever sat at the key. Proxied uploads use it.
type ChunkOverwriter interface {
	Overwrite(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error
}

// ProjectDeleter is implemented by stores that can remove every object of one
// project (its key prefix) at once; the project purge uses it.
type ProjectDeleter interface {
	DeleteProject(ctx context.Context, projectID snow.ID) error
}

// DirectTransferStore is implemented by backends that hand out their own
// presigned transfer URLs so clients can move chunk bytes directly to and from
// the backend instead of proxying them through the server. Returned URLs are
// absolute, carry their own authorization and expire after at most the given
// duration.
type DirectTransferStore interface {
	// PresignDownload returns an absolute URL for reading chunk content.
	PresignDownload(ctx context.Context, projectID snow.ID, hash domain.Hash, expires time.Duration) (string, error)

	// PresignUpload returns an absolute PUT target for one chunk of the given
	// exact size. The target writes the project's key for the hash, so it
	// must be create-only: the request is signed with If-None-Match: * (the
	// backend rejects a write when the object exists, even if the target is
	// replayed) and with the exact content length.
	PresignUpload(ctx context.Context, projectID snow.ID, hash domain.Hash, size int64, expires time.Duration) (UploadTarget, error)

	// DeleteChunk removes chunk content, used to drop an object whose bytes
	// do not match its hash.
	DeleteChunk(ctx context.Context, projectID snow.ID, hash domain.Hash) error
}

// UploadTarget is an absolute PUT URL plus the request headers the client must
// send with it; the backend verifies them because they are part of the
// signature.
type UploadTarget struct {
	URL     string
	Headers map[string]string
}

// PackStore is implemented by stores that hold chunk packs (internal/storage/
// pack): objects written by the server only, each holding many chunks of one
// project, addressed by key (PackKey) and read by byte range. The server
// writes bundle uploads as packs only to a store implementing it (S3 in
// proxy mode with CHUNK_PACKS=on); the local store keeps loose files.
type PackStore interface {
	// PutPack writes the object at key with exactly size bytes from body in
	// one single-part request, replacing anything at the key. body may be
	// read again from the start (it seeks) when the request is retried.
	PutPack(ctx context.Context, key string, body io.ReadSeeker, size int64) error

	// GetRange returns length bytes of the object at key starting at
	// offset, or a domain NotFound error when the object is absent. A
	// shorter object is an error, never a short read.
	GetRange(ctx context.Context, key string, offset, length int64) ([]byte, error)

	// PresignPackDownload returns an absolute URL for reading the object at
	// key; clients send a Range header with it to read part of the object.
	PresignPackDownload(ctx context.Context, key string, expires time.Duration) (string, error)

	// DeletePack removes the object at key; a missing object is not an
	// error.
	DeletePack(ctx context.Context, key string) error
}

// projectKeyPrefix is the first path element of every chunk key.
const projectKeyPrefix = "p"

// packKeyPrefix is the first path element of every pack key.
const packKeyPrefix = "packs"

// PackPrefix is the key prefix (no trailing slash) under which every pack
// of the project lives: "packs/<project base36>".
func PackPrefix(projectID snow.ID) string {
	return packKeyPrefix + "/" + projectID.Base36()
}

// PackKey is the key of one pack below a store's prefix:
// "packs/<project base36>/<pack id base36>".
func PackKey(projectID, packID snow.ID) string {
	return PackPrefix(projectID) + "/" + packID.Base36()
}

// ProjectPrefix is the key prefix (no trailing slash) under which every
// chunk of the project lives: "p/<project base36>".
func ProjectPrefix(projectID snow.ID) string {
	return projectKeyPrefix + "/" + projectID.Base36()
}

// ChunkKey is the key of one chunk below a store's root or prefix:
// "p/<project base36>/<hash[:2]>/<hash[2:]>".
func ChunkKey(projectID snow.ID, hash domain.Hash) string {
	hex := hash.String()
	return ProjectPrefix(projectID) + "/" + hex[:2] + "/" + hex[2:]
}

// ParseChunkKey reverses ChunkKey. Keys of any other shape (the old global
// layout, another tool's objects) are rejected.
func ParseChunkKey(key string) (snow.ID, domain.Hash, bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != projectKeyPrefix || len(parts[2]) != 2 {
		return 0, domain.Hash{}, false
	}
	projectID, err := snow.ParseBase36(parts[1])
	if err != nil || projectID.Base36() != parts[1] {
		return 0, domain.Hash{}, false
	}
	hash, err := domain.ParseHashHex(parts[2] + parts[3])
	if err != nil {
		return 0, domain.Hash{}, false
	}
	return projectID, hash, true
}
