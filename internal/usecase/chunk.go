package usecase

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/storage"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=chunk_mock_test.go -package=usecase
type chunkRepository interface {
	InsertChunkIfNotExists(ctx context.Context, hash domain.Hash, sizeBytes int64) error
	HasChunk(ctx context.Context, hash domain.Hash) (bool, error)
}

// ChunkRef identifies a chunk and the exact size of its content.
type ChunkRef struct {
	Hash      domain.Hash
	SizeBytes int64
}

// ChunkURL pairs a chunk hash with its signed transfer path. Method and
// FormData describe direct backend transfers: an empty method means PUT,
// a POST method carries multipart form fields.
type ChunkURL struct {
	Hash          domain.Hash
	URL           string
	AlreadyStored bool
	Method        string
	FormData      map[string]string
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

	unverifiedMu sync.Mutex
	unverified   map[domain.Hash]time.Time
	lastPrune    time.Time
}

func NewChunk(chunkRepo chunkRepository, chunkStore storage.ChunkStore, transfer ChunkTransferConfig) *Chunk {
	return &Chunk{
		chunkRepo:  chunkRepo,
		chunkStore: chunkStore,
		transfer:   transfer,
		now:        time.Now,
		unverified: map[domain.Hash]time.Time{},
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
		if exists {
			urls = append(urls, ChunkURL{Hash: ref.Hash, AlreadyStored: true})
			continue
		}
		target, err := c.uploadTarget(ctx, org, project, ref, expiry)
		if err != nil {
			return nil, "", err
		}
		urls = append(urls, ChunkURL{
			Hash:     ref.Hash,
			URL:      target.URL,
			Method:   target.Method,
			FormData: target.FormData,
		})
	}
	return urls, next, nil
}

func (c *Chunk) uploadTarget(ctx context.Context, org, project string, ref ChunkRef, expiry int64) (storage.UploadTarget, error) {
	direct, ok := c.chunkStore.(storage.DirectTransferStore)
	if !ok {
		return storage.UploadTarget{
			URL: chunkurl.UploadPath(c.transfer.SigningKey, org, project, ref.Hash.String(), ref.SizeBytes, expiry),
		}, nil
	}
	target, err := direct.PresignUpload(ctx, ref.Hash, ref.SizeBytes, c.transfer.PresignTTL)
	if err != nil {
		return storage.UploadTarget{}, domain.NewErrorInternalServer(fmt.Sprintf("presign upload for chunk %s: %v", ref.Hash, err))
	}
	c.markUnverified(ref.Hash)
	return target, nil
}

// PresignDownloadURLs signs one page of download paths for hashes the caller
// may read. Stores implementing storage.DirectTransferStore hand out absolute
// backend presigned URLs so clients fetch content directly from the backend.
func (c *Chunk) PresignDownloadURLs(ctx context.Context, org, project string, hashes []domain.Hash, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	page, next, err := paginate(hashes, pageSize, pageToken, c.transfer.MaxPageSize)
	if err != nil {
		return nil, "", err
	}
	expiry := chunkurl.Expiry(c.now(), c.transfer.PresignTTL)
	urls := make([]ChunkURL, 0, len(page))
	for _, hash := range page {
		path, err := c.downloadPath(ctx, org, project, hash, expiry)
		if err != nil {
			return nil, "", err
		}
		urls = append(urls, ChunkURL{Hash: hash, URL: path})
	}
	return urls, next, nil
}

func (c *Chunk) downloadPath(ctx context.Context, org, project string, hash domain.Hash, expiry int64) (string, error) {
	direct, ok := c.chunkStore.(storage.DirectTransferStore)
	if !ok {
		return chunkurl.DownloadPath(c.transfer.SigningKey, org, project, hash.String(), expiry), nil
	}
	if c.isUnverified(hash) {
		verified, err := c.verifyUnverifiedObject(ctx, direct, hash)
		if err != nil {
			return "", err
		}
		if !verified {
			c.clearUnverified(hash)
			return "", domain.NewErrorInternalServer(fmt.Sprintf("chunk %s failed verification", hash))
		}
		c.clearUnverified(hash)
	}
	url, err := direct.PresignDownload(ctx, hash, c.transfer.PresignTTL)
	if err != nil {
		return "", domain.NewErrorInternalServer(fmt.Sprintf("presign download for chunk %s: %v", hash, err))
	}
	return url, nil
}

// ConfirmUploads verifies that uploaded chunk content landed in the store and
// records metadata rows for the present chunks. The recorded size is measured
// from the stored content, never taken from the client. Content written
// through a direct upload target is always hash-verified because its bytes
// never passed through the server; untouched chunks are trusted once recorded.
// Hashes still missing are returned so the client can retry them.
func (c *Chunk) ConfirmUploads(ctx context.Context, hashes []domain.Hash) ([]domain.Hash, error) {
	direct, isDirect := c.chunkStore.(storage.DirectTransferStore)
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
		if isDirect {
			verify, err := c.needsVerification(ctx, hash)
			if err != nil {
				return nil, err
			}
			if verify {
				verified, err := c.verifyChunkContent(ctx, direct, hash, size)
				if err != nil {
					return nil, err
				}
				c.clearUnverified(hash)
				if !verified {
					missing = append(missing, hash)
					continue
				}
			}
		}
		if err := c.chunkRepo.InsertChunkIfNotExists(ctx, hash, size); err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// needsVerification reports whether a chunk stored by a direct backend must be
// hash-verified: content a direct upload target was issued for always must be,
// since the object may have been rewritten or re-uploaded after it was
// recorded, and untouched chunks only when no metadata row exists yet.
func (c *Chunk) needsVerification(ctx context.Context, hash domain.Hash) (bool, error) {
	if c.isUnverified(hash) {
		return true, nil
	}
	recorded, err := c.chunkRepo.HasChunk(ctx, hash)
	if err != nil {
		return false, err
	}
	return !recorded, nil
}

// verifyUnverifiedObject reads and verifies the content behind a hash whose
// object a direct upload target was issued for.
func (c *Chunk) verifyUnverifiedObject(ctx context.Context, direct storage.DirectTransferStore, hash domain.Hash) (bool, error) {
	size, err := c.chunkStore.Size(ctx, hash)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return false, nil
		}
		return false, domain.NewErrorDatabase(fmt.Sprintf("stat chunk %s: %v", hash, err))
	}
	return c.verifyChunkContent(ctx, direct, hash, size)
}

// unverifiedPruneInterval bounds how often the unverified set is swept for
// expired entries, keeping marks and lookups O(1) amortized on large pushes.
const unverifiedPruneInterval = time.Minute

// markUnverified records that a direct upload target was issued for hash. The
// mark is dropped once the object is verified, or when the target can no
// longer be used (presign TTL). It is kept in memory: a restart without the
// mark falls back to the metadata rule, and the target itself expires.
func (c *Chunk) markUnverified(hash domain.Hash) {
	c.unverifiedMu.Lock()
	defer c.unverifiedMu.Unlock()
	now := c.now()
	if now.Sub(c.lastPrune) >= unverifiedPruneInterval {
		c.lastPrune = now
		c.pruneUnverifiedLocked(now)
	}
	c.unverified[hash] = now.Add(c.transfer.PresignTTL)
}

func (c *Chunk) isUnverified(hash domain.Hash) bool {
	c.unverifiedMu.Lock()
	defer c.unverifiedMu.Unlock()
	expiry, ok := c.unverified[hash]
	if !ok {
		return false
	}
	if c.now().After(expiry) {
		delete(c.unverified, hash)
		return false
	}
	return true
}

func (c *Chunk) clearUnverified(hash domain.Hash) {
	c.unverifiedMu.Lock()
	defer c.unverifiedMu.Unlock()
	delete(c.unverified, hash)
}

func (c *Chunk) pruneUnverifiedLocked(now time.Time) {
	for hash, expiry := range c.unverified {
		if now.After(expiry) {
			delete(c.unverified, hash)
		}
	}
}

// verifyChunkContent checks one object that may have been written by a direct
// upload: oversized objects are dropped without being read, and content must
// hash to its key.
func (c *Chunk) verifyChunkContent(ctx context.Context, direct storage.DirectTransferStore, hash domain.Hash, size int64) (bool, error) {
	if size > chunker.MaxChunkSize() {
		return false, c.dropInvalidChunk(ctx, direct, hash)
	}
	data, err := c.chunkStore.Get(ctx, hash)
	if err != nil {
		if domain.IsErrorNotFound(err) {
			return false, nil
		}
		return false, domain.NewErrorDatabase(fmt.Sprintf("read chunk %s: %v", hash, err))
	}
	if chunker.Sum(data) == hash {
		return true, nil
	}
	return false, c.dropInvalidChunk(ctx, direct, hash)
}

func (c *Chunk) dropInvalidChunk(ctx context.Context, direct storage.DirectTransferStore, hash domain.Hash) error {
	if err := direct.DeleteChunk(ctx, hash); err != nil {
		return domain.NewErrorInternalServer(fmt.Sprintf("delete invalid chunk %s: %v", hash, err))
	}
	return nil
}

// VerifyTransferURL checks that a signed chunk URL matches the project, hash
// and transfer parameters and has not expired.
func (c *Chunk) VerifyTransferURL(org, project, hash string, params chunkurl.Params) error {
	return chunkurl.Verify(c.transfer.SigningKey, org, project, hash, params, c.now())
}

func validateChunkRefs(refs []ChunkRef) error {
	for _, ref := range refs {
		if ref.SizeBytes <= 0 || ref.SizeBytes > chunker.MaxChunkSize() {
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
