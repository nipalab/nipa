package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// chunkReader reads stored chunk content back for the browser endpoints.
type chunkReader interface {
	Get(ctx context.Context, hash domain.Hash) ([]byte, error)
}

// NewBranchWithChunks builds a Branch with content access for tree, blob and
// commit-diff browsing.
func NewBranchWithChunks(permUc permissionUsecase, branchRepo branchRepository, snowNode snow.Node, chunks chunkReader) *Branch {
	branch := NewBranch(permUc, branchRepo, snowNode)
	branch.chunks = chunks
	return branch
}

// TreeAt returns the readable subtree at path for a branch name, commit id or
// the default branch when rev is empty. Hidden paths return not found.
func (b *Branch) TreeAt(ctx context.Context, projectID snow.ID, rev, path string) (*domain.TreeNode, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	commitID, err := b.resolveRevision(ctx, projectID, rev)
	if err != nil {
		return nil, err
	}
	path = strings.Trim(path, "/")
	if commitID == nil {
		if path != "" {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found", path))
		}
		return &domain.TreeNode{}, nil
	}
	root, err := b.commitTreeManifest(ctx, projectID, *commitID, true)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return root, nil
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, err
	}
	if !filter.CanDescend(path) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found", path))
	}
	node := findLoadedTreeNode(root, path)
	if node == nil {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found", path))
	}
	return node, nil
}

// FileContent reassembles one readable file at the given revision.
func (b *Branch) FileContent(ctx context.Context, projectID snow.ID, rev, path string) ([]byte, diff.Entry, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, diff.Entry{}, domain.NewErrorNoPermission()
	}
	commitID, err := b.resolveRevision(ctx, projectID, rev)
	if err != nil {
		return nil, diff.Entry{}, err
	}
	path = strings.Trim(path, "/")
	if commitID == nil {
		return nil, diff.Entry{}, domain.NewErrorNotFound(fmt.Sprintf("file %q not found", path))
	}
	root, err := b.commitTreeManifest(ctx, projectID, *commitID, true)
	if err != nil {
		return nil, diff.Entry{}, err
	}
	entry, ok := diff.FromTree(root)[path]
	if !ok {
		return nil, diff.Entry{}, domain.NewErrorNotFound(fmt.Sprintf("file %q not found", path))
	}
	if b.chunks == nil {
		return nil, diff.Entry{}, domain.NewErrorInternalServer("chunk store is not configured")
	}
	data, err := diff.LoadContent(b.loadChunk(ctx), entry)
	if err != nil {
		return nil, diff.Entry{}, domain.NewErrorInternalServer(err.Error())
	}
	return data, entry, nil
}

// CommitDiff returns the path-filtered diff between a commit and its first
// parent (or an explicit base) plus the resolved base commit id.
func (b *Branch) CommitDiff(ctx context.Context, projectID snow.ID, commitID snow.ID, baseID *snow.ID) ([]diff.FileDiff, *snow.ID, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, nil, domain.NewErrorNoPermission()
	}
	commit, err := b.commitByIDInProject(ctx, projectID, commitID)
	if err != nil {
		return nil, nil, err
	}
	resolvedBase := baseID
	if resolvedBase == nil {
		resolvedBase = commit.Parent1ID
	}

	headTree, err := b.commitTreeManifest(ctx, projectID, commitID, true)
	if err != nil {
		return nil, nil, err
	}
	var baseTree *domain.TreeNode
	if resolvedBase != nil {
		baseCommit, err := b.commitByIDInProject(ctx, projectID, *resolvedBase)
		if err != nil {
			return nil, nil, err
		}
		baseTree, err = b.commitTreeManifest(ctx, projectID, baseCommit.ID, true)
		if err != nil {
			return nil, nil, err
		}
	}
	return diff.TreeDiff(baseTree, headTree, b.loadChunk(ctx)), resolvedBase, nil
}

func (b *Branch) resolveRevision(ctx context.Context, projectID snow.ID, rev string) (*snow.ID, error) {
	if rev == "" {
		branch, err := b.branchRepo.GetDefaultBranch(ctx, projectID)
		if err != nil {
			return nil, err
		}
		return branch.CommitID, nil
	}
	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, rev)
	if err == nil {
		return branch.CommitID, nil
	}
	if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	commitID, parseErr := snow.ParseBase36(rev)
	if parseErr != nil {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("revision %q not found", rev))
	}
	commit, err := b.commitByIDInProject(ctx, projectID, commitID)
	if err != nil {
		return nil, err
	}
	return &commit.ID, nil
}

func (b *Branch) loadChunk(ctx context.Context) func(domain.Hash) ([]byte, error) {
	if b.chunks == nil {
		return nil
	}
	return func(hash domain.Hash) ([]byte, error) {
		return b.chunks.Get(ctx, hash)
	}
}

func findLoadedTreeNode(root *domain.TreeNode, path string) *domain.TreeNode {
	node := root
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment == "" {
			continue
		}
		var next *domain.TreeNode
		for _, child := range node.TreeChildren {
			if child.Name == segment {
				next = child
				break
			}
		}
		if next == nil {
			return nil
		}
		node = next
	}
	return node
}
