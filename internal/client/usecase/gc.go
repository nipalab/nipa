package usecase

import (
	"log/slog"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

type cacheLocalRepo interface {
	Snapshot() (*clientDomain.Snapshot, error)
	LoadMergeState() (*clientDomain.MergeState, error)
	LoadRevertState() (*clientDomain.RevertState, error)
	PruneObjects(keep map[domain.Hash]struct{}) (int, int64, error)
}

type gcLocalRepo interface {
	Init(target string) error
	cacheLocalRepo
}

type PruneResult struct {
	Objects int
	Bytes   int64
	Skipped bool
}

type GC struct {
	localRepo gcLocalRepo
}

func NewGC(localRepo gcLocalRepo) *GC {
	return &GC{localRepo: localRepo}
}

func (g *GC) Run(root string) (PruneResult, error) {
	if err := g.localRepo.Init(root); err != nil {
		return PruneResult{}, err
	}
	return pruneCache(g.localRepo)
}

func pruneCache(lr cacheLocalRepo) (PruneResult, error) {
	merge, err := lr.LoadMergeState()
	if err != nil {
		return PruneResult{}, err
	}
	revert, err := lr.LoadRevertState()
	if err != nil {
		return PruneResult{}, err
	}
	if merge != nil || revert != nil {
		return PruneResult{Skipped: true}, nil
	}
	snapshot, err := lr.Snapshot()
	if err != nil {
		return PruneResult{}, err
	}
	keep := make(map[domain.Hash]struct{})
	if snapshot != nil {
		for _, f := range snapshot.Files {
			for _, h := range f.Chunks {
				keep[h] = struct{}{}
			}
		}
	}
	files, freed, err := lr.PruneObjects(keep)
	return PruneResult{Objects: files, Bytes: freed}, err
}

func pruneAfterSuccess(lr any, err error) {
	if err != nil {
		return
	}
	cache, ok := lr.(cacheLocalRepo)
	if !ok {
		return
	}
	if _, pruneErr := pruneCache(cache); pruneErr != nil {
		slog.Warn("local cache prune failed", "error", pruneErr)
	}
}
