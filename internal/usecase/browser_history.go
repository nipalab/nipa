package usecase

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const maxTreeHistoryCommits = 50

// TreeHistory carries the newest commit that touched a directory and the
// newest commit that touched each of its direct entries. ByPath is keyed by
// repo-relative path.
type TreeHistory struct {
	Latest *domain.CommitLogEntry
	ByPath map[string]*domain.CommitLogEntry
}

func (b *Branch) treeHistory(ctx context.Context, projectID snow.ID, headID snow.ID, path string, headDir *domain.TreeNode) (*TreeHistory, error) {
	history := &TreeHistory{ByPath: make(map[string]*domain.CommitLogEntry)}
	if headDir == nil {
		return history, nil
	}
	commits, err := b.branchRepo.CommitLog(ctx, projectID, headID, maxTreeHistoryCommits)
	if err != nil {
		return nil, err
	}
	remaining := len(headDir.FileChildren) + len(headDir.TreeChildren)
	newerDir := headDir
	for i, commit := range commits {
		var olderDir *domain.TreeNode
		if i+1 < len(commits) {
			olderDir, err = b.dirManifestAt(ctx, projectID, commits[i+1].ID, path)
			if err != nil {
				return nil, err
			}
		} else if commit.Parent1ID != nil {
			break
		}

		if history.Latest == nil && !sameTreeHash(newerDir, olderDir) {
			history.Latest = commit
		}
		if remaining > 0 {
			resolveEntryCommits(newerDir, olderDir, headDir, commit, path, history.ByPath, &remaining)
		}
		if history.Latest != nil && remaining == 0 {
			break
		}
		newerDir = olderDir
	}
	return history, nil
}

// dirManifestAt loads only the subtree at path for one commit instead of the
// whole repository manifest.
func (b *Branch) dirManifestAt(ctx context.Context, projectID snow.ID, commitID snow.ID, path string) (*domain.TreeNode, error) {
	node, err := b.treeNodeAtCommit(ctx, commitID, path)
	if err != nil || node == nil {
		return nil, err
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return nil, err
	}
	if filter.All() {
		return b.loadShallow(ctx, node)
	}
	if err := b.loadTreeManifest(ctx, node, path, true, filter, nil); err != nil {
		return nil, err
	}
	rehashTree(node)
	return node, nil
}

// treeNodeAtCommit descends from the commit root to path without loading any
// subtree content. A missing node returns (nil, nil).
func (b *Branch) treeNodeAtCommit(ctx context.Context, commitID snow.ID, path string) (*domain.TreeNode, error) {
	commit, err := b.branchRepo.GetCommit(ctx, commitID)
	if err != nil {
		return nil, err
	}
	node, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
	if err != nil {
		return nil, err
	}
	for _, segment := range splitPath(path) {
		child, err := b.branchRepo.GetTreeChildByName(ctx, node.ID, segment)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		node = child
	}
	return node, nil
}

// resolveEntryCommits attributes the head directory's entries to commit when
// their content changed between newer and older. Only head entries are
// considered, so entries deleted before head never consume the remaining
// budget or enter ByPath.
func resolveEntryCommits(newer, older, head *domain.TreeNode, commit *domain.CommitLogEntry, basePath string, byPath map[string]*domain.CommitLogEntry, remaining *int) {
	if head == nil {
		return
	}
	resolve := func(name string) {
		key := joinTreePath(basePath, name)
		if _, done := byPath[key]; done {
			return
		}
		newerHash, ok := childEntryHash(newer, name)
		if !ok {
			return
		}
		olderHash, ok := childEntryHash(older, name)
		if ok && olderHash == newerHash {
			return
		}
		byPath[key] = commit
		*remaining--
	}
	for _, file := range head.FileChildren {
		resolve(file.Name)
	}
	for _, child := range head.TreeChildren {
		resolve(child.Name)
	}
}

func childEntryHash(dir *domain.TreeNode, name string) (domain.Hash, bool) {
	if dir == nil {
		return domain.Hash{}, false
	}
	for _, file := range dir.FileChildren {
		if file.Name == name {
			return file.Hash, true
		}
	}
	for _, child := range dir.TreeChildren {
		if child.Name == name {
			return child.Hash, true
		}
	}
	return domain.Hash{}, false
}

func sameTreeHash(a, b *domain.TreeNode) bool {
	return a != nil && b != nil && a.Hash == b.Hash
}

func splitPath(path string) []string {
	var out []string
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}
