package pushbatch

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/usecase"
)

type fakeStore struct {
	chunks map[domain.Hash]int64

	lookups     [][][]byte
	fileBatches [][]fileRow
	linkBatches [][][3]int64

	nextFileID   int64
	dropFile     string
	lookupErr    error
	insertErr    error
	linkErr      error
	fileIDByKey  map[FileKey]int64
	failLookupAt int
}

func newFakeStore() *fakeStore {
	return &fakeStore{chunks: map[domain.Hash]int64{}, fileIDByKey: map[FileKey]int64{}}
}

func (s *fakeStore) ChunkIDs(_ context.Context, hashes [][]byte) (map[domain.Hash]int64, error) {
	s.lookups = append(s.lookups, hashes)
	if s.lookupErr != nil && (s.failLookupAt == 0 || len(s.lookups) == s.failLookupAt) {
		return nil, s.lookupErr
	}
	out := map[domain.Hash]int64{}
	for _, raw := range hashes {
		hash, err := domain.HashFromBytes(raw)
		if err != nil {
			return nil, err
		}
		if id, ok := s.chunks[hash]; ok {
			out[hash] = id
		}
	}
	return out, nil
}

func (s *fakeStore) InsertFiles(_ context.Context, payload []byte) (map[FileKey]int64, error) {
	if s.insertErr != nil {
		return nil, s.insertErr
	}
	var rows []fileRow
	if err := json.Unmarshal(payload, &rows); err != nil {
		return nil, err
	}
	s.fileBatches = append(s.fileBatches, rows)
	ids := map[FileKey]int64{}
	for _, row := range rows {
		if row.Name == s.dropFile {
			continue
		}
		s.nextFileID++
		key := FileKey{TreeID: row.TreeID, Name: row.Name}
		ids[key] = 1000 + s.nextFileID
		s.fileIDByKey[key] = ids[key]
	}
	return ids, nil
}

func (s *fakeStore) InsertLinks(_ context.Context, payload []byte) error {
	if s.linkErr != nil {
		return s.linkErr
	}
	var links [][3]int64
	if err := json.Unmarshal(payload, &links); err != nil {
		return err
	}
	s.linkBatches = append(s.linkBatches, links)
	return nil
}

func hashOf(i int) domain.Hash {
	var h domain.Hash
	binary.BigEndian.PutUint32(h[:4], uint32(i))
	return h
}

func withBatches(t *testing.T, lookup, files, links int) {
	t.Helper()
	oldLookup, oldFiles, oldLinks := lookupBatch, fileBatch, linkBatch
	lookupBatch, fileBatch, linkBatch = lookup, files, links
	t.Cleanup(func() { lookupBatch, fileBatch, linkBatch = oldLookup, oldFiles, oldLinks })
}

func pushFile(name string, treeID int64, chunks ...int) usecase.PushFileRow {
	row := usecase.PushFileRow{Name: name, Mode: 0o644, SizeBytes: int64(len(name)), Hash: hashOf(9000 + len(name)), TreeID: treeID, Encoding: "raw"}
	for _, c := range chunks {
		row.ChunkHashes = append(row.ChunkHashes, hashOf(c))
	}
	return row
}

func TestResolveChunks_BatchesDistinctHashes(t *testing.T) {
	withBatches(t, 3, 100, 100)
	store := newFakeStore()
	for i := 1; i <= 7; i++ {
		store.chunks[hashOf(i)] = int64(100 + i)
	}
	files := []usecase.PushFileRow{
		pushFile("a", 1, 1, 2, 3),
		pushFile("b", 1, 3, 4, 1),
		pushFile("c", 2, 5, 6, 7, 2),
	}

	ids, err := ResolveChunks(context.Background(), store, files)
	require.NoError(t, err)
	require.Len(t, ids, 7)
	for i := 1; i <= 7; i++ {
		require.Equal(t, int64(100+i), ids[hashOf(i)])
	}
	require.Len(t, store.lookups, 3, "seven distinct hashes in batches of three")
	require.Len(t, store.lookups[0], 3)
	require.Len(t, store.lookups[1], 3)
	require.Len(t, store.lookups[2], 1)
}

func TestResolveChunks_ReportsFirstMissingInFileOrder(t *testing.T) {
	withBatches(t, 2, 100, 100)
	store := newFakeStore()
	for _, i := range []int{1, 2, 3, 7} {
		store.chunks[hashOf(i)] = int64(i)
	}
	// 6 is missing from file "b" and 4 from file "c": file order, not hash or
	// batch order, decides which one is reported.
	files := []usecase.PushFileRow{pushFile("a", 1, 1, 2), pushFile("b", 1, 3, 6), pushFile("c", 1, 4, 7)}

	_, err := ResolveChunks(context.Background(), store, files)
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Contains(t, err.Error(), "missing chunk data for "+hashOf(6).String())
}

func TestResolveChunks_EmptyAndErrors(t *testing.T) {
	store := newFakeStore()
	ids, err := ResolveChunks(context.Background(), store, nil)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.Empty(t, store.lookups, "nothing to look up")

	ids, err = ResolveChunks(context.Background(), store, []usecase.PushFileRow{pushFile("empty", 1)})
	require.NoError(t, err)
	require.Empty(t, ids, "a file without chunks needs no lookup")

	boom := errors.New("db down")
	withBatches(t, 2, 100, 100)
	failing := newFakeStore()
	failing.chunks[hashOf(1)] = 1
	failing.lookupErr = boom
	failing.failLookupAt = 2
	_, err = ResolveChunks(context.Background(), failing, []usecase.PushFileRow{pushFile("a", 1, 1, 2, 3, 4)})
	require.ErrorIs(t, err, boom)
}

func TestInsertFiles_BatchesAndAssignsIDs(t *testing.T) {
	withBatches(t, 100, 2, 3)
	store := newFakeStore()
	nodeIDs := map[int64]int64{1: 11, 2: 22}
	chunkIDs := map[domain.Hash]int64{}
	for i := 1; i <= 5; i++ {
		chunkIDs[hashOf(i)] = int64(500 + i)
	}
	files := []usecase.PushFileRow{
		pushFile("a", 1, 1, 2),
		pushFile("b", 1),
		pushFile("c", 2, 3, 4, 5),
		pushFile("d", 2, 1),
		pushFile("e", 1, 5, 4),
	}
	files[2].IsBinary = true

	require.NoError(t, InsertFiles(context.Background(), store, files, nodeIDs, chunkIDs))

	require.Len(t, store.fileBatches, 3, "five files in batches of two")
	require.Len(t, store.fileBatches[0], 2)
	require.Len(t, store.fileBatches[2], 1)

	c := store.fileBatches[1][0]
	require.Equal(t, "c", c.Name)
	require.Equal(t, int64(0o644), c.Mode)
	require.Equal(t, int64(22), c.TreeID, "logical tree ids become real node ids")
	require.Equal(t, hex.EncodeToString(files[2].Hash.Bytes()), c.Hash)
	require.Equal(t, files[2].SizeBytes, c.SizeBytes)
	require.True(t, c.IsBinary)
	require.Equal(t, "raw", c.Encoding)

	var flat [][3]int64
	for _, batch := range store.linkBatches {
		require.LessOrEqual(t, len(batch), 3)
		flat = append(flat, batch...)
	}
	idOf := func(name string, tree int64) int64 { return store.fileIDByKey[FileKey{TreeID: tree, Name: name}] }
	require.Equal(t, [][3]int64{
		{idOf("a", 11), 501, 0}, {idOf("a", 11), 502, 1},
		{idOf("c", 22), 503, 0}, {idOf("c", 22), 504, 1}, {idOf("c", 22), 505, 2},
		{idOf("d", 22), 501, 0},
		{idOf("e", 11), 505, 0}, {idOf("e", 11), 504, 1},
	}, flat, "links follow file order and keep each file's chunk order")
	require.Len(t, store.linkBatches, 3, "eight links in batches of three")
}

func TestInsertFiles_NoFilesNoStatements(t *testing.T) {
	store := newFakeStore()
	require.NoError(t, InsertFiles(context.Background(), store, nil, nil, nil))
	require.Empty(t, store.fileBatches)
	require.Empty(t, store.linkBatches)

	require.NoError(t, InsertFiles(context.Background(), store, []usecase.PushFileRow{pushFile("a", 1)}, map[int64]int64{1: 1}, nil))
	require.Len(t, store.fileBatches, 1)
	require.Empty(t, store.linkBatches, "files without chunks produce no link statement")
}

func TestInsertFiles_Errors(t *testing.T) {
	boom := errors.New("db down")
	nodeIDs := map[int64]int64{1: 11}
	chunkIDs := map[domain.Hash]int64{hashOf(1): 1}
	files := []usecase.PushFileRow{pushFile("a", 1, 1), pushFile("b", 1, 1)}

	t.Run("file insert", func(t *testing.T) {
		store := newFakeStore()
		store.insertErr = boom
		require.ErrorIs(t, InsertFiles(context.Background(), store, files, nodeIDs, chunkIDs), boom)
		require.Empty(t, store.linkBatches)
	})

	t.Run("link insert", func(t *testing.T) {
		store := newFakeStore()
		store.linkErr = boom
		require.ErrorIs(t, InsertFiles(context.Background(), store, files, nodeIDs, chunkIDs), boom)
	})

	t.Run("a file the database did not return", func(t *testing.T) {
		store := newFakeStore()
		store.dropFile = "b"
		err := InsertFiles(context.Background(), store, files, nodeIDs, chunkIDs)
		var domErr *domain.Error
		require.ErrorAs(t, err, &domErr)
		require.Equal(t, 500, domErr.Code)
		require.Contains(t, domErr.InternalMessage, fmt.Sprintf("%q", "b"))
		require.Empty(t, store.linkBatches, "no links are written for an incomplete insert")
	})
}
