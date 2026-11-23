package pushbatch

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/usecase"
)

func withChunkRowBatch(t *testing.T, n int) {
	t.Helper()
	prev := chunkRowBatch
	chunkRowBatch = n
	t.Cleanup(func() { chunkRowBatch = prev })
}

func testHash(i int) domain.Hash {
	var h domain.Hash
	copy(h[:], fmt.Sprintf("%032d", 1000-i)) // descending input order
	return h
}

func TestInsertChunkRows_SortsDedupesAndBatches(t *testing.T) {
	withChunkRowBatch(t, 3)
	var refs []usecase.ChunkRef
	for i := 0; i < 7; i++ {
		refs = append(refs, usecase.ChunkRef{Hash: testHash(i), SizeBytes: int64(i + 1)})
	}
	refs = append(refs, usecase.ChunkRef{Hash: testHash(2), SizeBytes: 99})
	chunks := LooseRows(refs)

	var batches [][][]any
	require.NoError(t, InsertChunkRows(context.Background(), chunks, func(_ context.Context, payload []byte) error {
		var rows [][]any
		require.NoError(t, json.Unmarshal(payload, &rows))
		batches = append(batches, rows)
		return nil
	}))

	require.Len(t, batches, 3, "7 distinct rows in batches of 3")
	var prev []byte
	seen := 0
	for _, batch := range batches {
		require.LessOrEqual(t, len(batch), 3)
		for _, row := range batch {
			raw, err := hex.DecodeString(row[0].(string))
			require.NoError(t, err)
			require.Positive(t, bytes.Compare(raw, prev), "rows are sorted by hash across batches")
			prev = raw
			var h domain.Hash
			copy(h[:], raw)
			if h == testHash(2) {
				require.Equal(t, float64(3), row[1], "the first size of a duplicate wins")
			}
			seen++
		}
	}
	require.Equal(t, 7, seen)

	require.NoError(t, InsertChunkRows(context.Background(), nil, func(context.Context, []byte) error {
		t.Fatal("no statement for an empty batch")
		return nil
	}))
}

func TestInsertChunkRows_EncodesPackLocations(t *testing.T) {
	rows := []usecase.ChunkRow{
		{Hash: testHash(1), SizeBytes: 10},
		{Hash: testHash(2), SizeBytes: 20, Location: usecase.ChunkLocation{PackID: 1 << 60, Offset: 4}},
	}
	var payloads []string
	require.NoError(t, InsertChunkRows(context.Background(), rows, func(_ context.Context, payload []byte) error {
		payloads = append(payloads, string(payload))
		return nil
	}))
	require.Len(t, payloads, 1)
	// Sorted by hash: testHash(2) sorts first (descending input). The pack id
	// is written exactly, not as a float.
	require.Equal(t, fmt.Sprintf(`[["%s",20,%d,4],["%s",10]]`,
		hex.EncodeToString(rows[1].Hash[:]), int64(1<<60), hex.EncodeToString(rows[0].Hash[:])), payloads[0])
}

func TestInsertHashes_SortsDedupesAndBatches(t *testing.T) {
	withChunkRowBatch(t, 2)
	hashes := []domain.Hash{testHash(1), testHash(3), testHash(2), testHash(1), testHash(4)}

	var all []string
	calls := 0
	require.NoError(t, InsertHashes(context.Background(), hashes, func(_ context.Context, payload []byte) error {
		var batch []string
		require.NoError(t, json.Unmarshal(payload, &batch))
		require.LessOrEqual(t, len(batch), 2)
		all = append(all, batch...)
		calls++
		return nil
	}))
	require.Equal(t, 2, calls)
	require.Len(t, all, 4)
	for i := 1; i < len(all); i++ {
		require.Less(t, all[i-1], all[i])
	}
	require.Equal(t, testHash(1), hashes[0], "the caller's slice is not reordered")
}

func TestChunkRowInserterErrorsStop(t *testing.T) {
	withChunkRowBatch(t, 1)
	boom := errors.New("boom")
	calls := 0
	fail := func(context.Context, []byte) error { calls++; return boom }

	err := InsertChunkRows(context.Background(), []usecase.ChunkRow{{Hash: testHash(1)}, {Hash: testHash(2)}}, fail)
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, InsertHashes(context.Background(), []domain.Hash{testHash(1), testHash(2)}, fail), boom)
	require.Equal(t, 2, calls, "the first failing batch stops the rest")
}

func TestLookupBatches(t *testing.T) {
	prev := lookupBatch
	lookupBatch = 2
	t.Cleanup(func() { lookupBatch = prev })

	var sizes []int
	require.NoError(t, LookupBatches([]domain.Hash{testHash(1), testHash(2), testHash(3)}, func(batch [][]byte) error {
		sizes = append(sizes, len(batch))
		return nil
	}))
	require.Equal(t, []int{2, 1}, sizes)

	boom := errors.New("boom")
	require.ErrorIs(t, LookupBatches([]domain.Hash{testHash(1)}, func([][]byte) error { return boom }), boom)
}
