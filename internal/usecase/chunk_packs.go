package usecase

import (
	"bytes"
	"container/list"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// ChunkLocation is where a chunk's bytes sit in its project's store: a range
// of a pack object (PackID set, the length is the row's size) or, with a
// zero PackID, a loose object at the project's key for the hash.
type ChunkLocation struct {
	PackID snow.ID
	Offset int64
}

// Packed reports whether the chunk lives in a pack.
func (l ChunkLocation) Packed() bool {
	return l.PackID != 0
}

func locationFromProof(location chunkproof.Location) ChunkLocation {
	if location.Kind == chunkproof.LocationPack {
		return ChunkLocation{PackID: location.PackID, Offset: location.Offset}
	}
	return ChunkLocation{}
}

func (l ChunkLocation) proof() chunkproof.Location {
	if l.Packed() {
		return chunkproof.Packed(l.PackID, l.Offset)
	}
	return chunkproof.Loose()
}

// ChunkRow is one project chunk row to record: the hash, the size the server
// measured and where the bytes are.
type ChunkRow struct {
	Hash      domain.Hash
	SizeBytes int64
	Location  ChunkLocation
}

// ChunkPackState is the lifecycle of a pack object (chunk_packs.state).
type ChunkPackState string

const (
	// ChunkPackPending is a pack whose object is being written. A failed
	// write leaves the row for the garbage collector; nothing refers to it.
	ChunkPackPending ChunkPackState = "pending"
	// ChunkPackStored is a pack whose object is durable.
	ChunkPackStored ChunkPackState = "stored"
	// ChunkPackRetired is a pack compaction moved every live chunk out of;
	// it stays readable until the collector deletes it.
	ChunkPackRetired ChunkPackState = "retired"
)

// ChunkPack is one pack row: an object holding many chunks of one project.
type ChunkPack struct {
	ID         snow.ID
	ProjectID  snow.ID
	SizeBytes  int64
	ChunkCount int
}

// ChunkRecord is a project's chunk row with its location; PackState is the
// state of its pack (empty for a loose row).
type ChunkRecord struct {
	SizeBytes int64
	Location  ChunkLocation
	PackState ChunkPackState
}

// Readable reports whether a packed row's pack still holds its bytes. A
// loose row always says yes: only the store knows whether its object is
// there.
func (r ChunkRecord) Readable() bool {
	if !r.Location.Packed() {
		return true
	}
	return r.PackState == ChunkPackStored || r.PackState == ChunkPackRetired
}

// ChunkRelocation moves a row whose content was lost to newly stored bytes.
// The move only applies while the row still points at From.
type ChunkRelocation struct {
	Hash domain.Hash
	From ChunkLocation
	To   ChunkLocation
}

// chunkLocator looks up the rows (with their locations) of a project's
// chunks; the pack-aware reader uses it.
type chunkLocator interface {
	ProjectChunkRecords(ctx context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]ChunkRecord, error)
}

// DefaultChunkLocationCache bounds the chunk locations a pack-aware reader
// keeps per server process.
const DefaultChunkLocationCache = 100_000

// PackedReads makes reads of the projects served by stores (browser, diff,
// blame, merges and signed-path downloads) find packed chunks: the server's
// own store, when it holds packs (storage.PackStore), is wrapped in a reader
// that looks up each chunk's row and reads its pack range or its loose
// object. Own-storage (External) projects and stores without packs are
// served unchanged, so the free server is not affected.
func PackedReads(stores ChunkStores, locator chunkLocator) ChunkStores {
	return &packedStores{inner: stores, locator: locator, cache: newLocationCache(DefaultChunkLocationCache)}
}

type packedStores struct {
	inner   ChunkStores
	locator chunkLocator
	cache   *locationCache
}

func (p *packedStores) ForProject(ctx context.Context, projectID snow.ID) (ProjectStore, error) {
	resolved, err := p.inner.ForProject(ctx, projectID)
	if err != nil || resolved.External {
		return resolved, err
	}
	packs, ok := resolved.Store.(storage.PackStore)
	if !ok {
		return resolved, nil
	}
	resolved.Store = &packedStore{raw: resolved.Store, packs: packs, locator: p.locator, cache: p.cache}
	return resolved, nil
}

// unwrapStore returns the store a pack-aware reader wraps, or store itself:
// writes, presigns and confirm read-backs work on the raw store.
func unwrapStore(store storage.ChunkStore) storage.ChunkStore {
	if wrapped, ok := store.(*packedStore); ok {
		return wrapped.raw
	}
	return store
}

// packedStore reads one store's chunks by their row's location: a range of
// the pack for packed rows, the per-project key for loose ones (and for
// hashes without a row). Every read is checked against the chunk's BLAKE3
// hash, so an index bug or a corrupt object is an error instead of wrong
// content. Locations are cached; a cached location that fails (the object is
// gone or the bytes do not match, e.g. after compaction moved the chunk) is
// looked up again once.
type packedStore struct {
	raw     storage.ChunkStore
	packs   storage.PackStore
	locator chunkLocator
	cache   *locationCache
}

var _ storage.ChunkStore = (*packedStore)(nil)
var _ storage.ChunkOpener = (*packedStore)(nil)

func (s *packedStore) Put(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	return s.raw.Put(ctx, projectID, hash, data)
}

func (s *packedStore) Get(ctx context.Context, projectID snow.ID, hash domain.Hash) ([]byte, error) {
	record, cached, err := s.locate(ctx, projectID, hash)
	if err != nil {
		return nil, err
	}
	data, err := s.read(ctx, projectID, hash, record)
	if err == nil || !cached {
		return data, err
	}
	// The cached location may be stale: look it up again and retry once if
	// the row moved.
	s.cache.forget(projectID, hash)
	fresh, _, lookupErr := s.locate(ctx, projectID, hash)
	if lookupErr != nil {
		return nil, lookupErr
	}
	if fresh == record {
		return nil, err
	}
	return s.read(ctx, projectID, hash, fresh)
}

// Open reads the chunk whole (it must be hashed before anyone sees it) and
// serves it from memory.
func (s *packedStore) Open(ctx context.Context, projectID snow.ID, hash domain.Hash) (io.ReadCloser, error) {
	data, err := s.Get(ctx, projectID, hash)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *packedStore) Size(ctx context.Context, projectID snow.ID, hash domain.Hash) (int64, error) {
	record, _, err := s.locate(ctx, projectID, hash)
	if err != nil {
		return 0, err
	}
	if record.Location.Packed() {
		return record.SizeBytes, nil
	}
	return s.raw.Size(ctx, projectID, hash)
}

// Exists answers from the pack row for a packed chunk (no store request) and
// asks the store for a loose one.
func (s *packedStore) Exists(ctx context.Context, projectID snow.ID, hash domain.Hash) (bool, error) {
	records, err := s.locator.ProjectChunkRecords(ctx, projectID, []domain.Hash{hash})
	if err != nil {
		return false, err
	}
	record, ok := records[hash]
	if ok && record.Location.Packed() {
		return record.Readable(), nil
	}
	return s.raw.Exists(ctx, projectID, hash)
}

// Close does nothing: the wrapped store is shared and closed by its owner.
func (s *packedStore) Close() error {
	return nil
}

// Prefetch looks up the locations of many chunks in one query, so the reads
// that follow hit the cache.
func (s *packedStore) Prefetch(ctx context.Context, projectID snow.ID, hashes []domain.Hash) error {
	missing := make([]domain.Hash, 0, len(hashes))
	for _, hash := range hashes {
		if _, ok := s.cache.get(projectID, hash); !ok {
			missing = append(missing, hash)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	records, err := s.locator.ProjectChunkRecords(ctx, projectID, uniqueHashes(missing))
	if err != nil {
		return err
	}
	for hash, record := range records {
		s.cache.put(projectID, hash, record)
	}
	return nil
}

// locate returns the chunk's row (a zero record, read loose, when it has
// none) and whether it came from the cache.
func (s *packedStore) locate(ctx context.Context, projectID snow.ID, hash domain.Hash) (ChunkRecord, bool, error) {
	if record, ok := s.cache.get(projectID, hash); ok {
		return record, true, nil
	}
	records, err := s.locator.ProjectChunkRecords(ctx, projectID, []domain.Hash{hash})
	if err != nil {
		return ChunkRecord{}, false, err
	}
	record, ok := records[hash]
	if ok {
		s.cache.put(projectID, hash, record)
	}
	return record, false, nil
}

func (s *packedStore) read(ctx context.Context, projectID snow.ID, hash domain.Hash, record ChunkRecord) ([]byte, error) {
	var data []byte
	var err error
	if record.Location.Packed() {
		data, err = s.packs.GetRange(ctx, storage.PackKey(projectID, record.Location.PackID), record.Location.Offset, record.SizeBytes)
	} else {
		data, err = s.raw.Get(ctx, projectID, hash)
	}
	if err != nil {
		return nil, err
	}
	if chunker.Sum(data) != hash {
		return nil, domain.NewErrorInternalServer(fmt.Sprintf("stored content of chunk %s does not match its hash", hash))
	}
	return data, nil
}

// locationCache is a bounded LRU of chunk rows keyed by project and hash.
type locationCache struct {
	mu      sync.Mutex
	max     int
	entries map[locationKey]*list.Element
	order   *list.List
}

type locationKey struct {
	project snow.ID
	hash    domain.Hash
}

type locationEntry struct {
	key    locationKey
	record ChunkRecord
}

func newLocationCache(max int) *locationCache {
	return &locationCache{max: max, entries: map[locationKey]*list.Element{}, order: list.New()}
}

func (c *locationCache) get(project snow.ID, hash domain.Hash) (ChunkRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[locationKey{project, hash}]
	if !ok {
		return ChunkRecord{}, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*locationEntry).record, true
}

func (c *locationCache) put(project snow.ID, hash domain.Hash, record ChunkRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := locationKey{project, hash}
	if elem, ok := c.entries[key]; ok {
		elem.Value.(*locationEntry).record = record
		c.order.MoveToFront(elem)
		return
	}
	c.entries[key] = c.order.PushFront(&locationEntry{key: key, record: record})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*locationEntry).key)
	}
}

func (c *locationCache) forget(project snow.ID, hash domain.Hash) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[locationKey{project, hash}]; ok {
		c.order.Remove(elem)
		delete(c.entries, locationKey{project, hash})
	}
}
