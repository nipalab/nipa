package usecase

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/merge"
	"github.com/nipalab/nipa/internal/snow"
)

// mergeTreeReference is the full-tree loader the merge strategies used before
// they read only the changed paths, kept as an oracle for tests: a commit's
// complete tree, unfiltered.
func (b *Branch) mergeTreeReference(ctx context.Context, projectID snow.ID, commitID snow.ID) (map[string]merge.File, error) {
	commit, err := b.commitByIDInProject(ctx, projectID, commitID)
	if err != nil {
		return nil, err
	}
	root, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
	if err != nil {
		return nil, err
	}
	if err := b.loadTreeManifest(ctx, root, "", true, AllowAllFilter(), nil); err != nil {
		return nil, err
	}
	rehashTree(root)
	return merge.Flatten(root), nil
}

// replayMergeReference is replayMerge as it was written over full trees: the
// three-way engine sees every path of the base, the target and the source.
// It returns the full target tree and the full merged result.
func (b *Branch) replayMergeReference(ctx context.Context, projectID snow.ID, strategy string, base *snow.ID, targetID, sourceID snow.ID, land func(mergeLanding) error) (target, merged map[string]merge.File, stored []ChunkRow, err error) {
	targetTree, err := b.mergeTreeReference(ctx, projectID, targetID)
	if err != nil {
		return nil, nil, nil, err
	}
	load := func(id *snow.ID) (map[string]merge.File, error) {
		if id == nil {
			return map[string]merge.File{}, nil
		}
		return b.mergeTreeReference(ctx, projectID, *id)
	}
	if strategy != domain.MergeStrategyRebase {
		baseTree, err := load(base)
		if err != nil {
			return nil, nil, nil, err
		}
		theirs, err := b.mergeTreeReference(ctx, projectID, sourceID)
		if err != nil {
			return nil, nil, nil, err
		}
		result := merge.ThreeWay(baseTree, targetTree, theirs)
		if len(result.Conflicts) > 0 {
			return nil, nil, nil, conflictError(conflictPaths(result), "")
		}
		merged, stored, err := b.materialize(ctx, projectID, result, targetTree)
		if err != nil {
			return nil, nil, nil, err
		}
		files, removed := mergeDelta(targetTree, merged)
		if err := land(mergeLanding{files: files, removed: removed}); err != nil {
			return nil, nil, nil, err
		}
		return targetTree, merged, stored, nil
	}
	commits, err := b.rebaseCommits(ctx, projectID, sourceID, base)
	if err != nil || len(commits) == 0 {
		return nil, nil, nil, err
	}
	current := targetTree
	for _, commit := range commits {
		label := fmt.Sprintf("commit %s", commit.Hash.String()[:10])
		commitBase, err := load(commit.Parent1ID)
		if err != nil {
			return nil, nil, nil, err
		}
		commitTree, err := b.mergeTreeReference(ctx, projectID, commit.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		result := merge.ThreeWay(commitBase, current, commitTree)
		if len(result.Conflicts) > 0 {
			return nil, nil, nil, conflictError(conflictPaths(result), label)
		}
		var added []ChunkRow
		current, added, err = b.materialize(ctx, projectID, result, current)
		if err != nil {
			return nil, nil, nil, err
		}
		stored = append(stored, added...)
		files, removed := mergeDelta(targetTree, current)
		if err := land(mergeLanding{files: files, removed: removed, label: label, commit: commit}); err != nil {
			return nil, nil, nil, err
		}
	}
	return targetTree, current, stored, nil
}
