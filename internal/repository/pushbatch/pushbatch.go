// Package pushbatch writes the files of a push in a few bulk statements instead
// of one statement per chunk, file and link. The sqlite and postgres push
// repositories share it and only provide the three statements in Store, so both
// editions keep the same ordering, limits and error behaviour.
package pushbatch

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/usecase"
)

var (
	// lookupBatch bounds the hashes of one chunk lookup; sqlite expands them
	// into bound variables.
	lookupBatch = 500
	// fileBatch and linkBatch bound the rows of one bulk insert, which keeps a
	// single statement a moderate size on very large pushes.
	fileBatch = 5000
	linkBatch = 20000
)

// FileKey identifies an inserted file: names are unique within a tree node.
type FileKey struct {
	TreeID int64
	Name   string
}

// Store runs the bulk statements of one dialect inside the push transaction.
type Store interface {
	// ChunkIDs returns the ids of the chunks the project holds, for the hashes
	// that exist; unknown or unlinked hashes are simply absent.
	ChunkIDs(ctx context.Context, hashes [][]byte) (map[domain.Hash]int64, error)
	// InsertFiles inserts the JSON array of file rows and returns their ids.
	InsertFiles(ctx context.Context, payload []byte) (map[FileKey]int64, error)
	// InsertLinks inserts the JSON array of [file_id, chunk_id, chunk_index].
	InsertLinks(ctx context.Context, payload []byte) error
}

type fileRow struct {
	Name      string `json:"name"`
	Mode      int64  `json:"mode"`
	TreeID    int64  `json:"tree_id"`
	Hash      string `json:"hash"`
	SizeBytes int64  `json:"size_bytes"`
	IsBinary  bool   `json:"is_binary"`
	Encoding  string `json:"encoding"`
}

// ResolveChunks looks up every chunk the files reference in as few queries as
// possible and fails with the first missing hash in file order, before anything
// is written.
func ResolveChunks(ctx context.Context, store Store, files []usecase.PushFileRow) (map[domain.Hash]int64, error) {
	var distinct [][]byte
	seen := map[domain.Hash]struct{}{}
	for _, f := range files {
		for _, hash := range f.ChunkHashes {
			if _, ok := seen[hash]; ok {
				continue
			}
			seen[hash] = struct{}{}
			distinct = append(distinct, hash.Bytes())
		}
	}

	ids := make(map[domain.Hash]int64, len(distinct))
	for start := 0; start < len(distinct); start += lookupBatch {
		end := min(start+lookupBatch, len(distinct))
		found, err := store.ChunkIDs(ctx, distinct[start:end])
		if err != nil {
			return nil, err
		}
		for hash, id := range found {
			ids[hash] = id
		}
	}

	for _, f := range files {
		for _, hash := range f.ChunkHashes {
			if _, ok := ids[hash]; !ok {
				return nil, domain.NewErrorUser("missing chunk data for " + hash.String())
			}
		}
	}
	return ids, nil
}

// InsertFiles inserts the files and then their chunk links. nodeIDs maps the
// logical tree ids of the request to real node ids and chunkIDs comes from
// ResolveChunks.
func InsertFiles(ctx context.Context, store Store, files []usecase.PushFileRow, nodeIDs map[int64]int64, chunkIDs map[domain.Hash]int64) error {
	fileIDs := make([]int64, len(files))
	for start := 0; start < len(files); start += fileBatch {
		end := min(start+fileBatch, len(files))
		rows := make([]fileRow, 0, end-start)
		for _, f := range files[start:end] {
			rows = append(rows, fileRow{
				Name:      f.Name,
				Mode:      int64(f.Mode),
				TreeID:    nodeIDs[f.TreeID],
				Hash:      hex.EncodeToString(f.Hash.Bytes()),
				SizeBytes: f.SizeBytes,
				IsBinary:  f.IsBinary,
				Encoding:  f.Encoding,
			})
		}
		payload, err := json.Marshal(rows)
		if err != nil {
			return domain.NewErrorInternalServer(err.Error())
		}
		ids, err := store.InsertFiles(ctx, payload)
		if err != nil {
			return err
		}
		for i, f := range files[start:end] {
			id, ok := ids[FileKey{TreeID: nodeIDs[f.TreeID], Name: f.Name}]
			if !ok {
				return domain.NewErrorDatabase(fmt.Sprintf("inserted file %q was not returned", f.Name))
			}
			fileIDs[start+i] = id
		}
	}

	links := make([][3]int64, 0, min(linkBatch, len(files)))
	flush := func() error {
		if len(links) == 0 {
			return nil
		}
		payload, err := json.Marshal(links)
		if err != nil {
			return domain.NewErrorInternalServer(err.Error())
		}
		links = links[:0]
		return store.InsertLinks(ctx, payload)
	}
	for i, f := range files {
		for index, hash := range f.ChunkHashes {
			links = append(links, [3]int64{fileIDs[i], chunkIDs[hash], int64(index)})
			if len(links) >= linkBatch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	return flush()
}
