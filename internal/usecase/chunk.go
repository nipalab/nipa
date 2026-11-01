package usecase

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=chunk_mock_test.go -package=usecase
type chunkRepository interface {
	InsertChunkIfNotExists(ctx context.Context, hash domain.Hash, sizeBytes int64) error
	HasChunk(ctx context.Context, hash domain.Hash) (bool, error)
	LinkProjectChunk(ctx context.Context, projectID snow.ID, hash domain.Hash) error
	LinkProjectChunks(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error
	LinkedProjectChunks(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]bool, error)
}

// ChunkRef identifies a chunk and the exact size of its content.
type ChunkRef struct {
	Hash      domain.Hash
	SizeBytes int64
}

// ChunkURL pairs a chunk hash with its signed transfer path. For uploads, URL
// is empty when the content already exists and the project only has to prove it
// holds it; Headers are request headers the PUT must carry because they are
// part of a direct backend's signature.
type ChunkURL struct {
	Hash          domain.Hash
	URL           string
	AlreadyStored bool
	Headers       map[string]string
	// ProofChallenge is set for every chunk the project is not linked to yet;
	// ConfirmUploads must be given the matching proof.
	ProofChallenge string
}

// ChunkProof is a client's proof of possession for one chunk: Proof answers
// Challenge (issued by PresignUploadURLs) for the chunk's stored bytes.
type ChunkProof struct {
	Hash      domain.Hash
	Challenge string
	Proof     string
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
	ledger     StorageLedger
	metrics    *obs.Metrics
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

// WithStorageLedger attaches a storage ledger. Without one (the default), no
// usage is tracked and no quota is enforced.
func (c *Chunk) WithStorageLedger(ledger StorageLedger) *Chunk {
	c.ledger = ledger
	return c
}

// WithMetrics attaches observability metrics. Without one (the default), chunk
// operations are not counted.
func (c *Chunk) WithMetrics(metrics *obs.Metrics) *Chunk {
	c.metrics = metrics
	return c
}

// Upload stores one chunk. It returns true when the content was newly stored,
// false when an identical chunk was already present. The chunk metadata row is
// recorded only after content exists, so a Push that references this hash can
// rely on it.
func (c *Chunk) Upload(ctx context.Context, hash domain.Hash, data []byte) (bool, error) {
	if got := chunker.Sum(data); got != hash {
		c.metrics.ObserveChunkVerifyFailure("store")
		return false, domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", hash))
	}
	exists, err := c.chunkStore.Exists(ctx, hash)
	if err != nil {
		c.metrics.ObserveChunkOp("upload", "error")
		return false, domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", hash, err))
	}
	if exists {
		c.metrics.ObserveChunkOp("upload", "dedup")
		// The content may be present without a metadata row (a direct upload
		// whose confirm never ran); record it so callers that need the row
		// (merge attribution) do not fail on dedup.
		if err := c.chunkRepo.InsertChunkIfNotExists(ctx, hash, int64(len(data))); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := c.chunkStore.Put(ctx, hash, data); err != nil {
		c.metrics.ObserveChunkOp("upload", "error")
		return false, domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
	}
	c.metrics.ObserveChunkOp("upload", "stored")
	c.metrics.ObserveChunkBytes("upload", int64(len(data)))
	if err := c.chunkRepo.InsertChunkIfNotExists(ctx, hash, int64(len(data))); err != nil {
		return false, err
	}
	return true, nil
}

// AttributeProjectChunks links chunks the server produced itself (merge output)
// to the project and, when a ledger is attached, attributes their bytes.
func (c *Chunk) AttributeProjectChunks(ctx context.Context, projectID snow.ID, chunks []LedgerChunk) error {
	for _, chunk := range chunks {
		if err := c.chunkRepo.LinkProjectChunk(ctx, projectID, chunk.Hash); err != nil {
			return err
		}
	}
	if c.ledger == nil || len(chunks) == 0 {
		return nil
	}
	return c.ledger.AttributeProjectChunks(ctx, projectID, chunks)
}

// UploadForProject stores server-held content and links it to the project so a
// push of that project may reference it.
func (c *Chunk) UploadForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) (bool, error) {
	isNew, err := c.Upload(ctx, hash, data)
	if err != nil {
		return false, err
	}
	if err := c.chunkRepo.LinkProjectChunk(ctx, projectID, hash); err != nil {
		return false, err
	}
	return isNew, nil
}

// StoreUploaded verifies and stores chunk content without recording metadata;
// the metadata row is written by ConfirmUploads once the client reports the
// transfer done. Linking the project to the chunk also waits for confirm, where
// the client proves it holds the bytes, so this path never touches the database.
// Content writes are safe to run concurrently.
func (c *Chunk) StoreUploaded(ctx context.Context, hash domain.Hash, data []byte) (err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.store",
		attribute.String(obs.AttrChunkHash, hash.String()),
		attribute.Int(obs.AttrChunkSize, len(data)))
	defer func() { obs.FinishSpan(span, err) }()

	if got := chunker.Sum(data); got != hash {
		c.metrics.ObserveChunkVerifyFailure("store")
		return domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", hash))
	}
	exists, err := c.chunkStore.Exists(ctx, hash)
	if err != nil {
		c.metrics.ObserveChunkOp("put", "error")
		return domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", hash, err))
	}
	if exists {
		c.metrics.ObserveChunkOp("put", "dedup")
		return nil
	}
	if err := c.chunkStore.Put(ctx, hash, data); err != nil {
		c.metrics.ObserveChunkOp("put", "error")
		return domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
	}
	c.metrics.ObserveChunkOp("put", "stored")
	c.metrics.ObserveChunkBytes("put", int64(len(data)))
	return nil
}

// Download returns the stored content for a chunk hash.
func (c *Chunk) Download(ctx context.Context, hash domain.Hash) (data []byte, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.download",
		attribute.String(obs.AttrChunkHash, hash.String()))
	defer func() { obs.FinishSpan(span, err) }()

	data, err = c.chunkStore.Get(ctx, hash)
	if err != nil {
		c.metrics.ObserveChunkOp("get", "error")
		return nil, err
	}
	c.metrics.ObserveChunkOp("get", "ok")
	c.metrics.ObserveChunkBytes("get", int64(len(data)))
	return data, nil
}

// PresignUploadURLs signs one page of upload paths for refs. Chunks the
// project is already linked to get an empty URL with AlreadyStored set so
// clients can skip the transfer. Every other chunk gets a proof challenge. Only
// content that does not exist yet gets an upload target: a stored chunk is never
// written again, and a project that holds the bytes claims it with the proof
// instead, which links it without any transfer. When a ledger is attached, refs that would add new bytes
// are checked against the organization's remaining quota first.
func (c *Chunk) PresignUploadURLs(ctx context.Context, org *domain.Organization, project *domain.Project, refs []ChunkRef, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	ctx, span := obs.StartSpan(ctx, "chunk.presign_upload",
		attribute.String(obs.AttrOrg, org.Slug),
		attribute.String(obs.AttrProject, project.Slug),
		attribute.Int(obs.AttrChunkCount, len(refs)))
	urls, next, err := c.presignUploadURLs(ctx, org, project, refs, pageSize, pageToken)
	obs.FinishSpan(span, err)
	result := "ok"
	if err != nil {
		result = "error"
	}
	c.metrics.ObserveChunkOp("presign_upload", result)
	return urls, next, err
}

func (c *Chunk) presignUploadURLs(ctx context.Context, org *domain.Organization, project *domain.Project, refs []ChunkRef, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	if err := validateChunkRefs(refs); err != nil {
		return nil, "", err
	}
	if err := c.checkHeadroom(ctx, org, project, refs); err != nil {
		return nil, "", err
	}
	page, next, err := paginate(refs, pageSize, pageToken, c.transfer.MaxPageSize)
	if err != nil {
		return nil, "", err
	}
	expiry := chunkurl.Expiry(c.now(), c.transfer.PresignTTL)
	pageHashes := make([]domain.Hash, 0, len(page))
	for _, ref := range page {
		pageHashes = append(pageHashes, ref.Hash)
	}
	linked, err := c.chunkRepo.LinkedProjectChunks(ctx, project.ID, pageHashes)
	if err != nil {
		return nil, "", err
	}
	urls := make([]ChunkURL, 0, len(page))
	for _, ref := range page {
		exists, err := c.chunkStore.Exists(ctx, ref.Hash)
		if err != nil {
			return nil, "", domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", ref.Hash, err))
		}
		if exists && linked[ref.Hash] {
			urls = append(urls, ChunkURL{Hash: ref.Hash, AlreadyStored: true})
			continue
		}
		challenge, err := chunkproof.NewChallenge(c.transfer.SigningKey, project.ID, ref.Hash, c.now().Add(c.transfer.PresignTTL))
		if err != nil {
			return nil, "", domain.NewErrorInternalServer(err.Error())
		}
		entry := ChunkURL{Hash: ref.Hash, ProofChallenge: challenge}
		if !exists {
			target, err := c.uploadTarget(ctx, org, project, ref, expiry)
			if err != nil {
				return nil, "", err
			}
			entry.URL, entry.Headers = target.URL, target.Headers
		}
		urls = append(urls, entry)
	}
	return urls, next, nil
}

// checkHeadroom projects the bytes the unlinked refs would add and asks the
// ledger whether the organization still has room for them.
func (c *Chunk) checkHeadroom(ctx context.Context, org *domain.Organization, project *domain.Project, refs []ChunkRef) error {
	if c.ledger == nil {
		return nil
	}
	hashes := make([]domain.Hash, 0, len(refs))
	for _, ref := range refs {
		hashes = append(hashes, ref.Hash)
	}
	attributed, err := c.ledger.AttributedSizes(ctx, project.ID, hashes)
	if err != nil {
		return err
	}
	var projected int64
	for _, ref := range refs {
		if _, ok := attributed[ref.Hash]; ok {
			continue
		}
		projected += ref.SizeBytes
	}
	if projected == 0 {
		return nil
	}
	return c.ledger.Headroom(ctx, org, project, projected)
}

func (c *Chunk) uploadTarget(ctx context.Context, org *domain.Organization, project *domain.Project, ref ChunkRef, expiry int64) (storage.UploadTarget, error) {
	direct, ok := c.chunkStore.(storage.DirectTransferStore)
	if !ok {
		return storage.UploadTarget{
			URL: chunkurl.UploadPath(c.transfer.SigningKey, org.Slug, project.Slug, ref.Hash.String(), ref.SizeBytes, expiry),
		}, nil
	}
	target, err := direct.PresignUpload(ctx, ref.Hash, ref.SizeBytes, c.transfer.PresignTTL)
	if err != nil {
		return storage.UploadTarget{}, domain.NewErrorInternalServer(fmt.Sprintf("presign upload for chunk %s: %v", ref.Hash, err))
	}
	return target, nil
}

// PresignDownloadURLs signs one page of download paths for hashes the caller
// may read. Stores implementing storage.DirectTransferStore hand out absolute
// backend presigned URLs so clients fetch content directly from the backend.
func (c *Chunk) PresignDownloadURLs(ctx context.Context, org, project string, hashes []domain.Hash, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	urls, next, err := c.presignDownloadURLs(ctx, org, project, hashes, pageSize, pageToken)
	result := "ok"
	if err != nil {
		result = "error"
	}
	c.metrics.ObserveChunkOp("presign_download", result)
	return urls, next, err
}

func (c *Chunk) presignDownloadURLs(ctx context.Context, org, project string, hashes []domain.Hash, pageSize int, pageToken string) ([]ChunkURL, string, error) {
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
// Hashes still missing are returned so the client can retry them. When a
// ledger is attached, present chunks are attributed to the project and the
// organization's quota is enforced.
func (c *Chunk) ConfirmUploads(ctx context.Context, org *domain.Organization, project *domain.Project, hashes []domain.Hash, proofs []ChunkProof) ([]domain.Hash, error) {
	ctx, span := obs.StartSpan(ctx, "chunk.confirm",
		attribute.String(obs.AttrOrg, org.Slug),
		attribute.String(obs.AttrProject, project.Slug),
		attribute.Int(obs.AttrChunkCount, len(hashes)))
	missing, confirmedBytes, err := c.confirmUploads(ctx, org, project, hashes, proofs)
	if err == nil {
		span.SetAttributes(
			attribute.Int(obs.AttrChunkMissing, len(missing)),
			attribute.Int64(obs.AttrChunkBytes, confirmedBytes),
		)
	}
	obs.FinishSpan(span, err)
	result := "ok"
	switch {
	case err != nil:
		result = "error"
	case len(missing) > 0:
		result = "partial"
	}
	c.metrics.ObserveChunkOp("confirm", result)
	c.metrics.ObserveChunkBytes("confirm", confirmedBytes)
	return missing, err
}

func (c *Chunk) confirmUploads(ctx context.Context, org *domain.Organization, project *domain.Project, hashes []domain.Hash, proofs []ChunkProof) ([]domain.Hash, int64, error) {
	var missing []domain.Hash
	var attributed []LedgerChunk
	var confirmedBytes int64
	linked, err := c.chunkRepo.LinkedProjectChunks(ctx, project.ID, hashes)
	if err != nil {
		return nil, 0, err
	}
	var toLink []domain.Hash
	proofByHash := make(map[domain.Hash]ChunkProof, len(proofs))
	for _, proof := range proofs {
		proofByHash[proof.Hash] = proof
	}
	for _, hash := range hashes {
		var size int64
		var present bool
		if linked[hash] {
			size, err = c.chunkStore.Size(ctx, hash)
			switch {
			case err == nil:
				present = true
			case !domain.IsErrorNotFound(err):
				return nil, 0, domain.NewErrorDatabase(fmt.Sprintf("stat chunk %s: %v", hash, err))
			}
		} else {
			size, present, err = c.verifyPossession(ctx, project, hash, proofByHash[hash])
			if err != nil {
				return nil, 0, err
			}
			if present {
				toLink = append(toLink, hash)
			}
		}
		if !present {
			missing = append(missing, hash)
			continue
		}
		if err := c.chunkRepo.InsertChunkIfNotExists(ctx, hash, size); err != nil {
			return nil, 0, err
		}
		confirmedBytes += size
		if c.ledger != nil {
			attributed = append(attributed, LedgerChunk{Hash: hash, SizeBytes: size})
		}
	}
	if len(toLink) > 0 {
		if err := c.chunkRepo.LinkProjectChunks(ctx, project.ID, toLink); err != nil {
			return nil, 0, err
		}
	}
	if len(attributed) > 0 {
		if err := c.ledger.Attribute(ctx, org, project, attributed); err != nil {
			return nil, 0, err
		}
	}
	return missing, confirmedBytes, nil
}

// verifyPossession decides whether the caller may be linked to a chunk it is
// not linked to yet and returns the chunk's size. The challenge must be a live
// one issued for this project and hash, and the proof must equal
// BLAKE3(challenge || bytes) over bytes the server read itself. Direct uploads
// land on the shared key without passing through the server, so the bytes are
// also hashed: an object that does not match its key is deleted (it is garbage
// that would otherwise block the real content) and reported missing.
func (c *Chunk) verifyPossession(ctx context.Context, project *domain.Project, hash domain.Hash, proof ChunkProof) (int64, bool, error) {
	if proof.Challenge == "" || proof.Proof == "" {
		return 0, false, nil
	}
	if err := chunkproof.VerifyChallenge(c.transfer.SigningKey, project.ID, hash, proof.Challenge, c.now()); err != nil {
		return 0, false, nil
	}
	data, err := c.chunkStore.Get(ctx, hash)
	if domain.IsErrorNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, domain.NewErrorDatabase(fmt.Sprintf("read chunk %s: %v", hash, err))
	}
	if chunker.Sum(data) != hash {
		c.metrics.ObserveChunkVerifyFailure("confirm")
		return 0, false, c.dropInvalidChunk(ctx, hash)
	}
	return int64(len(data)), chunkproof.Equal(chunkproof.Proof(proof.Challenge, data), proof.Proof), nil
}

func (c *Chunk) dropInvalidChunk(ctx context.Context, hash domain.Hash) error {
	direct, ok := c.chunkStore.(storage.DirectTransferStore)
	if !ok {
		return nil
	}
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
