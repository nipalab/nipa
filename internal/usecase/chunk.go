package usecase

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/storage"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=chunk_mock_test.go -package=usecase
type chunkRepository interface {
	InsertChunkIfNotExists(ctx context.Context, hash domain.Hash, sizeBytes int64) error
}

// ChunkRef identifies a chunk and the exact size of its content.
type ChunkRef struct {
	Hash      domain.Hash
	SizeBytes int64
}

// ChunkURL pairs a chunk hash with its signed transfer path.
type ChunkURL struct {
	Hash          domain.Hash
	URL           string
	AlreadyStored bool
}

// ChunkTransferConfig configures presigned chunk transfer.
type ChunkTransferConfig struct {
	SigningKey  string
	PresignTTL  time.Duration
	MaxPageSize int
}

// Chunk moves chunk content between clients and the server. Upload verifies
// the BLAKE3 hash server-side and dedupes; download reads stored content back.
type Chunk struct {
	chunkRepo  chunkRepository
	chunkStore storage.ChunkStore
	transfer   ChunkTransferConfig
	now        func() time.Time
}

func NewChunk(chunkRepo chunkRepository, chunkStore storage.ChunkStore, transfer ChunkTransferConfig) *Chunk {
	return &Chunk{
		chunkRepo:  chunkRepo,
		chunkStore: chunkStore,
		transfer:   transfer,
		now:        time.Now,
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

// StoreUploaded verifies and stores chunk content without recording metadata;
// the metadata row is written by ConfirmUploads once the client reports the
// transfer done. Content writes are safe to run concurrently.
func (c *Chunk) StoreUploaded(ctx context.Context, hash domain.Hash, data []byte) error {
	if got := chunker.Sum(data); got != hash {
		return domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", hash))
	}
	exists, err := c.chunkStore.Exists(ctx, hash)
	if err != nil {
		return domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", hash, err))
	}
	if exists {
		return nil
	}
	if err := c.chunkStore.Put(ctx, hash, data); err != nil {
		return domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
	}
	return nil
}

// Download returns the stored content for a chunk hash.
func (c *Chunk) Download(ctx context.Context, hash domain.Hash) ([]byte, error) {
	data, err := c.chunkStore.Get(ctx, hash)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// PresignUploadURLs signs one page of upload paths for refs. Chunks already
// present in the store get an empty URL with AlreadyStored set so clients can
// skip the transfer.
func (c *Chunk) PresignUploadURLs(ctx context.Context, org, project string, refs []ChunkRef, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	if err := validateChunkRefs(refs); err != nil {
		return nil, "", err
	}
	page, next, err := paginate(refs, pageSize, pageToken, c.transfer.MaxPageSize)
	if err != nil {
		return nil, "", err
	}
	expiry := chunkurl.Expiry(c.now(), c.transfer.PresignTTL)
	urls := make([]ChunkURL, 0, len(page))
	for _, ref := range page {
		exists, err := c.chunkStore.Exists(ctx, ref.Hash)
		if err != nil {
			return nil, "", domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", ref.Hash, err))
		}
		url := ""
		if !exists {
			url = chunkurl.UploadPath(c.transfer.SigningKey, org, project, ref.Hash.String(), ref.SizeBytes, expiry)
		}
		urls = append(urls, ChunkURL{Hash: ref.Hash, URL: url, AlreadyStored: exists})
	}
	return urls, next, nil
}

// PresignDownloadURLs signs one page of download paths for hashes the caller
// may read.
func (c *Chunk) PresignDownloadURLs(org, project string, hashes []domain.Hash, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	page, next, err := paginate(hashes, pageSize, pageToken, c.transfer.MaxPageSize)
	if err != nil {
		return nil, "", err
	}
	expiry := chunkurl.Expiry(c.now(), c.transfer.PresignTTL)
	urls := make([]ChunkURL, 0, len(page))
	for _, hash := range page {
		urls = append(urls, ChunkURL{
			Hash: hash,
			URL:  chunkurl.DownloadPath(c.transfer.SigningKey, org, project, hash.String(), expiry),
		})
	}
	return urls, next, nil
}

// ConfirmUploads verifies that uploaded chunk content landed in the store and
// records metadata rows for the present chunks. The recorded size is measured
// from the stored content, never taken from the client. Hashes still missing
// are returned so the client can retry them.
func (c *Chunk) ConfirmUploads(ctx context.Context, hashes []domain.Hash) ([]domain.Hash, error) {
	var missing []domain.Hash
	for _, hash := range hashes {
		size, err := c.chunkStore.Size(ctx, hash)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				missing = append(missing, hash)
				continue
			}
			return nil, domain.NewErrorDatabase(fmt.Sprintf("stat chunk %s: %v", hash, err))
		}
		if err := c.chunkRepo.InsertChunkIfNotExists(ctx, hash, size); err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// VerifyTransferURL checks that a signed chunk URL matches the project, hash
// and transfer parameters and has not expired.
func (c *Chunk) VerifyTransferURL(org, project, hash string, params chunkurl.Params) error {
	return chunkurl.Verify(c.transfer.SigningKey, org, project, hash, params, c.now())
}

func validateChunkRefs(refs []ChunkRef) error {
	for _, ref := range refs {
		if ref.SizeBytes <= 0 || ref.SizeBytes > chunker.DefaultConfig.Max {
			return domain.NewErrorUser("invalid chunk size")
		}
	}
	return nil
}

func paginate[T any](items []T, pageSize int, pageToken string, maxPageSize int) ([]T, string, error) {
	offset := 0
	if pageToken != "" {
		parsed, err := strconv.Atoi(pageToken)
		if err != nil || parsed < 0 {
			return nil, "", domain.NewErrorUser("invalid page token")
		}
		offset = parsed
	}
	if offset >= len(items) {
		return nil, "", nil
	}
	size := pageSize
	switch {
	case maxPageSize > 0 && (size <= 0 || size > maxPageSize):
		size = maxPageSize
	case size <= 0:
		size = len(items)
	}
	end := offset + size
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return items[offset:end], next, nil
}
