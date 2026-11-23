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

	"github.com/nipalab/nipa/internal/chunkbundle"
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
	// InsertChunkRows records the project's chunk rows with their locations
	// (and their upload markers) in batched statements; existing rows are
	// left alone, keeping their location. A chunk row is the project's link
	// to the content: a push may reference exactly the hashes the project has
	// a row for.
	InsertChunkRows(ctx context.Context, projectID snow.ID, rows []ChunkRow) error
	// ProjectChunkRecords returns the row (size, location and pack state) of
	// every hash the project has a chunk row for; other hashes are absent.
	ProjectChunkRecords(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]ChunkRecord, error)
	// RelocateChunks points rows whose content was lost at newly stored
	// bytes. Each move applies only while the row still points at its From
	// location, so a concurrent move wins and is kept.
	RelocateChunks(ctx context.Context, projectID snow.ID, moves []ChunkRelocation) error
	// InsertChunkPack records a pack in the pending state, before its object
	// is written.
	InsertChunkPack(ctx context.Context, pack ChunkPack) error
	// SetChunkPackState moves a pack to state.
	SetChunkPackState(ctx context.Context, packID snow.ID, state ChunkPackState) error
}

// chunkTransactor runs a function inside one database transaction; the chunk
// repository and the storage ledger join it through the context.
type chunkTransactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// DefaultChunkVerifyConcurrency is the server-wide number of chunk read-backs
// confirm runs at once when CHUNK_VERIFY_CONCURRENCY is unset.
const DefaultChunkVerifyConcurrency = 64

// DefaultChunkStoreWriteConcurrency is the server-wide number of loose chunk
// writes storeChunks runs at once when CHUNK_STORE_WRITE_CONCURRENCY is unset.
const DefaultChunkStoreWriteConcurrency = 64

// DefaultChunkProxyBufferBytes is the server-wide memory budget for bundle
// upload bodies when none is configured (CHUNK_PROXY_BUFFER_MIB).
const DefaultChunkProxyBufferBytes = 256 << 20

// DefaultChunkBundleMaxBytes is the largest bundle body a presign signs when
// none is configured (CHUNK_BUNDLE_MAX_MIB).
const DefaultChunkBundleMaxBytes = 64 << 20

// UploadMode selects how clients send chunk bytes to a store that supports
// direct transfers (CHUNK_UPLOAD_MODE).
type UploadMode string

const (
	// UploadModeDirect hands out the store's own presigned upload URLs; the
	// bytes bypass the server and confirm reads every chunk back.
	UploadModeDirect UploadMode = "direct"
	// UploadModeProxy hands out a signed bundle URL: clients send many chunks
	// per request, the server hashes them on the way in, writes the store
	// itself and returns receipts that let confirm skip the read-back.
	// Own-storage (External) projects keep uploading direct so customer bytes
	// never flow through the server.
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
// is set only for direct uploads (the chunk goes to the store's presigned
// URL): it is empty when AlreadyStored is set or when the chunk goes in a
// bundle (UploadPresign.BundleURL). Headers are request headers the PUT must
// carry because they are part of a direct backend's signature.
//
// For downloads, Length > 0 means the chunk is the byte range [Offset,
// Offset+Length) of the object at URL (a pack); Length 0 means the whole
// object is the chunk.
type ChunkURL struct {
	Hash          domain.Hash
	URL           string
	AlreadyStored bool
	Headers       map[string]string
	Offset        int64
	Length        int64
}

// ChunkReceipt is what StoreBundle issued for one chunk of a bundle upload; a
// valid one stands in for the read-back at confirm.
type ChunkReceipt struct {
	Hash    domain.Hash
	Receipt string
}

// UploadPresign is one page of upload decisions. When the project's store
// takes uploads through the server (a store without direct transfers, or the
// server's own store in proxy mode), every chunk that needs an upload has no
// URL and goes in bundles to BundleURL instead, each body at most
// BundleMaxBytes and BundleMaxChunks entries.
type UploadPresign struct {
	URLs            []ChunkURL
	NextPageToken   string
	BundleURL       string
	BundleMaxBytes  int64
	BundleMaxChunks int
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
	// proxyBuffer bounds the bytes of every bundle upload body held in
	// memory at once, across all requests; proxyBufferSize is its capacity.
	proxyBuffer     *semaphore.Weighted
	proxyBufferSize int64
	// bundleMax is the largest bundle body a presign signs.
	bundleMax int64
	// writeSlots bounds the loose chunk writes of every storeChunks in
	// flight, across all requests.
	writeSlots *semaphore.Weighted
	// packs writes the content storeChunks stores as one pack object when the
	// project's store holds packs; packIDs names them.
	packs   bool
	packIDs snow.Node
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
		bundleMax:       DefaultChunkBundleMaxBytes,

		writeSlots: semaphore.NewWeighted(DefaultChunkStoreWriteConcurrency),
	}
}

// WithPacks makes storeChunks write the content of one call (a bundle, a
// merged file) as one pack object, named by ids, whenever the project is on
// the server's own store and that store holds packs (storage.PackStore,
// CHUNK_PACKS=on). Other projects keep loose objects.
func (c *Chunk) WithPacks(ids snow.Node) *Chunk {
	c.packs = ids != nil
	c.packIDs = ids
	return c
}

// WithStoreWriteLimit sets how many loose chunk writes all storeChunks calls
// together run at once; n <= 0 keeps DefaultChunkStoreWriteConcurrency.
func (c *Chunk) WithStoreWriteLimit(n int) *Chunk {
	if n <= 0 {
		n = DefaultChunkStoreWriteConcurrency
	}
	c.writeSlots = semaphore.NewWeighted(int64(n))
	return c
}

// WithBundleMax sets the largest bundle body a presign signs; n <= 0 keeps
// DefaultChunkBundleMaxBytes. It never goes below one entry of the largest
// chunk, so every chunk fits in some bundle.
func (c *Chunk) WithBundleMax(n int64) *Chunk {
	if n <= 0 {
		n = DefaultChunkBundleMaxBytes
	}
	c.bundleMax = max(n, chunkbundle.EncodedSize(int(chunker.MaxChunkSize())))
	return c
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

// WithProxyBuffer sets the server-wide byte budget for bundle upload bodies
// held in memory; n <= 0 keeps DefaultChunkProxyBufferBytes.
func (c *Chunk) WithProxyBuffer(n int64) *Chunk {
	if n <= 0 {
		n = DefaultChunkProxyBufferBytes
	}
	c.proxyBuffer = semaphore.NewWeighted(n)
	c.proxyBufferSize = n
	return c
}

// ReserveUploadBuffer takes size bytes of the bundle upload budget before
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

// RecordStoredChunks records the project's rows for chunks the server
// produced itself (merge output) and stored with StoreChunks, at the
// locations StoreChunks returned. The ledger attributes them first, in the
// same transaction, so a quota failure leaves no row.
func (c *Chunk) RecordStoredChunks(ctx context.Context, projectID snow.ID, rows []ChunkRow) error {
	if len(rows) == 0 {
		return nil
	}
	resolved, err := c.stores.ForProject(ctx, projectID)
	if err != nil {
		return err
	}
	var attribute func(ctx context.Context) error
	if ledger := c.ledgerFor(resolved); ledger != nil {
		chunks := make([]LedgerChunk, 0, len(rows))
		for _, row := range rows {
			chunks = append(chunks, LedgerChunk{Hash: row.Hash, SizeBytes: row.SizeBytes})
		}
		attribute = func(ctx context.Context) error {
			return ledger.AttributeProjectChunks(ctx, projectID, chunks)
		}
	}
	return c.recordChunks(ctx, projectID, rows, nil, attribute)
}

// recordChunks runs the ledger attribution (when given), then inserts the
// project's chunk rows and moves lost rows to their new location, in one
// transaction: a row is the project's link to the content, so it must not be
// visible before the quota accepted it.
func (c *Chunk) recordChunks(ctx context.Context, projectID snow.ID, rows []ChunkRow, moves []ChunkRelocation, attribute func(ctx context.Context) error) error {
	if len(rows) == 0 && len(moves) == 0 {
		return nil
	}
	return c.withinTx(ctx, func(ctx context.Context) error {
		if attribute != nil {
			if err := attribute(ctx); err != nil {
				return err
			}
		}
		if len(rows) > 0 {
			if err := c.chunkRepo.InsertChunkRows(ctx, projectID, rows); err != nil {
				return err
			}
		}
		if len(moves) == 0 {
			return nil
		}
		return c.chunkRepo.RelocateChunks(ctx, projectID, moves)
	})
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
func (c *Chunk) PresignUploadURLs(ctx context.Context, org *domain.Organization, project *domain.Project, refs []ChunkRef, pageSize int, pageToken string) (UploadPresign, error) {
	ctx, span := obs.StartSpan(ctx, "chunk.presign_upload",
		attribute.String(obs.AttrOrg, org.Slug),
		attribute.String(obs.AttrProject, project.Slug),
		attribute.Int(obs.AttrChunkCount, len(refs)))
	presign, err := c.presignUploadURLs(ctx, org, project, refs, pageSize, pageToken)
	obs.FinishSpan(span, err)
	result := "ok"
	if err != nil {
		result = "error"
	}
	c.metrics.ObserveChunkOp("presign_upload", result)
	return presign, err
}

func (c *Chunk) presignUploadURLs(ctx context.Context, org *domain.Organization, project *domain.Project, refs []ChunkRef, pageSize int, pageToken string) (UploadPresign, error) {
	if err := validateChunkRefs(refs); err != nil {
		return UploadPresign{}, err
	}
	resolved, err := c.writableStore(ctx, project.ID)
	if err != nil {
		return UploadPresign{}, err
	}
	if err := c.checkHeadroom(ctx, c.ledgerFor(resolved), org, project, refs); err != nil {
		return UploadPresign{}, err
	}
	page, next, err := paginate(refs, pageSize, pageToken, c.transfer.MaxPageSize)
	if err != nil {
		return UploadPresign{}, err
	}
	expiry := chunkurl.Expiry(c.now(), c.transfer.PresignTTL)
	pageHashes := make([]domain.Hash, 0, len(page))
	for _, ref := range page {
		pageHashes = append(pageHashes, ref.Hash)
	}
	recorded, err := c.chunkRepo.ProjectChunkRecords(ctx, project.ID, pageHashes)
	if err != nil {
		return UploadPresign{}, err
	}
	// Recorded chunks are protected from GC before they are checked: confirm
	// does not see the ones the store already holds (the client skips them),
	// so a mark would otherwise survive until the push that references them.
	// Protect waits for a sweep holding a row, so a swept chunk then reads as
	// missing and is uploaded again.
	protected := recordedHashes(pageHashes, recorded)
	if err := c.protect(ctx, project.ID, protected...); err != nil {
		return UploadPresign{}, err
	}
	// A packed row is answered from the database, not the store: read the
	// protected rows again, so a row a sweep deleted while Protect waited
	// for it reads as missing (a loose row's HEAD finds that by itself).
	if c.gcGuard != nil && hasPacked(recorded) {
		if recorded, err = c.chunkRepo.ProjectChunkRecords(ctx, project.ID, protected); err != nil {
			return UploadPresign{}, err
		}
	}
	exists, err := c.existsRecorded(ctx, unwrapStore(resolved.Store), project.ID, page, recorded)
	if err != nil {
		return UploadPresign{}, err
	}
	direct, isDirect := c.directUploads(resolved)
	presign := UploadPresign{URLs: make([]ChunkURL, 0, len(page)), NextPageToken: next}
	bundled := 0
	for i, ref := range page {
		switch {
		case exists[i]:
			presign.URLs = append(presign.URLs, ChunkURL{Hash: ref.Hash, AlreadyStored: true})
		case !isDirect:
			presign.URLs = append(presign.URLs, ChunkURL{Hash: ref.Hash})
			bundled++
		default:
			target, err := direct.PresignUpload(ctx, project.ID, ref.Hash, ref.SizeBytes, c.transfer.PresignTTL)
			if err != nil {
				return UploadPresign{}, domain.NewErrorInternalServer(fmt.Sprintf("presign upload for chunk %s: %v", ref.Hash, err))
			}
			presign.URLs = append(presign.URLs, ChunkURL{Hash: ref.Hash, URL: target.URL, Headers: target.Headers})
		}
	}
	if bundled > 0 {
		presign.BundleURL = chunkurl.BundlePath(c.transfer.SigningKey, org.Slug, project.Slug, c.bundleMax, bundled, expiry)
		presign.BundleMaxBytes = c.bundleMax
		presign.BundleMaxChunks = bundled
	}
	return presign, nil
}

// hasPacked reports whether any row points into a pack.
func hasPacked(records map[domain.Hash]ChunkRecord) bool {
	for _, record := range records {
		if record.Location.Packed() {
			return true
		}
	}
	return false
}

// recordedHashes returns the hashes the project has a chunk row for.
func recordedHashes(hashes []domain.Hash, recorded map[domain.Hash]ChunkRecord) []domain.Hash {
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
// confirm then reads it back). A row only hints "present"; refs with a loose
// row are still checked, which finds lost objects. A packed row is answered
// from its pack's state with no store request. The checks of all presigns
// share existsSlots.
func (c *Chunk) existsRecorded(ctx context.Context, store storage.ChunkStore, projectID snow.ID, page []ChunkRef, recorded map[domain.Hash]ChunkRecord) ([]bool, error) {
	exists := make([]bool, len(page))
	isRecorded := func(i int) bool {
		record, ok := recorded[page[i].Hash]
		if ok && record.Location.Packed() {
			exists[i] = record.Readable()
			return false
		}
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

// directUploads reports whether clients PUT the project's chunks straight to
// its store (presigned URLs) rather than in bundles through the server: only
// a direct-transfer store does, and in proxy mode only for own-storage
// (External) projects, whose bytes never flow through the server.
func (c *Chunk) directUploads(resolved ProjectStore) (storage.DirectTransferStore, bool) {
	direct, ok := unwrapStore(resolved.Store).(storage.DirectTransferStore)
	if !ok || (c.uploadMode == UploadModeProxy && !resolved.External) {
		return nil, false
	}
	return direct, true
}

// PresignDownloadURLs signs one page of download paths for hashes the caller
// may read. Stores implementing storage.DirectTransferStore hand out absolute
// backend presigned URLs so clients fetch content directly from the backend.
// A packed chunk gets its pack's presigned URL (signed once per pack per
// page and shared by all its chunks) with the chunk's offset and length, for
// a Range request; a loose chunk gets its own object's URL with no range.
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
	store := unwrapStore(resolved.Store)
	records, err := c.packedRecords(ctx, resolved, project.ID, page)
	if err != nil {
		return nil, "", err
	}
	expiry := chunkurl.Expiry(c.now(), c.transfer.PresignTTL)
	urls := make([]ChunkURL, 0, len(page))
	packURLs := make(map[snow.ID]string)
	for _, hash := range page {
		if record, ok := records[hash]; ok && record.Location.Packed() {
			url, err := c.packDownloadURL(ctx, store.(storage.PackStore), project.ID, record.Location.PackID, packURLs)
			if err != nil {
				return nil, "", err
			}
			urls = append(urls, ChunkURL{Hash: hash, URL: url, Offset: record.Location.Offset, Length: record.SizeBytes})
			continue
		}
		path, err := c.downloadPath(ctx, store, org.Slug, project, hash, expiry)
		if err != nil {
			return nil, "", err
		}
		urls = append(urls, ChunkURL{Hash: hash, URL: path})
	}
	return urls, next, nil
}

// packedRecords returns the rows of hashes when the project's store holds
// packs (only then can a row be packed); otherwise nil, without a query.
func (c *Chunk) packedRecords(ctx context.Context, resolved ProjectStore, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]ChunkRecord, error) {
	if resolved.External {
		return nil, nil
	}
	if _, ok := unwrapStore(resolved.Store).(storage.PackStore); !ok {
		return nil, nil
	}
	return c.chunkRepo.ProjectChunkRecords(ctx, projectID, hashes)
}

// packDownloadURL presigns the pack's object once per page.
func (c *Chunk) packDownloadURL(ctx context.Context, packs storage.PackStore, projectID, packID snow.ID, signed map[snow.ID]string) (string, error) {
	if url, ok := signed[packID]; ok {
		return url, nil
	}
	url, err := packs.PresignPackDownload(ctx, storage.PackKey(projectID, packID), c.transfer.PresignTTL)
	if err != nil {
		return "", domain.NewErrorInternalServer(fmt.Sprintf("presign download for pack %s: %v", packID.Base36(), err))
	}
	signed[packID] = url
	return url, nil
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
// upload path: a valid receipt from StoreBundle (bundle upload, same
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

// chunkCheck is what confirm learned about one uploaded chunk: its size and
// where the bytes are.
type chunkCheck struct {
	size     int64
	present  bool
	location ChunkLocation
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
	recorded, err := c.chunkRepo.ProjectChunkRecords(ctx, project.ID, hashes)
	if err != nil {
		return nil, 0, err
	}
	store := unwrapStore(resolved.Store)
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
		check, err := c.readBack(ctx, store, project.ID, hash)
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
	moves, err := c.lostRows(ctx, store, project.ID, recorded, receipted)
	if err != nil {
		return nil, 0, err
	}
	var missing []domain.Hash
	var rows []ChunkRow
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
		if record, ok := recorded[hash]; ok {
			// Already charged and recorded: keep the row's size and, unless
			// its content was lost (lostRows), its location.
			confirmedBytes += record.SizeBytes
			continue
		}
		rows = append(rows, ChunkRow{Hash: hash, SizeBytes: check.size, Location: check.location})
		confirmedBytes += check.size
	}
	var attribute func(ctx context.Context) error
	if ledger := c.ledgerFor(resolved); ledger != nil && len(rows) > 0 {
		attributed := make([]LedgerChunk, 0, len(rows))
		for _, row := range rows {
			attributed = append(attributed, LedgerChunk{Hash: row.Hash, SizeBytes: row.SizeBytes})
		}
		attribute = func(ctx context.Context) error {
			return ledger.Attribute(ctx, org, project, attributed)
		}
	}
	if err := c.recordChunks(ctx, project.ID, rows, moves, attribute); err != nil {
		return nil, 0, err
	}
	return missing, confirmedBytes, nil
}

// lostRows finds the recorded chunks a receipt names new bytes for while the
// row's own content is gone, and returns the moves that point those rows at
// the new bytes. A row whose content is still there keeps its location (the
// new copy is dead bytes in its pack): the same content uploaded twice by a
// retried bundle or a concurrent push. The row's content is lost when its
// pack is no longer readable (pack state, no store request) or, for a loose
// row, when its object is missing (one HEAD, only for loose rows that get a
// packed receipt: presign found the object missing and the client uploaded
// it again). Only server-signed locations reach a move.
func (c *Chunk) lostRows(ctx context.Context, store storage.ChunkStore, projectID snow.ID, recorded map[domain.Hash]ChunkRecord, receipted map[domain.Hash]chunkCheck) ([]ChunkRelocation, error) {
	var candidates []ChunkRelocation
	for hash, check := range receipted {
		record, ok := recorded[hash]
		if !ok || record.Location == check.location {
			continue
		}
		if record.Location.Packed() && record.Readable() {
			continue
		}
		if !record.Location.Packed() && !check.location.Packed() {
			// A loose receipt rewrote the row's own key: nothing to move.
			continue
		}
		candidates = append(candidates, ChunkRelocation{Hash: hash, From: record.Location, To: check.location})
	}
	lost := make([]bool, len(candidates))
	looseRow := func(i int) bool {
		if candidates[i].From.Packed() {
			lost[i] = true
			return false
		}
		return true
	}
	err := fanOut(ctx, c.existsSlots, len(candidates), looseRow, func(ctx context.Context, i int) error {
		ok, err := store.Exists(ctx, projectID, candidates[i].Hash)
		if err != nil {
			return domain.NewErrorDatabase(fmt.Sprintf("check chunk %s: %v", candidates[i].Hash, err))
		}
		lost[i] = !ok
		return nil
	})
	if err != nil {
		return nil, err
	}
	moves := make([]ChunkRelocation, 0, len(candidates))
	for i, move := range candidates {
		if lost[i] {
			moves = append(moves, move)
		}
	}
	return moves, nil
}

// verifyReceipts returns the chunks whose receipt is valid for this project
// and hash: the server received and stored these bytes for the project
// itself (bundle upload), so they are present with the size and location the
// receipt records and need no read-back. Invalid or expired receipts are
// ignored; those chunks are read back.
func (c *Chunk) verifyReceipts(projectID snow.ID, receipts []ChunkReceipt) map[domain.Hash]chunkCheck {
	receipted := make(map[domain.Hash]chunkCheck, len(receipts))
	for _, receipt := range receipts {
		if receipt.Receipt == "" {
			continue
		}
		size, location, err := chunkproof.VerifyReceipt(c.transfer.SigningKey, projectID, receipt.Hash, receipt.Receipt, c.now())
		if err != nil {
			continue
		}
		receipted[receipt.Hash] = chunkCheck{size: size, present: true, location: locationFromProof(location)}
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
