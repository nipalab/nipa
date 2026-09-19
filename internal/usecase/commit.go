package usecase

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func (b *Branch) GetCommit(ctx context.Context, projectID snow.ID, commitID snow.ID) (*domain.Commit, *domain.TreeNode, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, nil, domain.NewErrorNoPermission()
	}
	commit, err := b.commitByIDInProject(ctx, projectID, commitID)
	if err != nil {
		return nil, nil, err
	}
	root, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
	if err != nil {
		return nil, nil, err
	}
	if err := b.loadTreeManifest(ctx, root, true); err != nil {
		return nil, nil, err
	}
	return commit, root, nil
}

func (b *Branch) WalkCommits(ctx context.Context, projectID snow.ID, startCommitID snow.ID, stopCommitID *snow.ID, limit int) ([]*domain.Commit, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	if limit <= 0 {
		limit = 50
	}
	if stopCommitID != nil && *stopCommitID == startCommitID {
		return []*domain.Commit{}, nil
	}

	type frame struct {
		id       snow.ID
		expanded bool
	}
	visited := map[snow.ID]bool{startCommitID: true}
	loaded := make(map[snow.ID]*domain.Commit)
	stack := []frame{{id: startCommitID}}
	var ordered []*domain.Commit

	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.expanded {
			ordered = append(ordered, loaded[f.id])
			continue
		}
		if stopCommitID != nil && f.id == *stopCommitID {
			continue
		}
		commit, err := b.commitByIDInProject(ctx, projectID, f.id)
		if err != nil {
			return nil, err
		}
		loaded[f.id] = commit
		stack = append(stack, frame{id: f.id, expanded: true})
		parents := commitParents(commit)
		for i := len(parents) - 1; i >= 0; i-- {
			p := parents[i]
			if visited[p] {
				continue
			}
			visited[p] = true
			stack = append(stack, frame{id: p})
		}
	}

	out := make([]*domain.Commit, 0, limit)
	for i := len(ordered) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, ordered[i])
	}
	return out, nil
}

func (b *Branch) commitByIDInProject(ctx context.Context, projectID snow.ID, commitID snow.ID) (*domain.Commit, error) {
	commit, err := b.branchRepo.GetCommit(ctx, commitID)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", commitID.Base36()))
	}
	if err != nil {
		return nil, err
	}
	if commit.ProjectID != projectID {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", commitID.Base36()))
	}
	return commit, nil
}
