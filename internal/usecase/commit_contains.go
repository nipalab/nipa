package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// maxContainsWalk bounds the number of commits an ancestry check expands.
const maxContainsWalk = 10000

type branchTagLookup interface {
	GetTagByName(ctx context.Context, projectID snow.ID, name string) (*domain.Tag, error)
}

// WithTags lets ref lookups resolve release tags.
func (b *Branch) WithTags(tags branchTagLookup) *Branch {
	b.tags = tags
	return b
}

// Contains reports whether commitID is the commit ref points at or one of its
// ancestors, and returns the commit ref resolved to. ref names a tag, a branch,
// a commit id (base36) or a commit hash, tried in that order.
func (b *Branch) Contains(ctx context.Context, projectID, commitID snow.ID, ref string) (bool, snow.ID, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return false, 0, domain.NewErrorNoPermission()
	}
	commit, err := b.branchRepo.GetCommit(ctx, commitID)
	if err != nil && !domain.IsErrorNotFound(err) {
		return false, 0, err
	}
	if err != nil || commit.ProjectID != projectID {
		return false, 0, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", commitID.Base36()))
	}
	head, err := b.resolveRef(ctx, projectID, ref)
	if err != nil {
		return false, 0, err
	}
	if head == nil {
		return false, 0, nil
	}
	contains, err := b.isAncestor(ctx, commitID, *head)
	return contains, *head, err
}

func (b *Branch) resolveRef(ctx context.Context, projectID snow.ID, ref string) (*snow.ID, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, domain.NewErrorUser("ref is required")
	}
	if b.tags != nil {
		if tag, err := b.tags.GetTagByName(ctx, projectID, ref); err == nil {
			return &tag.CommitID, nil
		} else if !domain.IsErrorNotFound(err) {
			return nil, err
		}
	}
	if branch, err := b.branchRepo.GetBranchByName(ctx, projectID, ref); err == nil {
		return branch.CommitID, nil
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}
	if id, err := snow.ParseBase36(ref); err == nil {
		if commit, err := b.branchRepo.GetCommit(ctx, id); err == nil && commit.ProjectID == projectID {
			return &commit.ID, nil
		}
	}
	if hash, err := domain.ParseHashHex(ref); err == nil {
		if commit, err := b.branchRepo.GetCommitByHash(ctx, projectID, hash); err == nil {
			return &commit.ID, nil
		}
	}
	return nil, domain.NewErrorNotFound(fmt.Sprintf("ref %q not found", ref))
}

func (b *Branch) isAncestor(ctx context.Context, target, head snow.ID) (bool, error) {
	seen := map[snow.ID]bool{head: true}
	queue := []snow.ID{head}
	for walked := 0; len(queue) > 0; walked++ {
		id := queue[0]
		queue = queue[1:]
		if id == target {
			return true, nil
		}
		if walked >= maxContainsWalk {
			return false, domain.NewErrorUser(fmt.Sprintf("ancestry check exceeded %d commits", maxContainsWalk))
		}
		commit, err := b.branchRepo.GetCommit(ctx, id)
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
