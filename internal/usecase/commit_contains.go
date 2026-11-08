package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// Contains reports whether commitID is the commit ref points at or one of its
// ancestors, and returns the commit ref resolved to. ref names a tag, a branch,
// a commit id (base36) or a commit hash, tried in that order.
func (t *Tag) Contains(ctx context.Context, projectID, commitID snow.ID, ref string) (bool, snow.ID, error) {
	if !t.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return false, 0, domain.NewErrorNoPermission()
	}
	commit, err := t.branchRepo.GetCommit(ctx, commitID)
	if err != nil && !domain.IsErrorNotFound(err) {
		return false, 0, err
	}
	if err != nil || commit.ProjectID != projectID {
		return false, 0, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", commitID.Base36()))
	}
	head, err := t.resolveRef(ctx, projectID, ref)
	if err != nil {
		return false, 0, err
	}
	if head == nil {
		return false, 0, nil
	}
	contains, err := t.isAncestor(ctx, commitID, *head)
	return contains, *head, err
}

func (t *Tag) resolveRef(ctx context.Context, projectID snow.ID, ref string) (*snow.ID, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, domain.NewErrorUser("ref is required")
	}
	if tag, err := t.tagRepo.GetTagByName(ctx, projectID, ref); err == nil {
		return &tag.CommitID, nil
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	if branch, err := t.branchRepo.GetBranchByName(ctx, projectID, ref); err == nil {
		return branch.CommitID, nil
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	if id, err := snow.ParseBase36(ref); err == nil {
		if commit, err := t.branchRepo.GetCommit(ctx, id); err == nil && commit.ProjectID == projectID {
			return &commit.ID, nil
		}
	}
	if hash, err := domain.ParseHashHex(ref); err == nil {
		if commit, err := t.branchRepo.GetCommitByHash(ctx, projectID, hash); err == nil {
			return &commit.ID, nil
		}
	}
	return nil, domain.NewErrorNotFound(fmt.Sprintf("ref %q not found", ref))
}

// isAncestor walks head's history looking for target. Snowflake ids grow with
// time and a parent always exists before its child, so a commit older than
// target can never lead to it and is not expanded.
func (t *Tag) isAncestor(ctx context.Context, target, head snow.ID) (bool, error) {
	seen := map[snow.ID]bool{head: true}
	queue := []snow.ID{head}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == target {
			return true, nil
		}
		if id < target {
			continue
		}
		commit, err := t.branchRepo.GetCommit(ctx, id)
		if err != nil {
			return false, err
		}
		for _, parent := range commitParents(commit) {
			if !seen[parent] {
				seen[parent] = true
				queue = append(queue, parent)
			}
		}
	}
	return false, nil
}
