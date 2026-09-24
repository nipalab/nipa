package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	maxTreeHistoryCommits = 50
	maxPathHistoryScan    = 500
)

// TreeHistory carries the newest commit that touched a directory and the
// newest commit that touched each of its direct entries. ByPath is keyed by
// repo-relative path.
type TreeHistory struct {
	Latest *domain.CommitLogEntry
	ByPath map[string]*domain.CommitLogEntry
}

func (b *Branch) treeHistory(ctx context.Context, projectID snow.ID, headID snow.ID, path string, headRoot *domain.TreeNode) (*TreeHistory, error) {
	history := &TreeHistory{ByPath: make(map[string]*domain.CommitLogEntry)}
	headDir := findLoadedTreeNode(headRoot, path)
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

// PathCommitLog returns the commits that touched path, newest first. path may
// be a file or a directory; the walk is capped at maxPathHistoryScan commits.
func (b *Branch) PathCommitLog(ctx context.Context, projectID snow.ID, rev, path string, startCommitID *snow.ID, limit int) ([]*domain.CommitLogEntry, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	path = strings.Trim(path, "/")
	if limit <= 0 {
		limit = 50
	}
	commitID, err := b.resolveRevision(ctx, projectID, rev)
	if err != nil {
		return nil, err
	}
	if commitID == nil {
		return []*domain.CommitLogEntry{}, nil
	}
	startID := *commitID
	if startCommitID != nil {
		startID = *startCommitID
	}
	if path != "" {
		filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
		if err != nil {
			return nil, err
		}
		if !filter.CanDescend(path) {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found", path))
		}
	}

	commits, err := b.branchRepo.CommitLog(ctx, projectID, startID, maxPathHistoryScan)
	if err != nil {
		return nil, err
	}
	out := make([]*domain.CommitLogEntry, 0, limit)
	var curHash domain.Hash
	var curOK bool
	for i, commit := range commits {
		if i == 0 {
			curHash, curOK, err = b.pathHashAtCommit(ctx, projectID, commit.ID, path)
			if err != nil {
				return nil, err
			}
		}
		var olderHash domain.Hash
		olderOK := false
		hasOlder := false
		if i+1 < len(commits) {
			olderHash, olderOK, err = b.pathHashAtCommit(ctx, projectID, commits[i+1].ID, path)
			if err != nil {
				return nil, err
			}
			hasOlder = true
		} else if commit.Parent1ID != nil {
			break
		}

		if !hasOlder || hashChanged(curHash, curOK, olderHash, olderOK) {
			out = append(out, commit)
			if len(out) >= limit {
				break
			}
		}
		curHash, curOK = olderHash, olderOK
	}
	return out, nil
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
	if err := b.loadTreeManifest(ctx, node, path, true, filter, nil); err != nil {
		return nil, err
	}
	rehashTree(node)
	return node, nil
}

// pathHashAtCommit returns the content hash of one file or directory at a
// commit, loading only the nodes along path. The bool reports whether the
// path exists at that commit.
func (b *Branch) pathHashAtCommit(ctx context.Context, projectID snow.ID, commitID snow.ID, path string) (domain.Hash, bool, error) {
	segments := splitPath(path)
	parentPath := ""
	if len(segments) > 1 {
		parentPath = strings.Join(segments[:len(segments)-1], "/")
	}
	node, err := b.treeNodeAtCommit(ctx, commitID, parentPath)
	if err != nil || node == nil {
		return domain.Hash{}, false, err
	}
	if len(segments) > 0 {
		last := segments[len(segments)-1]
		files, err := b.branchRepo.ListFilesByTree(ctx, node.ID)
		if err != nil {
			return domain.Hash{}, false, err
		}
		for _, file := range files {
			if file.Name == last {
				return file.Hash, true, nil
			}
		}
		child, err := b.branchRepo.GetTreeChildByName(ctx, node.ID, last)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return domain.Hash{}, false, nil
			}
			return domain.Hash{}, false, err
		}
		node = child
	}
	filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
	if err != nil {
		return domain.Hash{}, false, err
	}
	if err := b.loadTreeManifest(ctx, node, path, true, filter, nil); err != nil {
		return domain.Hash{}, false, err
	}
	rehashTree(node)
	return node.Hash, true, nil
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

func hashChanged(newHash domain.Hash, newOK bool, oldHash domain.Hash, oldOK bool) bool {
	if !newOK && !oldOK {
		return false
	}
	if newOK != oldOK {
		return true
	}
	return newHash != oldHash
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
