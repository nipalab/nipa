package usecase

import (
	"context"
	"testing"

	"github.com/nipalab/nipa/internal/domain"
)

// Exports for the external usecase_test package, which can import the sqlite
// repositories (they depend on this package) to run against a real database.

var EmptyTreeHash = emptyTreeHash

// SetPathIndexBatches shrinks the path index batches for one test, so a short
// history crosses several batch boundaries; the sizes are restored at cleanup.
// Tests that call it must not run in parallel.
func SetPathIndexBatches(t testing.TB, first, maxBatch int) {
	t.Helper()
	oldFirst, oldMax := pathIndexFirstBatch, pathIndexMaxBatch
	pathIndexFirstBatch, pathIndexMaxBatch = first, maxBatch
	t.Cleanup(func() { pathIndexFirstBatch, pathIndexMaxBatch = oldFirst, oldMax })
}

func RehashTree(node *domain.TreeNode) domain.Hash { return rehashTree(node) }

// NewTestPathFilter builds a read filter from PBAC rules and path defaults.
func NewTestPathFilter(rules []*domain.PBACRule, defaults []*domain.ProjectPathPermission) *PathFilter {
	return &PathFilter{set: &permissionSet{rules: rules, defaults: defaults}, permission: domain.PermissionRead}
}

func (b *Branch) LoadTreeManifest(ctx context.Context, node *domain.TreeNode, path string, recursive bool, filter *PathFilter, sparse domain.PrefixSet) error {
	return b.loadTreeManifest(ctx, node, path, recursive, filter, sparse)
}

// LoadTreeManifestRecursive is the per-directory loader loadTreeManifest
// replaced, kept as the reference the level-by-level loader must match.
func (b *Branch) LoadTreeManifestRecursive(ctx context.Context, node *domain.TreeNode, path string, recursive bool, filter *PathFilter, sparse domain.PrefixSet) error {
	if node.Hash == emptyTreeHash {
		node.FileChildren, node.TreeChildren = nil, nil
		return nil
	}
	files, err := b.branchRepo.ListFilesByTree(ctx, node.ID)
	if err != nil {
		return err
	}
	node.FileChildren = nil
	for _, file := range files {
		filePath := joinTreePath(path, file.Name)
		if !filter.Allow(filePath) {
			continue
		}
		if !sparse.Covers(filePath) && !keepRootTeamFile(path, file.Name, sparse) {
			continue
		}
		node.FileChildren = append(node.FileChildren, file)
	}

	node.TreeChildren = nil
	if !recursive {
		return nil
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
		if err := b.LoadTreeManifestRecursive(ctx, child, childPath, true, filter, sparse); err != nil {
			return err
		}
		if len(child.FileChildren) == 0 && len(child.TreeChildren) == 0 {
			continue
		}
		node.TreeChildren = append(node.TreeChildren, child)
	}
	return nil
}
