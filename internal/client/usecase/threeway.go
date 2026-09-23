package usecase

import (
	"bytes"
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/merge"
	serverDomain "github.com/nipalab/nipa/internal/domain"
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

func applyThreeWay(ctx context.Context, client chunkDownloader, local threeWayLocalRepo, root string, ours map[string]merge.File, baseByPath map[string]domain.SnapshotFile, res *merge.Result, scope domain.ChunkScope) (*threeWayResult, error) {
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

	for _, p := range sortedEntryPaths(res.Entries) {
		e := res.Entries[p]
		switch e.Decision {
		case merge.KeepOurs:
			out.Files[p] = e.Ours

		case merge.KeepTheirs:
			if err := materializeFile(root, toMaterialized(e.Theirs), local.OpenChunk); err != nil {
				return nil, err
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
			mf, err := storeMergedFile(local, p, merged, e.Ours.Mode, e.Ours.IsBinary || e.Theirs.IsBinary)
			if err != nil {
				return nil, err
			}
			if err := materializeFile(root, toMaterialized(mf), local.OpenChunk); err != nil {
				return nil, err
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
		removed, err := guardedRemove(root, base)
		if err != nil {
			return nil, fmt.Errorf("remove %s: %w", p, err)
		}
		if removed {
			out.Deleted = append(out.Deleted, p)
			out.Staged = append(out.Staged, p)
		}
	}

	for _, p := range out.Staged {
		if err := local.StageAdd(p); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func storeMergedFile(local threeWayLocalRepo, path string, data []byte, mode int, isBinary bool) (merge.File, error) {
	var hashes []serverDomain.Hash
	var sizes []int64
	batch := make([]*serverDomain.ChunkData, 0, 16)
	err := chunker.Scan(bytes.NewReader(data), func(c chunker.Chunk) error {
		hashes = append(hashes, c.Hash)
		sizes = append(sizes, int64(len(c.Data)))
		batch = append(batch, &serverDomain.ChunkData{Hash: c.Hash, Data: c.Data})
		return nil
	}, chunker.ConfigForFile(path, chunker.IsBinary(data)))
	if err != nil {
		return merge.File{}, err
	}
	if err := local.StoreChunks(batch); err != nil {
		return merge.File{}, err
	}
	return merge.File{
		Path:        path,
		Mode:        mode,
		SizeBytes:   int64(len(data)),
		IsBinary:    isBinary || chunker.IsBinary(data),
		Hash:        chunker.FileHash(hashes),
		ChunkHashes: hashes,
		ChunkSizes:  sizes,
	}, nil
}
