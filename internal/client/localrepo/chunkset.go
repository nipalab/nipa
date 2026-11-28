package localrepo

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	sqlite "modernc.org/sqlite"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

// listsAnyChunkFunc is the SQL function FileListByChunkSet filters the files
// table with: nipa_lists_any_chunk(chunks, set_id) is 1 when the chunk list
// blob holds, on a hash boundary, a hash of the set registered under set_id
// (chunkSets), and 0 otherwise. An unknown set_id is an error, never a
// "no": a missed match would let a prune delete an object a file keeps.
const listsAnyChunkFunc = "nipa_lists_any_chunk"

var (
	// chunkSets holds the hash sets of the FileListByChunkSet queries in
	// flight, by id; each query registers its set and drops it after.
	chunkSets   sync.Map
	chunkSetIDs atomic.Int64
)

func init() {
	// Registered with the driver, so every connection opened afterwards has
	// it. Not deterministic: the answer depends on the registered set.
	sqlite.MustRegisterScalarFunction(listsAnyChunkFunc, 2, listsAnyChunkSQL)
}

func listsAnyChunkSQL(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	id, ok := args[1].(int64)
	if !ok {
		return nil, fmt.Errorf("%s: set id must be an integer", listsAnyChunkFunc)
	}
	set, ok := chunkSets.Load(id)
	if !ok {
		return nil, fmt.Errorf("%s: unknown chunk set %d", listsAnyChunkFunc, id)
	}
	var chunks []byte
	switch v := args[0].(type) {
	case []byte:
		chunks = v
	case string:
		chunks = []byte(v)
	case nil:
	default:
		return nil, fmt.Errorf("%s: chunk list must be a blob", listsAnyChunkFunc)
	}
	if listsAny(chunks, set.(map[serverDomain.Hash]struct{})) {
		return int64(1), nil
	}
	return int64(0), nil
}

// listsAny reports whether the chunk list blob holds one of want at a hash
// boundary.
func listsAny(chunks []byte, want map[serverDomain.Hash]struct{}) bool {
	for off := 0; off+chunkHashSize <= len(chunks); off += chunkHashSize {
		if _, ok := want[serverDomain.Hash(chunks[off:off+chunkHashSize])]; ok {
			return true
		}
	}
	return false
}

// FilesWithAnyChunk returns the tracked files whose chunk list holds at least
// one of hashes, sorted by path. It is FilesWithChunk for a set: there is no
// chunk index, so it is one scan of the files table whatever the number of
// hashes (FilesWithChunk costs one scan per hash), filtered inside SQLite by
// nipa_lists_any_chunk, so only the matching rows reach Go. A hash matches
// on a 32-byte boundary of the chunk list only, as FilesWithChunk re-checks.
func (l *LocalRepo) FilesWithAnyChunk(hashes []serverDomain.Hash) ([]domain.SnapshotFile, error) {
	if l.db == nil {
		return nil, errors.New("local repo not initialized")
	}
	if len(hashes) == 0 {
		return nil, nil
	}
	want := make(map[serverDomain.Hash]struct{}, len(hashes))
	for _, h := range hashes {
		want[h] = struct{}{}
	}
	id := chunkSetIDs.Add(1)
	chunkSets.Store(id, want)
	defer chunkSets.Delete(id)
	rows, err := sqlcLocalrepo.New(l.db).FileListByChunkSet(context.Background(), id)
	if err != nil {
		return nil, err
	}
	files := make([]domain.SnapshotFile, 0, len(rows))
	for _, r := range rows {
		f, err := snapshotFile(r.Path, r.Hash, r.SizeBytes, r.Mode, r.IsBinary, r.Encoding, r.Chunks)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}
