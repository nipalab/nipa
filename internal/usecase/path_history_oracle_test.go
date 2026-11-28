package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// The per-path history scan the path change index replaced, exported to the
// external usecase_test package as the oracle the index is tested against.

// oldPathHistoryScan is the walk cap the scan had.
const oldPathHistoryScan = 500

// PathCommitLogScan is the per-path history as it was before the path change
// index, kept as the oracle the index is tested against: it walks at most
// scanLimit first-parent commits (the old code used 500) and compares the
// path's hash between every commit and the next older one, reading the path's
// subtree at both under the caller's read filter.
func (b *Branch) PathCommitLogScan(ctx context.Context, projectID snow.ID, rev, path string, startCommitID *snow.ID, limit, scanLimit int) ([]*domain.CommitLogEntry, error) {
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

	commits, err := b.branchRepo.CommitLog(ctx, projectID, startID, scanLimit)
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
		files, err := b.branchRepo.ListFileMetaByTree(ctx, node.ID)
		if err != nil {
			return domain.Hash{}, false, err
		}
		for _, file := range files {
			if file.Name == last {
				filter, err := b.permUc.CompileFilter(ctx, projectID, domain.PermissionRead)
				if err != nil {
					return domain.Hash{}, false, err
				}
				if !filter.Allow(path) {
					return domain.Hash{}, false, nil
				}
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

func hashChanged(newHash domain.Hash, newOK bool, oldHash domain.Hash, oldOK bool) bool {
	if !newOK && !oldOK {
		return false
	}
	if newOK != oldOK {
		return true
	}
	return newHash != oldHash
}
