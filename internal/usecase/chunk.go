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
	// InsertChunksIfNotExist records the project's chunk rows (with their
	// upload markers) in batched statements; existing rows are left alone. A
	// chunk row is the project's link to the content: a push may reference
	// exactly the hashes the project has a row for.
	InsertChunksIfNotExist(ctx context.Context, projectID snow.ID, chunks []ChunkRef) error
	// ProjectChunkSizes returns the stored size of every hash the project has
	// a chunk row for; other hashes are absent.
	ProjectChunkSizes(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]int64, error)
}

// chunkTransactor runs a function inside one database transaction; the chunk
// repository and the storage ledger join it through the context.
type chunkTransactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
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
// is empty only when AlreadyStored is set; Headers are request headers the PUT
// must carry because they are part of a direct backend's signature.
type ChunkURL struct {
	Hash          domain.Hash
	URL           string
	AlreadyStored bool
	Headers       map[string]string
}

// ChunkReceipt is what StoreUploaded issued for a proxied upload of one chunk;
// a valid one stands in for the read-back at confirm.
type ChunkReceipt struct {
	Hash    domain.Hash
	Receipt string
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
	tx        chunkTransactor
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
// any row for it is recorded: it clears the garbage marks of the project's
// chunks, waiting for a sweep that holds them, so content the sweep removed is
// seen as missing (and uploaded again) instead of being recorded. Nil (the
// free default) runs no collector.
type ChunkGCGuard interface {
	Protect(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error
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

// WithTransactor runs the ledger attribution and the chunk row inserts of one
// confirm (or server-side write) in one transaction, so rows are never
// visible before the ledger accepted them. Without one (tests) they run
// back to back.
func (c *Chunk) WithTransactor(tx chunkTransactor) *Chunk {
	c.tx = tx
	return c
}

func (c *Chunk) withinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if c.tx == nil {
		return fn(ctx)
	}
	return c.tx.WithinTx(ctx, fn)
}

func (c *Chunk) protect(ctx context.Context, projectID snow.ID, hashes ...domain.Hash) error {
	if c.gcGuard == nil || len(hashes) == 0 {
		return nil
	}
	return c.gcGuard.Protect(ctx, projectID, hashes)
}

// WithMetrics attaches observability metrics. Without one (the default), chunk
// operations are not counted.
func (c *Chunk) WithMetrics(metrics *obs.Metrics) *Chunk {
	c.metrics = metrics
	return c
}

// upload stores one chunk in the project's store. It returns true when the
// content was newly stored, false when an identical chunk was already
// present. It records no row: recordChunks does, once the ledger accepted it.
func (c *Chunk) upload(ctx context.Context, store storage.ChunkStore, projectID snow.ID, hash domain.Hash, data []byte) (bool, error) {
	if got := chunker.Sum(data); got != hash {
		c.metrics.ObserveChunkVerifyFailure("store")
		return false, domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", hash))
	}
	if err := c.protect(ctx, projectID, hash); err != nil {
		return false, err
	}
	exists, err := store.Exists(ctx, projectID, hash)
	if err != nil {
		c.metrics.ObserveChunkOp("upload", "error")
		return false, domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", hash, err))
	}
	if exists {
		c.metrics.ObserveChunkOp("upload", "dedup")
		return false, nil
	}
	if err := store.Put(ctx, projectID, hash, data); err != nil {
		c.metrics.ObserveChunkOp("upload", "error")
		return false, domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
	}
	c.metrics.ObserveChunkOp("upload", "stored")
	c.metrics.ObserveChunkBytes("upload", int64(len(data)))
	return true, nil
}

// AttributeProjectChunks records the project's rows for chunks the server
// produced itself (merge output) and stored with StoreForProject. The ledger
// attributes them first, in the same transaction, so a quota failure leaves
// no row.
func (c *Chunk) AttributeProjectChunks(ctx context.Context, projectID snow.ID, chunks []LedgerChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	resolved, err := c.stores.ForProject(ctx, projectID)
	if err != nil {
		return err
	}
	rows := make([]ChunkRef, 0, len(chunks))
	for _, chunk := range chunks {
		rows = append(rows, ChunkRef(chunk))
	}
	var attribute func(ctx context.Context) error
	if ledger := c.ledgerFor(resolved); ledger != nil {
		attribute = func(ctx context.Context) error {
			return ledger.AttributeProjectChunks(ctx, projectID, chunks)
		}
	}
	return c.recordChunks(ctx, projectID, rows, attribute)
}

// recordChunks runs the ledger attribution (when given) and then inserts the
// project's chunk rows, in one transaction: a row is the project's link to
// the content, so it must not be visible before the quota accepted it.
func (c *Chunk) recordChunks(ctx context.Context, projectID snow.ID, rows []ChunkRef, attribute func(ctx context.Context) error) error {
	if len(rows) == 0 {
		return nil
	}
	return c.withinTx(ctx, func(ctx context.Context) error {
		if attribute != nil {
			if err := attribute(ctx); err != nil {
				return err
			}
		}
		return c.chunkRepo.InsertChunksIfNotExist(ctx, projectID, rows)
	})
}

// StoreForProject stores server-held content in the project's store without
// recording its row; AttributeProjectChunks records it once the caller is
// ready to land it.
func (c *Chunk) StoreForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) (bool, error) {
	resolved, err := c.writableStore(ctx, projectID)
	if err != nil {
		return false, err
	}
	return c.upload(ctx, resolved.Store, projectID, hash, data)
}

// UploadForProject stores server-held content in the project's store and
// records its row (through the ledger) so a push of that project may
// reference it.
func (c *Chunk) UploadForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) (bool, error) {
	isNew, err := c.StoreForProject(ctx, projectID, hash, data)
	if err != nil {
		return false, err
	}
	if err := c.AttributeProjectChunks(ctx, projectID, []LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}}); err != nil {
		return false, err
	}
	return isNew, nil
}

// StoreUploaded verifies and stores chunk content a client sent through a
// signed server path, without recording metadata; the project's chunk row is
// written by ConfirmUploads once the client reports the transfer done (quota
// is enforced there), so this path records no rows. Content writes are safe
// to run concurrently.
//
// It returns a receipt (chunkproof.NewReceipt) for the project, hash and the
// size the server measured, valid for the presign TTL: the server saw these
// bytes itself, so confirm accepts the receipt instead of reading them back.
//
// A store clients can also write directly (storage.ChunkOverwriter, i.e.
// S3 in proxy mode) is written unconditionally, which replaces garbage an
// earlier direct upload left at the key; other stores skip content they
// already hold. A chunk garbage collector's mark on the project's chunk is
// cleared before the write, so a sweep cannot remove the content between this
// write and the confirm that relies on the receipt.
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
	if err := c.protect(ctx, projectID, hash); err != nil {
		return "", err
	}
	if err := c.writeUploaded(ctx, resolved.Store, projectID, hash, data); err != nil {
		return "", err
	}
	size := int64(len(data))
	return chunkproof.NewReceipt(c.transfer.SigningKey, projectID, hash, size, c.now().Add(c.transfer.PresignTTL)), nil
}

func (c *Chunk) writeUploaded(ctx context.Context, store storage.ChunkStore, projectID snow.ID, hash domain.Hash, data []byte) error {
	if overwriter, ok := store.(storage.ChunkOverwriter); ok {
		if err := overwriter.Overwrite(ctx, projectID, hash, data); err != nil {
			c.metrics.ObserveChunkOp("put", "error")
			return domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", hash, err))
		}
		c.metrics.ObserveChunkOp("put", "stored")
		c.metrics.ObserveChunkBytes("put", int64(len(data)))
		return nil
	}
	exists, err := store.Exists(ctx, projectID, hash)
	if err != nil {
		c.metrics.ObserveChunkOp("put", "error")
		return domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", hash, err))
	}
	if exists {
		c.metrics.ObserveChunkOp("put", "dedup")
		return nil
	}
	if err := store.Put(ctx, projectID, hash, data); err != nil {
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
	data, err = resolved.Store.Get(ctx, projectID, hash)
	if err != nil {
		c.metrics.ObserveChunkOp("get", "error")
		return nil, err
	}
	c.metrics.ObserveChunkOp("get", "ok")
	c.metrics.ObserveChunkBytes("get", int64(len(data)))
	return data, nil
}

// PresignUploadURLs signs one page of upload paths for refs. Dedup is per
// project, so the decision only looks at the project's own chunk rows:
//
//   - no row: needs upload (the store is not asked, so a fresh push makes no
//     existence checks);
//   - a row and the object present: AlreadyStored, no URL, skip the transfer;
//   - a row but the object lost: needs upload again.
//
// When a ledger is attached, refs that would add new bytes are checked against
// the organization's remaining quota first.
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
	recorded, err := c.chunkRepo.ProjectChunkSizes(ctx, project.ID, pageHashes)
	if err != nil {
		return nil, "", err
	}
	// Recorded chunks are protected from GC before they are checked: confirm
	// does not see the ones the store already holds (the client skips them),
	// so a mark would otherwise survive until the push that references them.
	// Protect waits for a sweep holding a row, so a swept chunk then reads as
	// missing and is uploaded again.
	if err := c.protect(ctx, project.ID, recordedHashes(pageHashes, recorded)...); err != nil {
		return nil, "", err
	}
	exists, err := c.existsRecorded(ctx, resolved.Store, project.ID, page, recorded)
	if err != nil {
		return nil, "", err
	}
	urls := make([]ChunkURL, 0, len(page))
	for i, ref := range page {
		if exists[i] {
			urls = append(urls, ChunkURL{Hash: ref.Hash, AlreadyStored: true})
			continue
		}
		target, err := c.uploadTarget(ctx, resolved, org, project, ref, expiry)
		if err != nil {
			return nil, "", err
		}
		urls = append(urls, ChunkURL{Hash: ref.Hash, URL: target.URL, Headers: target.Headers})
	}
	return urls, next, nil
}

// recordedHashes returns the hashes the project has a chunk row for.
func recordedHashes(hashes []domain.Hash, recorded map[domain.Hash]int64) []domain.Hash {
	out := make([]domain.Hash, 0, len(recorded))
	for _, hash := range hashes {
		if _, ok := recorded[hash]; ok {
			out = append(out, hash)
		}
	}
	return out
}

// existsRecorded checks the store only for refs the project has a chunk row
// for: content the project never confirmed is treated as absent and gets an
// upload target without a HEAD (an object that does sit at such a key, an
// upload whose confirm never ran, makes the create-only PUT answer 412 and
// confirm then reads it back). A row only hints "present"; refs with one are
// still checked, which finds lost objects. The checks of all presigns share
// existsSlots.
func (c *Chunk) existsRecorded(ctx context.Context, store storage.ChunkStore, projectID snow.ID, page []ChunkRef, recorded map[domain.Hash]int64) ([]bool, error) {
	exists := make([]bool, len(page))
	isRecorded := func(i int) bool {
		_, ok := recorded[page[i].Hash]
		return ok
	}
	err := fanOut(ctx, c.existsSlots, len(page), isRecorded, func(ctx context.Context, i int) error {
		ok, err := store.Exists(ctx, projectID, page[i].Hash)
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

// checkHeadroom projects the bytes the refs without a chunk row in the project
// would add and asks the ledger whether the organization still has room for
// them.
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
	target, err := direct.PresignUpload(ctx, project.ID, ref.Hash, ref.SizeBytes, c.transfer.PresignTTL)
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
		path, err := c.downloadPath(ctx, resolved.Store, org.Slug, project, hash, expiry)
		if err != nil {
			return nil, "", err
		}
		urls = append(urls, ChunkURL{Hash: hash, URL: path})
	}
	return urls, next, nil
}

func (c *Chunk) downloadPath(ctx context.Context, store storage.ChunkStore, org string, project *domain.Project, hash domain.Hash, expiry int64) (string, error) {
	direct, ok := store.(storage.DirectTransferStore)
	if !ok {
		return chunkurl.DownloadPath(c.transfer.SigningKey, org, project.Slug, hash.String(), expiry), nil
	}
	url, err := direct.PresignDownload(ctx, project.ID, hash, c.transfer.PresignTTL)
	if err != nil {
		return "", domain.NewErrorInternalServer(fmt.Sprintf("presign download for chunk %s: %v", hash, err))
	}
	return url, nil
}

// ConfirmUploads verifies that uploaded chunk content landed in the project's
// store and records the project's chunk rows for the present chunks. Hashes
// the project already has a row for are trusted (the content was verified
// when the row was written). For the others the verification depends on the
// upload path: a valid receipt from StoreUploaded (proxied upload, same
// project and hash, not expired) means the server hashed the bytes on the way
// in, so its size is used with no store read; anything else (direct uploads,
// an invalid or expired receipt) is read back from the store, streamed and
// hashed with plain BLAKE3. Read-backs share one server-wide limit
// (WithVerifyLimit). The recorded size is measured by the server, never taken
// from the client. Then, in one transaction, the ledger attributes the new
// rows (quota) and the rows are inserted, so no row is visible before the
// quota accepted it. Hashes still missing are returned so the client can
// retry them.
func (c *Chunk) ConfirmUploads(ctx context.Context, org *domain.Organization, project *domain.Project, hashes []domain.Hash, receipts []ChunkReceipt) ([]domain.Hash, error) {
	ctx, span := obs.StartSpan(ctx, "chunk.confirm",
		attribute.String(obs.AttrOrg, org.Slug),
		attribute.String(obs.AttrProject, project.Slug),
		attribute.Int(obs.AttrChunkCount, len(hashes)))
	missing, confirmedBytes, err := c.confirmUploads(ctx, org, project, hashes, receipts)
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

// chunkCheck is what confirm learned about one chunk without a row.
type chunkCheck struct {
	size    int64
	present bool
}

func (c *Chunk) confirmUploads(ctx context.Context, org *domain.Organization, project *domain.Project, hashes []domain.Hash, receipts []ChunkReceipt) ([]domain.Hash, int64, error) {
	hashes = uniqueHashes(hashes)
	resolved, err := c.writableStore(ctx, project.ID)
	if err != nil {
		return nil, 0, err
	}
	if err := c.protect(ctx, project.ID, hashes...); err != nil {
		return nil, 0, err
	}
	recorded, err := c.chunkRepo.ProjectChunkSizes(ctx, project.ID, hashes)
	if err != nil {
		return nil, 0, err
	}
	// receipted is filled before the read-backs start and only read after;
	// checks is written by them under checkMu.
	receipted := c.verifyReceipts(project.ID, receipts)
	checks := make(map[domain.Hash]chunkCheck)
	var checkMu sync.Mutex
	needsCheck := func(i int) bool {
		// A row does not vouch for the object: presign re-issues an upload
		// when the object behind a row is gone, so the new bytes of a
		// recorded chunk are hashed too unless a receipt says the server did.
		_, isReceipted := receipted[hashes[i]]
		return !isReceipted
	}
	err = fanOut(ctx, c.verifySlots, len(hashes), needsCheck, func(ctx context.Context, i int) error {
		hash := hashes[i]
		check, err := c.readBack(ctx, resolved.Store, project.ID, hash)
		if err != nil {
			return err
		}
		checkMu.Lock()
		checks[hash] = check
		checkMu.Unlock()
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	var missing []domain.Hash
	var rows []ChunkRef
	var confirmedBytes int64
	for _, hash := range hashes {
		check, ok := receipted[hash]
		if !ok {
			check = checks[hash]
		}
		if !check.present {
			missing = append(missing, hash)
			continue
		}
		if size, ok := recorded[hash]; ok {
			// Already charged and recorded: keep the row's size.
			confirmedBytes += size
			continue
		}
		rows = append(rows, ChunkRef{Hash: hash, SizeBytes: check.size})
		confirmedBytes += check.size
	}
	var attribute func(ctx context.Context) error
	if ledger := c.ledgerFor(resolved); ledger != nil && len(rows) > 0 {
		attributed := make([]LedgerChunk, 0, len(rows))
		for _, row := range rows {
			attributed = append(attributed, LedgerChunk(row))
		}
		attribute = func(ctx context.Context) error {
			return ledger.Attribute(ctx, org, project, attributed)
		}
	}
	if err := c.recordChunks(ctx, project.ID, rows, attribute); err != nil {
		return nil, 0, err
	}
	return missing, confirmedBytes, nil
}

// verifyReceipts returns the chunks without a row whose receipt is valid for
// this project and hash: the server received and stored these bytes for the
// project itself (proxied upload), so they are present with the size the
// receipt records and need no read-back. Invalid or expired receipts are
// ignored; those chunks are read back.
func (c *Chunk) verifyReceipts(projectID snow.ID, receipts []ChunkReceipt) map[domain.Hash]chunkCheck {
	receipted := make(map[domain.Hash]chunkCheck, len(receipts))
	for _, receipt := range receipts {
		if receipt.Receipt == "" {
			continue
		}
		size, err := chunkproof.VerifyReceipt(c.transfer.SigningKey, projectID, receipt.Hash, receipt.Receipt, c.now())
		if err != nil {
			continue
		}
		receipted[receipt.Hash] = chunkCheck{size: size, present: true}
	}
	return receipted
}

// readBack streams the project's stored object through a plain BLAKE3 check
// and returns its size. Direct uploads land in the store without passing
// through the server, so the bytes are hashed here: an object that does not
// match its key is deleted (it is garbage that would otherwise block the real
// content at a create-only key) and reported missing, like an absent object.
func (c *Chunk) readBack(ctx context.Context, store storage.ChunkStore, projectID snow.ID, hash domain.Hash) (chunkCheck, error) {
	verifier := chunkproof.NewVerifier()
	err := readChunk(ctx, store, projectID, hash, verifier)
	if domain.IsErrorNotFound(err) {
		return chunkCheck{}, nil
	}
	if err != nil {
		return chunkCheck{}, domain.NewErrorDatabase(fmt.Sprintf("read chunk %s: %v", hash, err))
	}
	// A legitimate chunk is never larger than the maximum: presign rejects
	// larger refs and direct uploads pin the signed length. readChunk stops
	// one byte past it, so a larger object fails here without being read whole.
	if verifier.Size() > chunker.MaxChunkSize() || verifier.Sum() != hash {
		c.metrics.ObserveChunkVerifyFailure("confirm")
		return chunkCheck{}, c.dropInvalidChunk(ctx, store, projectID, hash)
	}
	return chunkCheck{size: verifier.Size(), present: true}, nil
}

// uniqueHashes drops repeated hashes, keeping the first occurrence's order.
func uniqueHashes(hashes []domain.Hash) []domain.Hash {
	seen := make(map[domain.Hash]struct{}, len(hashes))
	out := make([]domain.Hash, 0, len(hashes))
	for _, hash := range hashes {
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		out = append(out, hash)
	}
	return out
}

// readChunk streams the stored chunk into w, at most one byte more than the
// largest legal chunk. Stores implementing storage.ChunkOpener are streamed;
// others are read whole with Get. A missing chunk is a domain NotFound error.
func readChunk(ctx context.Context, store storage.ChunkStore, projectID snow.ID, hash domain.Hash, w io.Writer) error {
	limit := chunker.MaxChunkSize() + 1
	opener, ok := store.(storage.ChunkOpener)
	if !ok {
		data, err := store.Get(ctx, projectID, hash)
		if err != nil {
			return err
		}
		_, _ = w.Write(data[:min(int64(len(data)), limit)])
		return nil
	}
	r, err := opener.Open(ctx, projectID, hash)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	_, err = io.Copy(w, io.LimitReader(r, limit))
	return err
}

func (c *Chunk) dropInvalidChunk(ctx context.Context, store storage.ChunkStore, projectID snow.ID, hash domain.Hash) error {
	direct, ok := store.(storage.DirectTransferStore)
	if !ok {
		return nil
	}
	if err := direct.DeleteChunk(ctx, projectID, hash); err != nil {
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
