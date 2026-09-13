package usecase

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type MergeBaseInfo struct {
	TargetBranch      string
	SourceBranch      string
	TargetCommitID    *snow.ID
	SourceCommitID    *snow.ID
	TargetCommitHash  *domain.Hash
	SourceCommitHash  *domain.Hash
	MergeBaseCommitID *snow.ID
	MergeBaseTree     *domain.TreeNode
}

func (b *Branch) GetMergeBase(ctx context.Context, projectID snow.ID, targetBranch, sourceBranch string) (*MergeBaseInfo, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}

	target, err := b.branchRepo.GetBranchByName(ctx, projectID, targetBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", targetBranch))
	}
	if err != nil {
		return nil, err
	}
	source, err := b.branchRepo.GetBranchByName(ctx, projectID, sourceBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", sourceBranch))
	}
	if err != nil {
		return nil, err
	}

	baseCommitID, err := b.findMergeBase(ctx, target.CommitID, source.CommitID)
	if err != nil {
		return nil, err
	}

	info := &MergeBaseInfo{
		TargetBranch:      target.Name,
		SourceBranch:      source.Name,
		TargetCommitID:    target.CommitID,
		SourceCommitID:    source.CommitID,
		MergeBaseCommitID: baseCommitID,
	}
	if target.CommitID != nil {
		c, err := b.branchRepo.GetCommit(ctx, *target.CommitID)
		if err != nil {
			return nil, err
		}
		info.TargetCommitHash = &c.Hash
	}
	if source.CommitID != nil {
		c, err := b.branchRepo.GetCommit(ctx, *source.CommitID)
		if err != nil {
			return nil, err
		}
		info.SourceCommitHash = &c.Hash
	}
	if baseCommitID == nil {
		return info, nil
	}
	tree, err := b.commitTreeManifest(ctx, *baseCommitID, true)
	if err != nil {
		return nil, err
	}
	info.MergeBaseTree = tree
	return info, nil
}

func (b *Branch) FastForward(ctx context.Context, projectID snow.ID, targetBranch, sourceBranch string) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}

	target, err := b.branchRepo.GetBranchByName(ctx, projectID, targetBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", targetBranch))
	}
	if err != nil {
		return nil, err
	}
	source, err := b.branchRepo.GetBranchByName(ctx, projectID, sourceBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", sourceBranch))
	}
	if err != nil {
		return nil, err
	}

	if source.CommitID == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("cannot fast-forward branch %q: source branch %q has no commits", targetBranch, sourceBranch))
	}
	if target.CommitID == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("cannot fast-forward empty branch %q", targetBranch))
	}

	base, err := b.findMergeBase(ctx, target.CommitID, source.CommitID)
	if err != nil {
		return nil, err
	}
	if base == nil || *base != *target.CommitID {
		return nil, domain.NewErrorConflict(fmt.Sprintf("cannot fast-forward branch %q: branches have diverged", targetBranch))
	}

	if err := b.branchRepo.UpdateCommitIf(ctx, target.ID, target.CommitID, source.CommitID); err != nil {
		return nil, err
	}
	return b.branchRepo.GetByProjectIDAndID(ctx, projectID, target.ID)
}

func (b *Branch) findMergeBase(ctx context.Context, a, c *snow.ID) (*snow.ID, error) {
	if a == nil || c == nil {
		return nil, nil
	}
	if *a == *c {
		return a, nil
	}

	seenA := map[snow.ID]bool{*a: true}
	seenB := map[snow.ID]bool{*c: true}
	queueA := []snow.ID{*a}
	queueB := []snow.ID{*c}

	for len(queueA) > 0 || len(queueB) > 0 {
		var nextA, nextB []snow.ID
		for _, id := range queueA {
			commit, err := b.branchRepo.GetCommit(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, p := range commitParents(commit) {
				if seenB[p] {
					id := p
					return &id, nil
				}
				if !seenA[p] {
					seenA[p] = true
					nextA = append(nextA, p)
				}
			}
		}
		queueA = nextA

		for _, id := range queueB {
			commit, err := b.branchRepo.GetCommit(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, p := range commitParents(commit) {
				if seenA[p] {
					id := p
					return &id, nil
				}
				if !seenB[p] {
					seenB[p] = true
					nextB = append(nextB, p)
				}
			}
		}
		queueB = nextB
	}
	return nil, nil
}

func commitParents(c *domain.Commit) []snow.ID {
	var out []snow.ID
	if c.Parent1ID != nil {
		out = append(out, *c.Parent1ID)
	}
	if c.Parent2ID != nil {
		out = append(out, *c.Parent2ID)
	}
	return out
}

func (b *Branch) commitTreeManifest(ctx context.Context, commitID snow.ID, recursive bool) (*domain.TreeNode, error) {
	commit, err := b.branchRepo.GetCommit(ctx, commitID)
	if err != nil {
		return nil, err
	}
	root, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
	if err != nil {
		return nil, err
	}
	if err := b.loadTreeManifest(ctx, root, recursive); err != nil {
		return nil, err
	}
	return root, nil
}
