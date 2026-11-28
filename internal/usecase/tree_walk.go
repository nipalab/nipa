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

// collectVisibleChunks adds the chunk hashes of every readable file below node
// to seen. Directories already recorded in visited are skipped, which makes
// repeated work across commits that share subtrees free.
func (b *Branch) collectVisibleChunks(ctx context.Context, node *domain.TreeNode, path string, filter *PathFilter, sparse domain.PrefixSet, visited map[treeVisit]struct{}, seen map[domain.Hash]struct{}) error {
	key := treeVisit{path: path, hash: node.Hash}
	if node.Hash != (domain.Hash{}) {
		if _, ok := visited[key]; ok {
			return nil
		}
		visited[key] = struct{}{}
	}

	files, err := b.branchRepo.ListFilesByTree(ctx, node.ID)
	if err != nil {
		return err
	}
	for _, file := range files {
		filePath := joinTreePath(path, file.Name)
		if !filter.Allow(filePath) {
			continue
		}
		if !sparse.Covers(filePath) && !keepRootTeamFile(path, file.Name, sparse) {
			continue
		}
		for _, chunk := range file.Chunks {
			seen[chunk.Hash] = struct{}{}
		}
	}

	children, err := b.branchRepo.ListTreeChildren(ctx, node.ID)
	if err != nil {
		return err
	}
	for _, child := range children {
		childPath := joinTreePath(path, child.Name)
		if !filter.CanDescend(childPath) || !sparse.CanDescend(childPath) {
			continue
		}
		if err := b.collectVisibleChunks(ctx, child, childPath, filter, sparse, visited, seen); err != nil {
			return err
		}
	}
	return nil
}
