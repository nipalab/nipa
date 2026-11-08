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
	snapshot, err := p.localRepo.Snapshot()
	if err != nil || snapshot == nil {
		return err
	}
	var covered []clientDomain.SnapshotFile
	for _, f := range snapshot.Files {
		if f.Encoding != chunker.EncodingZstd && domain.PrefixCovers(pin, f.Path) {
			covered = append(covered, f)
		}
	}
	return pristine{root: root, repo: p.localRepo, auth: p.auth, client: p.client}.ensureSnapshotContent(ctx, covered)
}

// Unpin drops the lock path and prunes the content it kept.
func (p *LockPins) Unpin(ctx context.Context, root, path string) (err error) {
	defer func() { pruneAfterSuccess(p.localRepo, err) }()
	if err := p.localRepo.Init(root); err != nil {
		return err
	}
	unlock, err := lockShared(p.localRepo)
	if err != nil {
		return err
	}
	defer unlock()
	return p.localRepo.UnpinPaths([]string{path})
}
