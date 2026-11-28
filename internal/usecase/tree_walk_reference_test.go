package usecase

import (
	"context"
	"sort"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
)

// changedTreeEntriesReference is the per-directory walk changedTreeEntries
// replaced, kept as an oracle for tests. It flattens only the directories where two trees differ.
// Both sides are walked in lockstep and a pair of directories with the same
// stored hash is skipped without any query, so the cost follows the change
// instead of the repository. Stored hashes cover the unfiltered content and the
// same filter applies to both sides, so equal hashes are equal after filtering
// too. Files that are identical on both sides are left out of both maps, which
// does not change what diff.Compare reports.
func (b *Branch) changedTreeEntriesReference(ctx context.Context, base, head *domain.TreeNode, filter *PathFilter) (baseEntries, headEntries map[string]diff.Entry, err error) {
	baseEntries = map[string]diff.Entry{}
	headEntries = map[string]diff.Entry{}
	if err := b.walkChangedTreesReference(ctx, base, head, "", filter, baseEntries, headEntries); err != nil {
		return nil, nil, err
	}
	return baseEntries, headEntries, nil
}

func (b *Branch) walkChangedTreesReference(ctx context.Context, base, head *domain.TreeNode, path string, filter *PathFilter, baseOut, headOut map[string]diff.Entry) error {
	if base == nil && head == nil {
		return nil
	}
	if base != nil && head != nil && base.Hash != (domain.Hash{}) && base.Hash == head.Hash {
		return nil
	}

	baseChildren, err := b.collectDirectoryReference(ctx, base, path, filter, baseOut)
	if err != nil {
		return err
	}
	headChildren, err := b.collectDirectoryReference(ctx, head, path, filter, headOut)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(baseChildren)+len(headChildren))
	for name := range baseChildren {
		names = append(names, name)
	}
	for name := range headChildren {
		if _, ok := baseChildren[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		childPath := joinTreePath(path, name)
		if !filter.CanDescend(childPath) {
			continue
		}
		if err := b.walkChangedTreesReference(ctx, baseChildren[name], headChildren[name], childPath, filter, baseOut, headOut); err != nil {
			return err
		}
	}
	return nil
}

// collectDirectoryReference adds the readable files of one directory to out and returns
// its sub-directories by name.
func (b *Branch) collectDirectoryReference(ctx context.Context, node *domain.TreeNode, path string, filter *PathFilter, out map[string]diff.Entry) (map[string]*domain.TreeNode, error) {
	if node == nil {
		return nil, nil
	}
	files, err := b.branchRepo.ListFilesByTree(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	allowed := files[:0:0]
	for _, file := range files {
		if filter.Allow(joinTreePath(path, file.Name)) {
			allowed = append(allowed, file)
		}
	}
	diff.AddFileEntries(out, path, allowed)

	children, err := b.branchRepo.ListTreeChildren(ctx, node.ID)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*domain.TreeNode, len(children))
	for _, child := range children {
		byName[child.Name] = child
	}
	return byName, nil
}
