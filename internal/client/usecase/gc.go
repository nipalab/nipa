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
	ListPins() ([]string, error)
}

type gcLocalRepo interface {
	Init(target string) error
	cacheLocalRepo
}

type sharedLocker interface {
	LockShared() (func(), error)
}

type exclusiveLocker interface {
	TryLockExclusive() (func(), bool, error)
}

const (
	PruneSkipBusy    = "repository busy"
	PruneSkipPending = "a merge or revert is in progress"
)

type PruneResult struct {
	Objects    int
	Bytes      int64
	Skipped    bool
	SkipReason string
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

func lockShared(lr any) (func(), error) {
	if locker, ok := lr.(sharedLocker); ok {
		return locker.LockShared()
	}
	return func() {}, nil
}

func pruneCache(lr cacheLocalRepo) (PruneResult, error) {
	if locker, ok := lr.(exclusiveLocker); ok {
		release, acquired, err := locker.TryLockExclusive()
		if err != nil {
			return PruneResult{}, err
		}
		if !acquired {
			return PruneResult{Skipped: true, SkipReason: PruneSkipBusy}, nil
		}
		defer release()
	}
	merge, err := lr.LoadMergeState()
	if err != nil {
		return PruneResult{}, err
	}
	revert, err := lr.LoadRevertState()
	if err != nil {
		return PruneResult{}, err
	}
	if merge != nil || revert != nil {
		return PruneResult{Skipped: true, SkipReason: PruneSkipPending}, nil
	}
	snapshot, err := lr.Snapshot()
	if err != nil {
		return PruneResult{}, err
	}
	pins, err := lr.ListPins()
	if err != nil {
		return PruneResult{}, err
	}
	keep := make(map[domain.Hash]struct{})
	if snapshot != nil {
		for _, f := range snapshot.Files {
			if !cachedInPristine(f, pins) {
				continue
			}
			for _, h := range f.Chunks {
				keep[h] = struct{}{}
			}
		}
	}
	files, freed, err := lr.PruneObjects(keep)
	return PruneResult{Objects: files, Bytes: freed}, err
}

// PruneCache drops cached objects no snapshot file keeps. It is a no-op while
// the repository is busy or a merge or revert is pending.
func PruneCache(lr cacheLocalRepo) (PruneResult, error) {
	return pruneCache(lr)
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
