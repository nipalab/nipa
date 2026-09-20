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

// MergeRef identifies one side of a merge-base lookup. A commit ID takes
// precedence over a branch name.
type MergeRef struct {
	BranchName string
	CommitID   *snow.ID
}

type mergeSide struct {
	name       string
	commitID   *snow.ID
	commitHash *domain.Hash
}

func (b *Branch) resolveMergeRef(ctx context.Context, projectID snow.ID, ref MergeRef) (*mergeSide, error) {
	if ref.CommitID != nil {
		commit, err := b.branchRepo.GetCommit(ctx, *ref.CommitID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", ref.CommitID.Base36()))
			}
			return nil, err
		}
		if commit.ProjectID != projectID {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", ref.CommitID.Base36()))
		}
		return &mergeSide{commitID: &commit.ID, commitHash: &commit.Hash}, nil
	}

	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, ref.BranchName)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", ref.BranchName))
	}
	if err != nil {
		return nil, err
	}
	return &mergeSide{name: branch.Name, commitID: branch.CommitID}, nil
}

func (b *Branch) GetMergeBase(ctx context.Context, projectID snow.ID, target, source MergeRef) (*MergeBaseInfo, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}

	targetSide, err := b.resolveMergeRef(ctx, projectID, target)
	if err != nil {
		return nil, err
	}
	sourceSide, err := b.resolveMergeRef(ctx, projectID, source)
	if err != nil {
		return nil, err
	}

	baseCommitID, err := b.findMergeBase(ctx, targetSide.commitID, sourceSide.commitID)
	if err != nil {
		return nil, err
	}

	for _, side := range []*mergeSide{targetSide, sourceSide} {
		if side.commitHash != nil || side.commitID == nil {
			continue
		}
		commit, err := b.branchRepo.GetCommit(ctx, *side.commitID)
		if err != nil {
			return nil, err
		}
		side.commitHash = &commit.Hash
	}

	info := &MergeBaseInfo{
		TargetBranch:      targetSide.name,
		SourceBranch:      sourceSide.name,
		TargetCommitID:    targetSide.commitID,
		SourceCommitID:    sourceSide.commitID,
		TargetCommitHash:  targetSide.commitHash,
		SourceCommitHash:  sourceSide.commitHash,
		MergeBaseCommitID: baseCommitID,
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
