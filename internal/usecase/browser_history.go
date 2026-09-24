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
// newest commit that touched each of its direct entries.
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
	newerRoot := headRoot
	for i, commit := range commits {
		var olderRoot *domain.TreeNode
		if i+1 < len(commits) {
			olderRoot, err = b.commitTreeManifest(ctx, projectID, commits[i+1].ID, true)
			if err != nil {
				return nil, err
			}
		} else if commit.Parent1ID != nil {
			break
		}

		newerDir := findLoadedTreeNode(newerRoot, path)
		olderDir := findLoadedTreeNode(olderRoot, path)
		if history.Latest == nil && !sameTreeHash(newerDir, olderDir) {
			history.Latest = commit
		}
		if remaining > 0 {
			resolveEntryCommits(newerDir, olderDir, commit, history.ByPath, &remaining)
		}
		if history.Latest != nil && remaining == 0 {
			break
		}
		newerRoot = olderRoot
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
	var newerRoot *domain.TreeNode
	for i, commit := range commits {
		curRoot := newerRoot
		if curRoot == nil {
			curRoot, err = b.commitTreeManifest(ctx, projectID, commit.ID, true)
			if err != nil {
				return nil, err
			}
		}
		var olderRoot *domain.TreeNode
		hasOlder := false
		if i+1 < len(commits) {
			olderRoot, err = b.commitTreeManifest(ctx, projectID, commits[i+1].ID, true)
			if err != nil {
				return nil, err
			}
			hasOlder = true
		} else if commit.Parent1ID != nil {
			break
		}

		if !hasOlder || pathChanged(curRoot, olderRoot, path) {
			out = append(out, commit)
			if len(out) >= limit {
				break
			}
		}
		newerRoot = olderRoot
	}
	return out, nil
}

func resolveEntryCommits(newer, older *domain.TreeNode, commit *domain.CommitLogEntry, byPath map[string]*domain.CommitLogEntry, remaining *int) {
	if newer == nil {
		return
	}
	resolve := func(name string, newerHash domain.Hash) {
		if _, done := byPath[name]; done {
			return
		}
		olderHash, ok := childEntryHash(older, name)
		if ok && olderHash == newerHash {
			return
		}
		byPath[name] = commit
		*remaining--
	}
	for _, file := range newer.FileChildren {
		resolve(file.Name, file.Hash)
	}
	for _, child := range newer.TreeChildren {
		resolve(child.Name, child.Hash)
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

func pathChanged(newerRoot, olderRoot *domain.TreeNode, path string) bool {
	newHash, newOK := pathEntryHash(newerRoot, path)
	oldHash, oldOK := pathEntryHash(olderRoot, path)
	if !newOK && !oldOK {
		return false
	}
	if newOK != oldOK {
		return true
	}
	return newHash != oldHash
}

func pathEntryHash(root *domain.TreeNode, path string) (domain.Hash, bool) {
	if root == nil {
		return domain.Hash{}, false
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	node := root
	for i, segment := range segments {
		if segment == "" {
			continue
		}
		if i == len(segments)-1 {
			for _, file := range node.FileChildren {
				if file.Name == segment {
					return file.Hash, true
				}
			}
		}
		var next *domain.TreeNode
		for _, child := range node.TreeChildren {
			if child.Name == segment {
				next = child
				break
			}
		}
		if next == nil {
			return domain.Hash{}, false
		}
		node = next
	}
	return node.Hash, true
}
