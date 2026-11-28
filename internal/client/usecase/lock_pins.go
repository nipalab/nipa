package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/chunker"
	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

type pinLocalRepo interface {
	Init(target string) error
	Snapshot() (*clientDomain.Snapshot, error)
	PinPaths(paths []string) error
	UnpinPaths(paths []string) error
	pristineRepo
}

// LockPins keeps the cached old version of files under a lock this client
// took, so offline compare and restore work while they are edited.
type LockPins struct {
	auth      *Auth
	client    ContentDownloader
	localRepo pinLocalRepo
}

func NewLockPins(auth *Auth, client ContentDownloader, localRepo pinLocalRepo) *LockPins {
	return &LockPins{auth: auth, client: client, localRepo: localRepo}
}

// Pin records the lock path and captures the raw snapshot files it covers.
// The server lock is already granted, so callers report a failure as a
// warning and keep the lock.
func (p *LockPins) Pin(ctx context.Context, root, path string) error {
	if err := p.localRepo.Init(root); err != nil {
		return err
	}
	unlock, err := lockShared(p.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := p.localRepo.PinPaths([]string{path}); err != nil {
		return err
	}
	pin, err := domain.NormalizePathPrefix(path)
	if err != nil || pin == "" {
		return err
	}
	covered, err := pinnedRawFiles(p.localRepo, pin)
	if err != nil {
		return err
	}
	return pristine{root: root, repo: p.localRepo, auth: p.auth, client: p.client}.ensureSnapshotContent(ctx, covered)
}

// Unpin drops the lock path and prunes the content it kept: the chunks of the
// raw files it covered, unless another pin still covers a file that lists
// them. A repository that cannot scan for a set of chunks gets the full sweep.
func (p *LockPins) Unpin(ctx context.Context, root, path string) (err error) {
	var released []domain.Hash
	targeted := false
	defer func() {
		if targeted {
			pruneCandidates(p.localRepo, released, err)
			return
		}
		pruneAfterSuccess(p.localRepo, err)
	}()
	if err := p.localRepo.Init(root); err != nil {
		return err
	}
	unlock, err := lockShared(p.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := p.localRepo.UnpinPaths([]string{path}); err != nil {
		return err
	}
	pin, normErr := domain.NormalizePathPrefix(path)
	if _, scans := p.localRepo.(chunkSetScanner); !scans || normErr != nil || pin == "" {
		return nil
	}
	if _, scoped := p.localRepo.(scopedSnapshotRepo); !scoped {
		return nil
	}
	covered, err := pinnedRawFiles(p.localRepo, pin)
	if err != nil {
		return err
	}
	seen := map[domain.Hash]bool{}
	for _, f := range covered {
		for _, h := range f.Chunks {
			if !seen[h] {
				seen[h] = true
				released = append(released, h)
			}
		}
	}
	targeted = true
	return nil
}

// pinnedRawFiles returns the tracked files the normalized pin covers (the
// file at the pin, or every file below the directory) whose content is not
// compressed text, in path order. A repository that reads by path is asked
// for those rows only; any other loads the snapshot and filters it.
func pinnedRawFiles(repo any, pin string) ([]clientDomain.SnapshotFile, error) {
	var rows []clientDomain.SnapshotFile
	if scoped, ok := repo.(scopedSnapshotRepo); ok {
		exact, err := scoped.SnapshotFiles([]string{pin})
		if err != nil {
			return nil, err
		}
		if f, ok := exact[pin]; ok {
			rows = append(rows, f)
		}
		under, err := scoped.SnapshotFilesUnder(pin)
		if err != nil {
			return nil, err
		}
		rows = append(rows, under...)
	} else if loader, ok := repo.(snapshotLoader); ok {
		snapshot, err := loader.Snapshot()
		if err != nil || snapshot == nil {
			return nil, err
		}
		for _, f := range snapshot.Files {
			if domain.PrefixCovers(pin, f.Path) {
				rows = append(rows, f)
			}
		}
	}
	covered := rows[:0]
	for _, f := range rows {
		if f.Encoding != chunker.EncodingZstd {
			covered = append(covered, f)
		}
	}
	return covered, nil
}
