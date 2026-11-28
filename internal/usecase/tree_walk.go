package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
)

// changedTreeEntries returns, flattened per side, the files that differ
// between two trees as the caller's filter sees them; a nil root is the empty
// tree. Files identical on both sides are left out of both maps, which does
// not change what diff.Compare reports (it only reports paths that differ),
// so the result is the same as comparing the two full manifests. The walk
// reads only the directories whose stored hashes differ, level by level with
// batched reads, and chunk lists only for the files it returns (see
// compareTrees).
func (b *Branch) changedTreeEntries(ctx context.Context, base, head *domain.TreeNode, filter *PathFilter) (baseEntries, headEntries map[string]diff.Entry, err error) {
	changed, err := compareTrees(ctx, b.branchRepo, treeView{filter: filter}, []*domain.TreeNode{base, head}, nil)
	if err != nil {
		return nil, nil, err
	}
	baseEntries = map[string]diff.Entry{}
	headEntries = map[string]diff.Entry{}
	for _, file := range changed {
		if file.files[0] != nil {
			diff.AddFileEntries(baseEntries, file.dir, file.files[:1])
		}
		if file.files[1] != nil {
			diff.AddFileEntries(headEntries, file.dir, file.files[1:])
		}
	}
	return baseEntries, headEntries, nil
}

// treeVisit identifies a directory by where it sits and what it holds: the
// same subtree can be visible at one path and hidden at another.
type treeVisit struct {
	path string
	hash domain.Hash
}

// collectVisibleChunks adds the chunk hashes of every readable file below
// roots to seen. It walks all roots together, level by level: a level costs
// one file metadata batch, one chunk batch for the files the caller may read
// and one children batch, so the number of repository calls follows the tree
// depth instead of the directory count. Directories already recorded in
// visited (same path, same content) are skipped, which makes subtrees shared
// across commits free, and directories outside the filter or the sparse
// prefixes are never queried.
func (b *Branch) collectVisibleChunks(ctx context.Context, roots []*domain.TreeNode, filter *PathFilter, sparse domain.PrefixSet, seen map[domain.Hash]struct{}) error {
	visited := map[treeVisit]struct{}{}
	level := make([]manifestEntry, 0, len(roots))
	for _, root := range roots {
		level = append(level, manifestEntry{node: root})
	}
	for len(level) > 0 {
		next, err := b.collectVisibleLevel(ctx, level, filter, sparse, visited, seen)
		if err != nil {
			return err
		}
		level = next
	}
	return nil
}

// collectVisibleLevel reads one level of the visible-chunk walk and returns
// the directories of the next one.
func (b *Branch) collectVisibleLevel(ctx context.Context, level []manifestEntry, filter *PathFilter, sparse domain.PrefixSet, visited map[treeVisit]struct{}, seen map[domain.Hash]struct{}) ([]manifestEntry, error) {
	entries := level[:0:0]
	ids := make([]int64, 0, len(level))
	queued := make(map[int64]struct{}, len(level))
	for _, entry := range level {
		if entry.node.Hash == emptyTreeHash {
			continue
		}
		if entry.node.Hash != (domain.Hash{}) {
			key := treeVisit{path: entry.path, hash: entry.node.Hash}
			if _, ok := visited[key]; ok {
				continue
			}
			visited[key] = struct{}{}
		}
		entries = append(entries, entry)
		if _, ok := queued[entry.node.ID]; !ok {
			queued[entry.node.ID] = struct{}{}
			ids = append(ids, entry.node.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	files, err := b.branchRepo.ListFileMetaByTrees(ctx, ids)
	if err != nil {
		return nil, err
	}
	var fileIDs []int64
	wanted := map[int64]struct{}{}
	for _, entry := range entries {
		for _, file := range files[entry.node.ID] {
			if _, ok := wanted[file.ID]; ok {
				continue
			}
			filePath := joinTreePath(entry.path, file.Name)
			if !filter.Allow(filePath) {
				continue
			}
			if !sparse.Covers(filePath) && !keepRootTeamFile(entry.path, file.Name, sparse) {
				continue
			}
			wanted[file.ID] = struct{}{}
			fileIDs = append(fileIDs, file.ID)
		}
	}
	if len(fileIDs) > 0 {
		chunks, err := b.branchRepo.ListChunksByFiles(ctx, fileIDs)
		if err != nil {
			return nil, err
		}
		for _, list := range chunks {
			for _, chunk := range list {
				seen[chunk.Hash] = struct{}{}
			}
		}
	}

	children, err := b.branchRepo.ListTreeChildrenByTrees(ctx, ids)
	if err != nil {
		return nil, err
	}
	var next []manifestEntry
	for _, entry := range entries {
		for _, child := range children[entry.node.ID] {
			childPath := joinTreePath(entry.path, child.Name)
			if !filter.CanDescend(childPath) || !sparse.CanDescend(childPath) {
				continue
			}
			next = append(next, manifestEntry{node: child, path: childPath})
		}
	}
	return next, nil
}
