package usecase

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
)

type threeWayLocalRepo interface {
	workingCopyLocalRepo
	StageAdd(path string) error
}

type threeWayResult struct {
	Files      map[string]merge.File
	Staged     []string
	Deleted    []string
	Conflicted []string
}

// applyThreeWay decides and (when write is true) materializes the merge
// result. A dry-run (write false) computes the exact resulting file map and
// conflicts in memory, downloading missing content if needed but touching
// neither the working copy nor the local metadata.
func applyThreeWay(ctx context.Context, client chunkDownloader, local threeWayLocalRepo, root string, ours map[string]merge.File, baseByPath map[string]domain.SnapshotFile, res *merge.Result, scope domain.ChunkScope, write bool) (*threeWayResult, error) {
	var needs []merge.File
	for _, e := range res.Entries {
		switch e.Decision {
		case merge.KeepTheirs:
			needs = append(needs, e.Theirs)
		case merge.TextMerge:
			needs = append(needs, e.Base, e.Ours, e.Theirs)
		}
	}
	var want []serverDomain.Hash
	for _, f := range needs {
		want = append(want, f.ChunkHashes...)
	}
	missing, err := local.MissingChunks(want)
	if err != nil {
		return nil, err
	}
	if err := downloadMissing(ctx, client, local, scope, missing, estimatedBytes(needs, missing)); err != nil {
		return nil, err
	}

	out := &threeWayResult{Files: make(map[string]merge.File, len(ours))}
	for p, f := range ours {
		out.Files[p] = f
	}
	statEntries := make(map[string]domain.StatEntry)

	for _, p := range sortedEntryPaths(res.Entries) {
		e := res.Entries[p]
		switch e.Decision {
		case merge.KeepOurs:
			out.Files[p] = e.Ours

		case merge.KeepTheirs:
			if write {
				if err := materializeFile(root, toMaterialized(e.Theirs), local.OpenChunk); err != nil {
					return nil, err
				}
				if entry, err := statEntryFor(root, p, e.Theirs.Hash); err == nil {
					statEntries[p] = entry
				}
			}
			out.Files[p] = e.Theirs
			out.Staged = append(out.Staged, p)

		case merge.TextMerge:
			baseContent, err := loadFileContent(local.LoadChunk, e.Base)
			if err != nil {
				return nil, err
			}
			oursContent, err := loadFileContent(local.LoadChunk, e.Ours)
			if err != nil {
				return nil, err
			}
			theirsContent, err := loadFileContent(local.LoadChunk, e.Theirs)
			if err != nil {
				return nil, err
			}
			merged, wasConflict := merge.MergeText(baseContent, oursContent, theirsContent)
			var mf merge.File
			if write {
				mf, err = storeMergedFile(local, p, merged, e.Ours.Mode, e.Ours.IsBinary || e.Theirs.IsBinary)
				if err != nil {
					return nil, err
				}
				if err := materializeFile(root, toMaterialized(mf), local.OpenChunk); err != nil {
					return nil, err
				}
				if entry, err := statEntryFor(root, p, mf.Hash); err == nil {
					statEntries[p] = entry
				}
			} else {
				var batch []*serverDomain.ChunkData
				mf, batch, err = encodeMergedFile(p, merged, e.Ours.Mode, e.Ours.IsBinary || e.Theirs.IsBinary)
				if err != nil {
					return nil, err
				}
				// Cache the merged chunks so a chained dry-run (a later target
				// merging the same path) can reload them instead of asking the
				// server for content that was never uploaded.
				if err := local.StoreChunks(batch); err != nil {
					return nil, err
				}
			}
			out.Files[p] = mf
			out.Staged = append(out.Staged, p)
			if wasConflict {
				out.Conflicted = append(out.Conflicted, p)
			}

		case merge.BinaryConflict, merge.AddAddConflict:
			out.Files[p] = e.Ours
			out.Conflicted = append(out.Conflicted, p)

		case merge.ModifyDeleteConflict:
			out.Files[p] = e.Ours
			out.Conflicted = append(out.Conflicted, p)

		case merge.DeleteModifyConflict:
			out.Conflicted = append(out.Conflicted, p)
		}
	}

	for _, p := range sortedStrings(res.Deleted) {
		delete(out.Files, p)
		base, ok := baseByPath[p]
		if !ok {
			continue
		}
		if !write {
			out.Deleted = append(out.Deleted, p)
			out.Staged = append(out.Staged, p)
			continue
		}
		removed, err := guardedRemove(root, base)
		if err != nil {
			return nil, fmt.Errorf("remove %s: %w", p, err)
		}
		if removed {
			out.Deleted = append(out.Deleted, p)
			out.Staged = append(out.Staged, p)
		}
	}

	if !write {
		return out, nil
	}
	for _, p := range out.Staged {
		if err := local.StageAdd(p); err != nil {
			return nil, err
		}
	}
	if len(statEntries) > 0 {
		if err := local.SaveStatEntries(statEntries); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func encodeMergedFile(path string, data []byte, mode int, isBinary bool) (merge.File, []*serverDomain.ChunkData, error) {
	probeBinary := chunker.IsBinary(data)
	encoding, chunks, err := chunker.EncodeBytes(data, path, probeBinary, "")
	if err != nil {
		return merge.File{}, nil, err
	}
	var hashes []serverDomain.Hash
	var sizes []int64
	batch := make([]*serverDomain.ChunkData, 0, len(chunks))
	for _, c := range chunks {
		hashes = append(hashes, c.Hash)
		sizes = append(sizes, c.SizeBytes)
		batch = append(batch, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
	}
	return merge.File{
		Path:        path,
		Mode:        mode,
		SizeBytes:   int64(len(data)),
		IsBinary:    isBinary || probeBinary,
		Encoding:    encoding,
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
		ChunkSizes:  sizes,
	}, batch, nil
}

func storeMergedFile(local threeWayLocalRepo, path string, data []byte, mode int, isBinary bool) (merge.File, error) {
	file, batch, err := encodeMergedFile(path, data, mode, isBinary)
	if err != nil {
		return merge.File{}, err
	}
	if err := local.StoreChunks(batch); err != nil {
		return merge.File{}, err
	}
	return file, nil
}
