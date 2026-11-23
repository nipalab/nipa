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
	"github.com/nipalab/nipa/internal/storage/pack"
)

// ChunkContent is chunk content the server holds and is about to store.
type ChunkContent struct {
	Hash domain.Hash
	Data []byte
}

// storeChunks is the one place chunk bytes reach a project's store from the
// server: bundle uploads (StoreBundle) and server-generated content (merges,
// StoreChunks) both go through it, so the storage layout changes here only.
// The content must already be verified against its hash; a repeated hash is
// stored once. No chunk row and no quota is touched: confirm (bundles) or
// RecordStoredChunks (server content) records them at the returned
// locations.
//
// The garbage collector's marks on the project's chunks are cleared first, so
// a sweep cannot remove content between this write and the confirm that
// trusts it. Then:
//
//   - packs (WithPacks, the project on the server's own store and that store
//     a storage.PackStore): a pending chunk_packs row, one pack object with
//     every chunk (one store write), the row set stored. A failure after the
//     row insert leaves a pending row (with or without an object) that the
//     garbage collector removes; no location is returned for it.
//   - otherwise each chunk is its own (loose) object, written concurrently
//     under the server-wide write limit (WithStoreWriteLimit); the first
//     failure cancels the others. A store clients can also write directly
//     (storage.ChunkOverwriter, S3) gets an unconditional single-part
//     Overwrite, which replaces garbage an earlier direct upload left at the
//     key; other stores get Put, which skips content they already hold.
func (c *Chunk) storeChunks(ctx context.Context, resolved ProjectStore, projectID snow.ID, chunks []ChunkContent) ([]ChunkRow, error) {
	chunks = uniqueContent(chunks)
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
	store := unwrapStore(resolved.Store)
	if packs, ok := store.(storage.PackStore); ok && c.packs && !resolved.External {
		return c.writePack(ctx, packs, projectID, chunks)
	}
	return c.writeLooseAll(ctx, store, projectID, chunks)
}

// uniqueContent drops repeated hashes, keeping the first copy.
func uniqueContent(chunks []ChunkContent) []ChunkContent {
	seen := make(map[domain.Hash]struct{}, len(chunks))
	out := make([]ChunkContent, 0, len(chunks))
	for _, chunk := range chunks {
		if _, ok := seen[chunk.Hash]; ok {
			continue
		}
		seen[chunk.Hash] = struct{}{}
		out = append(out, chunk)
	}
	return out
}

// writePack stores the chunks as one pack object of the project.
func (c *Chunk) writePack(ctx context.Context, packs storage.PackStore, projectID snow.ID, chunks []ChunkContent) (stored []ChunkRow, err error) {
	ctx, span := obs.StartSpan(ctx, "chunk.write_pack",
		attribute.Int(obs.AttrChunkCount, len(chunks)))
	defer func() { obs.FinishSpan(span, err) }()

	entries := make([]pack.Entry, 0, len(chunks))
	for _, chunk := range chunks {
		entries = append(entries, pack.Entry(chunk))
	}
	built, err := pack.Build(entries)
	if err != nil {
		return nil, domain.NewErrorInternalServer(fmt.Sprintf("build chunk pack: %v", err))
	}
	packID := c.packIDs.Generate()
	span.SetAttributes(attribute.String("nipa.chunk.pack", packID.Base36()), attribute.Int64(obs.AttrChunkBytes, built.Size()))
	if err := c.chunkRepo.InsertChunkPack(ctx, ChunkPack{ID: packID, ProjectID: projectID, SizeBytes: built.Size(), ChunkCount: len(chunks)}); err != nil {
		return nil, err
	}
	if err := packs.PutPack(ctx, storage.PackKey(projectID, packID), built.Reader(), built.Size()); err != nil {
		c.metrics.ObserveChunkOp("put_pack", "error")
		return nil, domain.NewErrorDatabase(fmt.Sprintf("store chunk pack %s: %v", packID.Base36(), err))
	}
	if err := c.chunkRepo.SetChunkPackState(ctx, packID, ChunkPackStored); err != nil {
		return nil, err
	}
	c.metrics.ObserveChunkOp("put_pack", "stored")
	c.metrics.ObserveChunkBytes("put_pack", built.Size())
	stored = make([]ChunkRow, 0, len(chunks))
	for _, entry := range built.Index() {
		stored = append(stored, ChunkRow{
			Hash:      entry.Hash,
			SizeBytes: entry.Length,
			Location:  ChunkLocation{PackID: packID, Offset: entry.Offset},
		})
	}
	return stored, nil
}

// writeLooseAll writes every chunk as its own object, at most the server-wide
// write limit at once.
func (c *Chunk) writeLooseAll(ctx context.Context, store storage.ChunkStore, projectID snow.ID, chunks []ChunkContent) ([]ChunkRow, error) {
	all := func(int) bool { return true }
	err := fanOut(ctx, c.writeSlots, len(chunks), all, func(ctx context.Context, i int) error {
		return c.writeLoose(ctx, store, projectID, chunks[i])
	})
	if err != nil {
		return nil, err
	}
	stored := make([]ChunkRow, 0, len(chunks))
	for _, chunk := range chunks {
		stored = append(stored, ChunkRow{Hash: chunk.Hash, SizeBytes: int64(len(chunk.Data))})
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
// project's store without recording rows, and returns where it put each
// chunk; RecordStoredChunks records them once the caller is ready to land
// them. A chunk whose content does not hash to its hash fails the whole call
// before anything is written.
func (c *Chunk) StoreChunks(ctx context.Context, projectID snow.ID, chunks []ChunkContent) ([]ChunkRow, error) {
	for _, chunk := range chunks {
		if got := chunker.Sum(chunk.Data); got != chunk.Hash {
			c.metrics.ObserveChunkVerifyFailure("store")
			return nil, domain.NewErrorUser(fmt.Sprintf("chunk hash mismatch for %s", chunk.Hash))
		}
	}
	resolved, err := c.writableStore(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return c.storeChunks(ctx, resolved, projectID, chunks)
}

// UploadForProject stores server-held content in the project's store and
// records its row (through the ledger) so a push of that project may
// reference it.
func (c *Chunk) UploadForProject(ctx context.Context, projectID snow.ID, hash domain.Hash, data []byte) error {
	stored, err := c.StoreChunks(ctx, projectID, []ChunkContent{{Hash: hash, Data: data}})
	if err != nil {
		return err
	}
	return c.RecordStoredChunks(ctx, projectID, stored)
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
	stored, err := c.storeChunks(ctx, resolved, projectID, chunks)
	if err != nil {
		return nil, err
	}
	expires := c.now().Add(c.transfer.PresignTTL)
	receipts = make([]ChunkReceipt, 0, len(stored))
	for _, chunk := range stored {
		receipts = append(receipts, ChunkReceipt{
			Hash:    chunk.Hash,
			Receipt: chunkproof.NewReceipt(c.transfer.SigningKey, projectID, chunk.Hash, chunk.SizeBytes, chunk.Location.proof(), expires),
		})
	}
	return receipts, nil
}
