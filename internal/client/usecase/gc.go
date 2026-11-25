package usecase

import (
	"log/slog"
	"time"

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

type shelfStateLoader interface {
	LoadShelfState() (*clientDomain.ShelfState, error)
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
	result, err := pruneCache(g.localRepo)
	if err == nil && !result.Skipped {
		// Temp files of writes that died long ago; anything younger may
		// belong to a write that starts right after the prune.
		sweepMaterializeTemps(root, time.Now().Add(-staleTempAge))
	}
	return result, err
}

func lockShared(lr any) (func(), error) {
	if locker, ok := lr.(sharedLocker); ok {
		return locker.LockShared()
	}
	return func() {}, nil
}

// pruneGuard takes what every prune needs before it deletes anything: the
// exclusive repository lock, without waiting, and no pending merge, revert or
// shelf state (their content lives in the cache without a snapshot row). When
// the prune must not run it returns the skipped result instead of a release
// function.
func pruneGuard(lr cacheLocalRepo) (release func(), skipped *PruneResult, err error) {
	release = func() {}
	if locker, ok := lr.(exclusiveLocker); ok {
		unlock, acquired, err := locker.TryLockExclusive()
		if err != nil {
			return nil, nil, err
		}
		if !acquired {
			return nil, &PruneResult{Skipped: true, SkipReason: PruneSkipBusy}, nil
		}
		release = unlock
	}
	pending, err := pruneBlocked(lr)
	if err != nil || pending {
		release()
		if err != nil {
			return nil, nil, err
		}
		return nil, &PruneResult{Skipped: true, SkipReason: PruneSkipPending}, nil
	}
	return release, nil, nil
}

// pruneBlocked reports whether a merge, revert or shelf operation is pending.
func pruneBlocked(lr cacheLocalRepo) (bool, error) {
	merge, err := lr.LoadMergeState()
	if err != nil {
		return false, err
	}
	revert, err := lr.LoadRevertState()
	if err != nil {
		return false, err
	}
	if merge != nil || revert != nil {
		return true, nil
	}
	if l, ok := lr.(shelfStateLoader); ok {
		shelf, err := l.LoadShelfState()
		if err != nil {
			return false, err
		}
		return shelf != nil, nil
	}
	return false, nil
}

func pruneCache(lr cacheLocalRepo) (PruneResult, error) {
	release, skipped, err := pruneGuard(lr)
	if err != nil {
		return PruneResult{}, err
	}
	if skipped != nil {
		return *skipped, nil
	}
	defer release()
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
