package pushbatch

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"slices"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/usecase"
)

// chunkRowBatch bounds the rows of one chunk-row or link insert at confirm.
var chunkRowBatch = 1000

// ChunkRowInserter runs one dialect's bulk statement over a JSON payload.
type ChunkRowInserter func(ctx context.Context, payload []byte) error

// InsertChunkRows inserts the chunk metadata rows confirm recorded, as JSON
// arrays of ["<hex hash>", size] in batches of chunkRowBatch. Duplicates are
// dropped (the first size wins) and rows are sorted by hash, so concurrent
// confirms with overlapping hashes take the unique-index locks in the same
// order and cannot deadlock on postgres.
func InsertChunkRows(ctx context.Context, chunks []usecase.ChunkRef, insert ChunkRowInserter) error {
	seen := make(map[domain.Hash]struct{}, len(chunks))
	rows := make([]usecase.ChunkRef, 0, len(chunks))
	for _, chunk := range chunks {
		if _, ok := seen[chunk.Hash]; ok {
			continue
		}
		seen[chunk.Hash] = struct{}{}
		rows = append(rows, chunk)
	}
	slices.SortFunc(rows, func(a, b usecase.ChunkRef) int { return bytes.Compare(a.Hash[:], b.Hash[:]) })
	for start := 0; start < len(rows); start += chunkRowBatch {
		end := min(start+chunkRowBatch, len(rows))
		batch := make([][2]any, 0, end-start)
		for _, row := range rows[start:end] {
			batch = append(batch, [2]any{hex.EncodeToString(row.Hash[:]), row.SizeBytes})
		}
		if err := insertJSON(ctx, batch, insert); err != nil {
			return err
		}
	}
	return nil
}

// InsertHashes passes the distinct hashes, sorted, as JSON arrays of hex
// strings in batches of chunkRowBatch; the project links at confirm use it.
func InsertHashes(ctx context.Context, hashes []domain.Hash, insert ChunkRowInserter) error {
	sorted := slices.Clone(hashes)
	slices.SortFunc(sorted, func(a, b domain.Hash) int { return bytes.Compare(a[:], b[:]) })
	sorted = slices.Compact(sorted)
	for start := 0; start < len(sorted); start += chunkRowBatch {
		end := min(start+chunkRowBatch, len(sorted))
		batch := make([]string, 0, end-start)
		for _, hash := range sorted[start:end] {
			batch = append(batch, hex.EncodeToString(hash[:]))
		}
		if err := insertJSON(ctx, batch, insert); err != nil {
			return err
		}
	}
	return nil
}

func insertJSON(ctx context.Context, batch any, insert ChunkRowInserter) error {
	payload, err := json.Marshal(batch)
	if err != nil {
		return domain.NewErrorInternalServer(err.Error())
	}
	return insert(ctx, payload)
}

// LookupBatches calls lookup with the hashes in batches of lookupBatch, for
// queries that expand the hashes into bound variables on sqlite.
func LookupBatches(hashes []domain.Hash, lookup func(batch [][]byte) error) error {
	for start := 0; start < len(hashes); start += lookupBatch {
		end := min(start+lookupBatch, len(hashes))
		batch := make([][]byte, 0, end-start)
		for _, hash := range hashes[start:end] {
			batch = append(batch, hash.Bytes())
		}
		if err := lookup(batch); err != nil {
			return err
		}
	}
	return nil
}
