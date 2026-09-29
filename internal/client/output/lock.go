package output

import (
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

type Locks struct {
	Locks []Lock `json:"locks"`
}

type Lock struct {
	Path               string `json:"path"`
	Scope              string `json:"scope"`
	Branch             string `json:"branch,omitempty"`
	HeldBy             string `json:"held_by"`
	HeldByName         string `json:"held_by_name,omitempty"`
	MergeRequestNumber *int64 `json:"merge_request_number,omitempty"`
	AcquiredAt         string `json:"acquired_at"`
}

func NewLock(lock *clientDomain.FileLock) Lock {
	if lock == nil {
		return Lock{}
	}
	scope := "branch"
	if lock.Global {
		scope = "mainline"
	}
	return Lock{
		Path:               lock.Path,
		Scope:              scope,
		Branch:             lock.Branch,
		HeldBy:             lock.HeldBy,
		HeldByName:         lock.HeldByName,
		MergeRequestNumber: lock.MergeRequestNumber,
		AcquiredAt:         formatTime(lock.AcquiredAt),
	}
}

func NewLocks(locks []*clientDomain.FileLock) Locks {
	out := Locks{Locks: make([]Lock, 0, len(locks))}
	for _, lock := range locks {
		if lock == nil {
			continue
		}
		out.Locks = append(out.Locks, NewLock(lock))
	}
	return out
}
