package usecase

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const commitGraphBatch = 512

type commitAncestorLoader interface {
	CommitAncestors(ctx context.Context, projectID, startID snow.ID, limit int) ([]*domain.Commit, error)
}

type commitGraph struct {
	repo      branchRepository
	projectID snow.ID
	scoped    bool
	cache     map[snow.ID]*domain.Commit
}

func newCommitGraph(repo branchRepository, projectID snow.ID, scoped bool) *commitGraph {
	return &commitGraph{repo: repo, projectID: projectID, scoped: scoped, cache: make(map[snow.ID]*domain.Commit)}
}

func (g *commitGraph) get(ctx context.Context, id snow.ID) (*domain.Commit, error) {
	if c, ok := g.cache[id]; ok {
		return c, nil
	}
	loader, ok := g.repo.(commitAncestorLoader)
	if !ok {
		if g.scoped {
			return commitByIDInProject(ctx, g.repo, g.projectID, id)
		}
		return g.repo.GetCommit(ctx, id)
	}
	commits, err := loader.CommitAncestors(ctx, g.projectID, id, commitGraphBatch)
	if err != nil {
		return nil, err
	}
	for _, c := range commits {
		g.cache[c.ID] = c
	}
	if c, ok := g.cache[id]; ok {
		return c, nil
	}
	return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", id.Base36()))
}
