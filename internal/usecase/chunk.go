package usecase

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

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
	// InsertChunksIfNotExist records many metadata rows in batched
	// statements; existing rows are left alone.
	InsertChunksIfNotExist(ctx context.Context, chunks []ChunkRef) error
	HasChunk(ctx context.Context, hash domain.Hash) (bool, error)
	// KnownChunks returns the hashes that have a metadata row, whatever
	// project they are linked to.
	KnownChunks(ctx context.Context, hashes []domain.Hash) (map[domain.Hash]struct{}, error)
	LinkProjectChunk(ctx context.Context, projectID snow.ID, hash domain.Hash) error
	LinkProjectChunks(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error
	LinkedProjectChunkSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error)
}

// DefaultChunkVerifyConcurrency is the server-wide number of chunk read-backs
// confirm runs at once when CHUNK_VERIFY_CONCURRENCY is unset.
const DefaultChunkVerifyConcurrency = 64

// DefaultChunkProxyBufferBytes is the server-wide memory budget for proxied
// upload bodies when none is configured (CHUNK_PROXY_BUFFER_MIB).
const DefaultChunkProxyBufferBytes = 128 << 20

// UploadMode selects how clients send chunk bytes to a store that supports
// direct transfers (CHUNK_UPLOAD_MODE).
type UploadMode string

const (
	// UploadModeDirect hands out the store's own presigned upload URLs; the
	// bytes bypass the server and confirm reads every chunk back.
	UploadModeDirect UploadMode = "direct"
	// UploadModeProxy hands out signed server paths: the server hashes the
	// bytes on the way in, writes the store itself and returns a receipt that
	// lets confirm skip the read-back. Own-storage (External) projects keep
	// uploading direct so customer bytes never flow through the server.
	UploadModeProxy UploadMode = "proxy"
)

// presignExistsLimit is the server-wide number of store existence checks
// presign runs at once.
var presignExistsLimit int64 = 64

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
// Receipt, when set, is what StoreUploaded issued for a proxied upload of the
// chunk; a valid one stands in for the read-back.
type ChunkProof struct {
	Hash      domain.Hash
	Challenge string
	Proof     string
	Receipt   string
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
	chunkRepo chunkRepository
	stores    ChunkStores
	transfer  ChunkTransferConfig
	ledger    StorageLedger
	gcGuard   ChunkGCGuard
	metrics   *obs.Metrics
	now       func() time.Time
	// verifySlots bounds the chunk read-backs of every confirm in flight and
	// existsSlots the existence checks of every presign: both are shared by
	// all requests, so store I/O and memory stay bounded however many arrive.
	verifySlots *semaphore.Weighted
	existsSlots *semaphore.Weighted
	uploadMode  UploadMode
	// proxyBuffer bounds the bytes of every proxied upload body held in
	// memory at once, across all requests; proxyBufferSize is its capacity.
	proxyBuffer     *semaphore.Weighted
	proxyBufferSize int64
}

// ChunkGCGuard keeps a chunk garbage collector from deleting content a caller
// is about to rely on. Protect runs before the content is checked and before
// any link to it is recorded: it clears the chunks' garbage marks, waiting for
// a sweep that holds them, so content the sweep removed is seen as missing
// (and uploaded again) instead of being linked. Nil (the free default) runs no
// collector.
type ChunkGCGuard interface {
	Protect(ctx context.Context, hashes []domain.Hash) error
}

func NewChunk(chunkRepo chunkRepository, chunkStore storage.ChunkStore, transfer ChunkTransferConfig) *Chunk {
	return &Chunk{
		chunkRepo: chunkRepo,
		stores:    StaticStores(chunkStore),
		transfer:  transfer,
		now:       time.Now,

		verifySlots: semaphore.NewWeighted(DefaultChunkVerifyConcurrency),
		existsSlots: semaphore.NewWeighted(presignExistsLimit),
		uploadMode:  UploadModeDirect,

		proxyBuffer:     semaphore.NewWeighted(DefaultChunkProxyBufferBytes),
		proxyBufferSize: DefaultChunkProxyBufferBytes,
	}
}

// WithUploadMode selects direct or proxied uploads for projects on the
// server's own store; an unknown mode keeps UploadModeDirect.
func (c *Chunk) WithUploadMode(mode UploadMode) *Chunk {
	if mode != UploadModeProxy {
		mode = UploadModeDirect
	}
	c.uploadMode = mode
	return c
}

// WithProxyBuffer sets the server-wide byte budget for proxied upload bodies
// held in memory; n <= 0 keeps DefaultChunkProxyBufferBytes.
func (c *Chunk) WithProxyBuffer(n int64) *Chunk {
	if n <= 0 {
		n = DefaultChunkProxyBufferBytes
	}
	c.proxyBuffer = semaphore.NewWeighted(n)
	c.proxyBufferSize = n
	return c
}

// ReserveUploadBuffer takes size bytes of the proxied upload budget before
// the caller reads an upload body, waiting while other uploads hold it. A body
// larger than the whole budget takes all of it. It fails when ctx ends first.
// The caller must call release once the body is stored or dropped.
func (c *Chunk) ReserveUploadBuffer(ctx context.Context, size int64) (release func(), err error) {
	n := min(max(size, 1), c.proxyBufferSize)
	if err := c.proxyBuffer.Acquire(ctx, n); err != nil {
		return nil, domain.NewErrorServiceUnavailable("chunk upload cancelled while waiting for buffer space")
	}
	var once sync.Once
	return func() { once.Do(func() { c.proxyBuffer.Release(n) }) }, nil
}

// WithVerifyLimit sets how many chunk read-backs all confirms together run at
// once; n <= 0 keeps DefaultChunkVerifyConcurrency.
func (c *Chunk) WithVerifyLimit(n int) *Chunk {
	if n <= 0 {
		n = DefaultChunkVerifyConcurrency
	}
	c.verifySlots = semaphore.NewWeighted(int64(n))
	return c
}

// WithChunkStores serves each project from the store the resolver picks
// instead of the single store given to NewChunk.
func (c *Chunk) WithChunkStores(stores ChunkStores) *Chunk {
	c.stores = stores
	return c
}

// ChunkStores returns the resolver chunk content goes through.
func (c *Chunk) ChunkStores() ChunkStores {
	return c.stores
}

// RequireWritable refuses projects whose store is read-only.
func (c *Chunk) RequireWritable(ctx context.Context, projectID snow.ID) error {
	_, err := c.writableStore(ctx, projectID)
	return err
}

func (c *Chunk) writableStore(ctx context.Context, projectID snow.ID) (ProjectStore, error) {
	resolved, err := c.stores.ForProject(ctx, projectID)
	if err != nil {
		return ProjectStore{}, err
	}
	if resolved.ReadOnly {
		return ProjectStore{}, ErrStorageReadOnly()
	}
	return resolved, nil
}

// CheckUploadHeadroom asks the storage ledger whether the project's owner has
// room for bytes new bytes. It reports false when no quota applies to the
// project (no ledger, or an external store).
func (c *Chunk) CheckUploadHeadroom(ctx context.Context, org *domain.Organization, project *domain.Project, bytes int64) (bool, error) {
	resolved, err := c.writableStore(ctx, project.ID)
	if err != nil {
		return false, err
	}
	ledger := c.ledgerFor(resolved)
	if ledger == nil {
		return false, nil
	}
	if bytes <= 0 {
		return true, nil
	}
	return true, ledger.Headroom(ctx, org, project, bytes)
}

func (c *Chunk) ledgerFor(resolved ProjectStore) StorageLedger {
	if resolved.External {
		return nil
	}
	return c.ledger
}

// WithStorageLedger attaches a storage ledger. Without one (the default), no
// usage is tracked and no quota is enforced.
func (c *Chunk) WithStorageLedger(ledger StorageLedger) *Chunk {
	c.ledger = ledger
	return c
}

// WithGCGuard attaches the chunk garbage collector's guard.
func (c *Chunk) WithGCGuard(guard ChunkGCGuard) *Chunk {
	c.gcGuard = guard
	return c
}

func (c *Chunk) protect(ctx context.Context, hashes ...domain.Hash) error {
	if c.gcGuard == nil || len(hashes) == 0 {
		return nil
	}
	return c.gcGuard.Protect(ctx, hashes)
}

// WithMetrics attaches observability metrics. Without one (the default), chunk
// operations are not counted.
func (c *Chunk) WithMetrics(metrics *obs.Metrics) *Chunk {
	c.metrics = metrics
	return c
}

// upload stores one chunk. It returns true when the content was newly stored,
// false when an identical chunk was already present. The chunk metadata row is
// recorded only after content exists, so a Push that references this hash can
// rely on it.
func (c *Chunk) upload(ctx context.Context, store storage.ChunkStore, hash domain.Hash, data []byte) (bool, error) {
	if got := chunker.Sum(data); got != hash {
		c.metrics.ObserveChunkVerifyFailure("store")
		return false, domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", hash))
	}
	if err := c.protect(ctx, hash); err != nil {
		return false, err
	}
	exists, err := store.Exists(ctx, hash)
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
	if err := store.Put(ctx, hash, data); err != nil {
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
// to the project. The ledger attributes first so a quota failure leaves no link.
func (c *Chunk) AttributeProjectChunks(ctx context.Context, projectID snow.ID, chunks []LedgerChunk) error {
	resolved, err := c.stores.ForProject(ctx, projectID)
	if err != nil {
		return err
	}
	if ledger := c.ledgerFor(resolved); ledger != nil && len(chunks) > 0 {
		if err := ledger.AttributeProjectChunks(ctx, projectID, chunks); err != nil {
			return err
		}
	}
	for _, chunk := range chunks {
		if err := c.chunkRepo.LinkProjectChunk(ctx, projectID, chunk.Hash); err != nil {
			return err
		}
	}
	return nil
}

// StoreForProject stores server-held content in the project's store and
// records its metadata row without linking it; AttributeProjectChunks links it
// once the caller is ready to land it.
func (c *Chunk) StoreForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) (bool, error) {
	resolved, err := c.writableStore(ctx, projectID)
	if err != nil {
		return false, err
	}
	return c.upload(ctx, resolved.Store, hash, data)
}

// UploadForProject stores server-held content in the project's store and links
// it to the project so a push of that project may reference it.
func (c *Chunk) UploadForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) (bool, error) {
	isNew, err := c.StoreForProject(ctx, projectID, hash, data)
	if err != nil {
		return false, err
	}
	if err := c.chunkRepo.LinkProjectChunk(ctx, projectID, hash); err != nil {
		return false, err
	}
	return isNew, nil
}

// StoreUploaded verifies and stores chunk content a client sent through a
// signed server path, without recording metadata; the metadata row is written
// by ConfirmUploads once the client reports the transfer done. Linking the
// project to the chunk also waits for confirm (quota is enforced there), so
// this path records no rows or links. Content writes are safe to run
// concurrently.
//
// It returns a receipt (chunkproof.NewReceipt) for the project, hash and the
// size the server measured, valid for the presign TTL: the server saw these
// bytes itself, so confirm accepts the receipt instead of reading them back.
//
// A store clients can also write directly (storage.ChunkOverwriter, i.e.
// S3 in proxy mode) is written unconditionally, which replaces garbage an
// earlier direct upload left at the key; other stores skip content they
// already hold. A chunk garbage collector's mark on the hash is cleared
// before the write, so a sweep cannot remove the content between this write
// and the confirm that relies on the receipt.
func (c *Chunk) StoreUploaded(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) (receipt string, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.store",
		attribute.String(obs.AttrChunkHash, hash.String()),
		attribute.Int(obs.AttrChunkSize, len(data)))
	defer func() { obs.FinishSpan(span, err) }()

	if got := chunker.Sum(data); got != hash {
		c.metrics.ObserveChunkVerifyFailure("store")
		return "", domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", hash))
	}
	resolved, err := c.writableStore(ctx, projectID)
	if err != nil {
		return "", err
	}
	if err := c.protect(ctx, hash); err != nil {
		return "", err
	}
	if err := c.writeUploaded(ctx, resolved.Store, hash, data); err != nil {
		return "", err
	}
	size := int64(len(data))
	return chunkproof.NewReceipt(c.transfer.SigningKey, projectID, hash, size, c.now().Add(c.transfer.PresignTTL)), nil
}

func (c *Chunk) writeUploaded(ctx context.Context, store storage.ChunkStore, hash domain.Hash, data []byte) error {
	if overwriter, ok := store.(storage.ChunkOverwriter); ok {
		if err := overwriter.Overwrite(ctx, hash, data); err != nil {
			c.metrics.ObserveChunkOp("put", "error")
			return domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
		}
		c.metrics.ObserveChunkOp("put", "stored")
		c.metrics.ObserveChunkBytes("put", int64(len(data)))
		return nil
	}
	exists, err := store.Exists(ctx, hash)
	if err != nil {
		c.metrics.ObserveChunkOp("put", "error")
		return domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", hash, err))
	}
	if exists {
		c.metrics.ObserveChunkOp("put", "dedup")
		return nil
	}
	if err := store.Put(ctx, hash, data); err != nil {
		c.metrics.ObserveChunkOp("put", "error")
		return domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
	}
	c.metrics.ObserveChunkOp("put", "stored")
	c.metrics.ObserveChunkBytes("put", int64(len(data)))
	return nil
}

// Download returns the stored content for a chunk hash.
func (c *Chunk) Download(ctx context.Context, projectID snow.ID, hash domain.Hash) (data []byte, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.download",
		attribute.String(obs.AttrChunkHash, hash.String()))
	defer func() { obs.FinishSpan(span, err) }()

	resolved, err := c.stores.ForProject(ctx, projectID)
	if err != nil {
		c.metrics.ObserveChunkOp("get", "error")
		return nil, err
	}
	data, err = resolved.Store.Get(ctx, hash)
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
// instead, which links it without any transfer. Existence is checked in the
// store only for hashes with a chunk metadata row (see existsKnown). When a
// ledger is attached, refs that would add new bytes are checked against the
// organization's remaining quota first.
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
	resolved, err := c.writableStore(ctx, project.ID)
	if err != nil {
		return nil, "", err
	}
	if err := c.checkHeadroom(ctx, c.ledgerFor(resolved), org, project, refs); err != nil {
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
	linked, err := c.chunkRepo.LinkedProjectChunkSizes(ctx, project.ID, pageHashes)
	if err != nil {
		return nil, "", err
	}
	known, err := c.chunkRepo.KnownChunks(ctx, pageHashes)
	if err != nil {
		return nil, "", err
	}
	exists, err := c.existsKnown(ctx, resolved.Store, page, known)
	if err != nil {
		return nil, "", err
	}
	urls := make([]ChunkURL, 0, len(page))
	for i, ref := range page {
		if _, ok := linked[ref.Hash]; ok && exists[i] {
			urls = append(urls, ChunkURL{Hash: ref.Hash, AlreadyStored: true})
			continue
		}
		challenge, err := chunkproof.NewChallenge(c.transfer.SigningKey, project.ID, ref.Hash, c.now().Add(c.transfer.PresignTTL))
		if err != nil {
			return nil, "", domain.NewErrorInternalServer(err.Error())
		}
		entry := ChunkURL{Hash: ref.Hash, ProofChallenge: challenge}
		if !exists[i] {
			target, err := c.uploadTarget(ctx, resolved, org, project, ref, expiry)
			if err != nil {
				return nil, "", err
			}
			entry.URL, entry.Headers = target.URL, target.Headers
		}
		urls = append(urls, entry)
	}
	return urls, next, nil
}

// existsKnown checks the store only for refs that have a chunk metadata row:
// content the server never confirmed is treated as absent and gets an upload
// target without a HEAD. An object that does sit at such a key (an upload
// whose confirm never ran, or garbage) makes the create-only PUT answer 412,
// and confirm then reads it back: valid content is linked through the proof,
// garbage is deleted and uploaded again. A row only hints "maybe present";
// refs with one are still checked, which finds lost objects. The checks of all
// presigns share existsSlots.
func (c *Chunk) existsKnown(ctx context.Context, store storage.ChunkStore, page []ChunkRef, known map[domain.Hash]struct{}) ([]bool, error) {
	exists := make([]bool, len(page))
	isKnown := func(i int) bool {
		_, ok := known[page[i].Hash]
		return ok
	}
	err := fanOut(ctx, c.existsSlots, len(page), isKnown, func(ctx context.Context, i int) error {
		ok, err := store.Exists(ctx, page[i].Hash)
		if err != nil {
			return domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", page[i].Hash, err))
		}
		exists[i] = ok
		return nil
	})
	if err != nil {
		return nil, err
	}
	return exists, nil
}

// fanOut runs fn for every index in [0, n) that wants it, each call holding
// one slot of the shared semaphore, and returns the first error. It stops
// starting calls once one failed or ctx ended.
func fanOut(ctx context.Context, slots *semaphore.Weighted, n int, wants func(i int) bool, fn func(ctx context.Context, i int) error) error {
	group, groupCtx := errgroup.WithContext(ctx)
	var acquireErr error
	for i := 0; i < n; i++ {
		if !wants(i) {
			continue
		}
		if err := slots.Acquire(groupCtx, 1); err != nil {
			acquireErr = err
			break
		}
		group.Go(func() error {
			defer slots.Release(1)
			return fn(groupCtx, i)
		})
	}
	if err := group.Wait(); err != nil {
		return err
	}
	return acquireErr
}

// checkHeadroom projects the bytes the unlinked refs would add and asks the
// ledger whether the organization still has room for them.
func (c *Chunk) checkHeadroom(ctx context.Context, ledger StorageLedger, org *domain.Organization, project *domain.Project, refs []ChunkRef) error {
	if ledger == nil {
		return nil
	}
	hashes := make([]domain.Hash, 0, len(refs))
	for _, ref := range refs {
		hashes = append(hashes, ref.Hash)
	}
	attributed, err := ledger.AttributedSizes(ctx, project.ID, hashes)
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
	return ledger.Headroom(ctx, org, project, projected)
}

// uploadTarget picks where a client PUTs one chunk: the store's own presigned
// URL for a direct-transfer store, or the signed server path (StoreUploaded)
// for any other store and, in proxy mode, for the server's own store too.
// Own-storage (External) projects always upload direct.
func (c *Chunk) uploadTarget(ctx context.Context, resolved ProjectStore, org *domain.Organization, project *domain.Project, ref ChunkRef, expiry int64) (storage.UploadTarget, error) {
	direct, ok := resolved.Store.(storage.DirectTransferStore)
	if !ok || (c.uploadMode == UploadModeProxy && !resolved.External) {
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
func (c *Chunk) PresignDownloadURLs(ctx context.Context, org *domain.Organization, project *domain.Project, hashes []domain.Hash, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	urls, next, err := c.presignDownloadURLs(ctx, org, project, hashes, pageSize, pageToken)
	result := "ok"
	if err != nil {
		result = "error"
	}
	c.metrics.ObserveChunkOp("presign_download", result)
	return urls, next, err
}

func (c *Chunk) presignDownloadURLs(ctx context.Context, org *domain.Organization, project *domain.Project, hashes []domain.Hash, pageSize int, pageToken string) ([]ChunkURL, string, error) {
	page, next, err := paginate(hashes, pageSize, pageToken, c.transfer.MaxPageSize)
	if err != nil {
		return nil, "", err
	}
	resolved, err := c.stores.ForProject(ctx, project.ID)
	if err != nil {
		return nil, "", err
	}
	expiry := chunkurl.Expiry(c.now(), c.transfer.PresignTTL)
	urls := make([]ChunkURL, 0, len(page))
	for _, hash := range page {
		path, err := c.downloadPath(ctx, resolved.Store, org.Slug, project.Slug, hash, expiry)
		if err != nil {
			return nil, "", err
		}
		urls = append(urls, ChunkURL{Hash: hash, URL: path})
	}
	return urls, next, nil
}

func (c *Chunk) downloadPath(ctx context.Context, store storage.ChunkStore, org, project string, hash domain.Hash, expiry int64) (string, error) {
	direct, ok := store.(storage.DirectTransferStore)
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
// records metadata rows for the present chunks (one batched insert, before the
// ledger and the links). Read-backs stream the content and share one
// server-wide limit (WithVerifyLimit). The recorded size is measured
// from the stored content, never taken from the client. A hash the project is
// not linked to yet, or one the client supplies a proof for, is read back,
// hashed and checked against the proof, because direct uploads never pass
// through the server; linked chunks without a proof are trusted once linked.
// A proof carrying a valid receipt from StoreUploaded (proxied upload, same
// project and hash, not expired) skips the read-back: the server hashed those
// bytes itself on the way in. Receipts never skip the rows, the ledger or the
// quota. Hashes still missing are returned so the client can retry them. When a
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
	resolved, err := c.writableStore(ctx, project.ID)
	if err != nil {
		return nil, 0, err
	}
	ledger := c.ledgerFor(resolved)
	if err := c.protect(ctx, hashes...); err != nil {
		return nil, 0, err
	}
	linked, err := c.chunkRepo.LinkedProjectChunkSizes(ctx, project.ID, hashes)
	if err != nil {
		return nil, 0, err
	}
	var toLink []domain.Hash
	proofByHash := make(map[domain.Hash]ChunkProof, len(proofs))
	for _, proof := range proofs {
		proofByHash[proof.Hash] = proof
	}
	type possession struct {
		size    int64
		present bool
	}
	// receipted is filled before the read-backs start and only read after;
	// checks is written by them under checkMu.
	receipted := make(map[domain.Hash]possession)
	checks := make(map[domain.Hash]possession)
	// A valid receipt means the server received and stored these bytes for
	// this project itself (proxied upload): present with the size it records,
	// no read-back. An invalid or expired one falls through to the proof.
	for _, hash := range hashes {
		proof, supplied := proofByHash[hash]
		if !supplied || proof.Receipt == "" {
			continue
		}
		size, err := chunkproof.VerifyReceipt(c.transfer.SigningKey, project.ID, hash, proof.Receipt, c.now())
		if err != nil {
			continue
		}
		receipted[hash] = possession{size: size, present: true}
	}
	var checkMu sync.Mutex
	needsCheck := func(i int) bool {
		if _, ok := receipted[hashes[i]]; ok {
			return false
		}
		_, isLinked := linked[hashes[i]]
		_, supplied := proofByHash[hashes[i]]
		return !isLinked || supplied
	}
	err = fanOut(ctx, c.verifySlots, len(hashes), needsCheck, func(ctx context.Context, i int) error {
		hash := hashes[i]
		size, present, err := c.verifyPossession(ctx, resolved.Store, project, hash, proofByHash[hash])
		if err != nil {
			return err
		}
		checkMu.Lock()
		checks[hash] = possession{size: size, present: present}
		checkMu.Unlock()
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	var rows []ChunkRef
	for _, hash := range hashes {
		linkedSize, isLinked := linked[hash]
		_, supplied := proofByHash[hash]
		if isLinked && !supplied {
			confirmedBytes += linkedSize
			if ledger != nil {
				attributed = append(attributed, LedgerChunk{Hash: hash, SizeBytes: linkedSize})
			}
			continue
		}
		check, ok := receipted[hash]
		if !ok {
			check = checks[hash]
		}
		if check.present && !isLinked {
			toLink = append(toLink, hash)
		}
		if !check.present {
			missing = append(missing, hash)
			continue
		}
		rows = append(rows, ChunkRef{Hash: hash, SizeBytes: check.size})
		confirmedBytes += check.size
		if ledger != nil {
			attributed = append(attributed, LedgerChunk{Hash: hash, SizeBytes: check.size})
		}
	}
	// Rows first: the ledger and the links resolve chunks through them. A
	// quota failure below still leaves the project unlinked; rows without a
	// link are harmless.
	if len(rows) > 0 {
		if err := c.chunkRepo.InsertChunksIfNotExist(ctx, rows); err != nil {
			return nil, 0, err
		}
	}
	if len(attributed) > 0 {
		if err := ledger.Attribute(ctx, org, project, attributed); err != nil {
			return nil, 0, err
		}
	}
	if len(toLink) > 0 {
		if err := c.chunkRepo.LinkProjectChunks(ctx, project.ID, toLink); err != nil {
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
func (c *Chunk) verifyPossession(ctx context.Context, store storage.ChunkStore, project *domain.Project, hash domain.Hash, proof ChunkProof) (int64, bool, error) {
	if proof.Challenge == "" || proof.Proof == "" {
		return 0, false, nil
	}
	if err := chunkproof.VerifyChallenge(c.transfer.SigningKey, project.ID, hash, proof.Challenge, c.now()); err != nil {
		return 0, false, nil
	}
	verifier := chunkproof.NewVerifier(proof.Challenge)
	err := readChunk(ctx, store, hash, verifier)
	if domain.IsErrorNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, domain.NewErrorDatabase(fmt.Sprintf("read chunk %s: %v", hash, err))
	}
	// A legitimate chunk is never larger than the maximum: presign rejects
	// larger refs and direct uploads pin the signed length. readChunk stops
	// one byte past it, so a larger object fails here without being read whole.
	if verifier.Size() > chunker.MaxChunkSize() || verifier.Sum() != hash {
		c.metrics.ObserveChunkVerifyFailure("confirm")
		return 0, false, c.dropInvalidChunk(ctx, store, hash)
	}
	return verifier.Size(), chunkproof.Equal(verifier.Proof(), proof.Proof), nil
}

// readChunk streams the stored chunk into w, at most one byte more than the
// largest legal chunk. Stores implementing storage.ChunkOpener are streamed;
// others are read whole with Get. A missing chunk is a domain NotFound error.
func readChunk(ctx context.Context, store storage.ChunkStore, hash domain.Hash, w io.Writer) error {
	limit := chunker.MaxChunkSize() + 1
	opener, ok := store.(storage.ChunkOpener)
	if !ok {
		data, err := store.Get(ctx, hash)
		if err != nil {
			return err
		}
		_, _ = w.Write(data[:min(int64(len(data)), limit)])
		return nil
	}
	r, err := opener.Open(ctx, hash)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	_, err = io.Copy(w, io.LimitReader(r, limit))
	return err
}

func (c *Chunk) dropInvalidChunk(ctx context.Context, store storage.ChunkStore, hash domain.Hash) error {
	direct, ok := store.(storage.DirectTransferStore)
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
