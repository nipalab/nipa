package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.opentelemetry.io/otel/attribute"

	"github.com/nipalab/nipa/internal/chunkbundle"
	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/chunkproof"
	"github.com/nipalab/nipa/internal/chunkurl"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

// ChunkContent is chunk content the server holds and is about to store.
type ChunkContent struct {
	Hash domain.Hash
	Data []byte
}

// storedChunk is one chunk storeChunks wrote and where it put it.
type storedChunk struct {
	hash     domain.Hash
	size     int64
	location chunkproof.Location
}

// storeChunks is the one place chunk bytes reach a project's store from the
// server: bundle uploads (StoreBundle) and server-generated content (merges,
// StoreChunks) both go through it, so the storage layout changes here only.
// The content must already be verified against its hash. No row and no quota
// is touched: confirm (bundles) or AttributeProjectChunks (server content)
// records them.
//
// The garbage collector's marks on the project's chunks are cleared first, so
// a sweep cannot remove content between this write and the confirm that
// trusts it. Each chunk is then written as its own (loose) object: a store
// clients can also write directly (storage.ChunkOverwriter, S3) gets an
// unconditional single-part Overwrite, which replaces garbage an earlier
// direct upload left at the key; other stores get Put, which skips content
// they already hold.
func (c *Chunk) storeChunks(ctx context.Context, store storage.ChunkStore, projectID snow.ID, chunks []ChunkContent) ([]storedChunk, error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	hashes := make([]domain.Hash, 0, len(chunks))
	for _, chunk := range chunks {
		hashes = append(hashes, chunk.Hash)
	}
	if err := c.protect(ctx, projectID, hashes...); err != nil {
		return nil, err
	}
	stored := make([]storedChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if err := c.writeLoose(ctx, store, projectID, chunk); err != nil {
			return nil, err
		}
		stored = append(stored, storedChunk{hash: chunk.Hash, size: int64(len(chunk.Data)), location: chunkproof.Loose()})
	}
	return stored, nil
}

func (c *Chunk) writeLoose(ctx context.Context, store storage.ChunkStore, projectID snow.ID, chunk ChunkContent) error {
	var err error
	if overwriter, ok := store.(storage.ChunkOverwriter); ok {
		err = overwriter.Overwrite(ctx, projectID, chunk.Hash, chunk.Data)
	} else {
		err = store.Put(ctx, projectID, chunk.Hash, chunk.Data)
	}
	if err != nil {
		c.metrics.ObserveChunkOp("put", "error")
		return domain.NewErrorDatabase(fmt.Sprintf("store chunk %s: %v", chunk.Hash, err))
	}
	c.metrics.ObserveChunkOp("put", "stored")
	c.metrics.ObserveChunkBytes("put", int64(len(chunk.Data)))
	return nil
}

// StoreChunks verifies and stores server-held content (merge output) in the
// project's store without recording rows; AttributeProjectChunks records them
// once the caller is ready to land them. A chunk whose content does not hash
// to its hash fails the whole call before anything is written.
func (c *Chunk) StoreChunks(ctx context.Context, projectID snow.ID, chunks []ChunkContent) error {
	for _, chunk := range chunks {
		if got := chunker.Sum(chunk.Data); got != chunk.Hash {
			c.metrics.ObserveChunkVerifyFailure("store")
			return domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", chunk.Hash))
		}
	}
	resolved, err := c.writableStore(ctx, projectID)
	if err != nil {
		return err
	}
	_, err = c.storeChunks(ctx, resolved.Store, projectID, chunks)
	return err
}

// StoreForProject stores one chunk of server-held content (StoreChunks).
func (c *Chunk) StoreForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	return c.StoreChunks(ctx, projectID, []ChunkContent{{Hash: hash, Data: data}})
}

// UploadForProject stores server-held content in the project's store and
// records its row (through the ledger) so a push of that project may
// reference it.
func (c *Chunk) UploadForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	if err := c.StoreForProject(ctx, projectID, hash, data); err != nil {
		return err
	}
	return c.AttributeProjectChunks(ctx, projectID, []LedgerChunk{{Hash: hash, SizeBytes: int64(len(data))}})
}

// VerifyBundleURL checks that a signed bundle URL was issued for the project
// with these limits and has not expired.
func (c *Chunk) VerifyBundleURL(org, project string, params chunkurl.BundleParams) error {
	return chunkurl.VerifyBundle(c.transfer.SigningKey, org, project, params, c.now())
}

// StoreBundle reads a bundle upload body of exactly size bytes (at most
// maxCount entries, chunkbundle format), verifies every entry's BLAKE3 hash
// and length, and only then stores the entries in the project's store
// (storeChunks; a repeated hash is stored once, the first copy wins). Any bad
// entry, bad length, truncation or limit breach rejects the whole bundle with
// a user error and nothing is written.
//
// It returns one receipt per stored chunk (chunkproof.NewReceipt) for the
// project, hash, the size the server measured and the location, valid for
// the presign TTL: the server saw these bytes itself, so confirm accepts the
// receipt instead of reading them back. No row and no quota is touched here.
// The caller holds the body's bytes in the upload budget
// (ReserveUploadBuffer).
func (c *Chunk) StoreBundle(ctx context.Context, projectID snow.ID, body io.Reader, size int64, maxCount int) (receipts []ChunkReceipt, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.store_bundle",
		attribute.Int64(obs.AttrChunkBytes, size))
	defer func() { obs.FinishSpan(span, err) }()

	resolved, err := c.writableStore(ctx, projectID)
	if err != nil {
		return nil, err
	}
	entries, err := chunkbundle.DecodeAll(body, chunkbundle.Limits{Size: size, MaxCount: maxCount})
	if errors.Is(err, chunkbundle.ErrInvalid) {
		c.metrics.ObserveChunkVerifyFailure("store")
		return nil, domain.NewErrorUser(err.Error())
	}
	if err != nil {
		return nil, domain.NewErrorUser("unable to read chunk bundle body")
	}
	span.SetAttributes(attribute.Int(obs.AttrChunkCount, len(entries)))
	chunks := make([]ChunkContent, 0, len(entries))
	for _, entry := range entries {
		chunks = append(chunks, ChunkContent(entry))
	}
	stored, err := c.storeChunks(ctx, resolved.Store, projectID, chunks)
	if err != nil {
		return nil, err
	}
	expires := c.now().Add(c.transfer.PresignTTL)
	receipts = make([]ChunkReceipt, 0, len(stored))
	for _, chunk := range stored {
		receipts = append(receipts, ChunkReceipt{
			Hash:    chunk.hash,
			Receipt: chunkproof.NewReceipt(c.transfer.SigningKey, projectID, chunk.hash, chunk.size, chunk.location, expires),
		})
	}
	return receipts, nil
}
