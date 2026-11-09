package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
)

type lockClient interface {
	Connect(ctx context.Context, host string) error
	LockFile(ctx context.Context, org, project, path, branch string, ttl time.Duration) (*domain.FileLock, error)
	UnlockFile(ctx context.Context, org, project, path, branch string) error
	ListFileLocks(ctx context.Context, org, project string) ([]*domain.FileLock, error)
}

type lockLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
}

type FileLock struct {
	auth      *Auth
	client    lockClient
	localRepo lockLocalRepo
}

func NewFileLock(auth *Auth, client lockClient, localRepo lockLocalRepo) *FileLock {
	return &FileLock{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
	}
}

// LockOptions scopes a lock to a branch (empty = the clone's branch) and makes
// it expire after TTL (zero = permanent). Re-locking with a TTL renews it.
type LockOptions struct {
	Branch string
	TTL    time.Duration
}

func (f *FileLock) Lock(ctx context.Context, root, path string, opts LockOptions) (*domain.FileLock, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, domain.NewUserError("a path is required")
	}
	url, cfg, err := f.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(opts.Branch)
	if branch == "" {
		branch = cfg.Branch
	}
	lock, err := f.client.LockFile(ctx, url.Org, url.Project, path, branch, opts.TTL)
	if err != nil {
		return nil, err
	}
	if pins := f.pins(); pins != nil {
		pinned := path
		if lock != nil && lock.Path != "" {
			pinned = lock.Path
		}
		if err := pins.Pin(ctx, root, pinned); err != nil && lock != nil {
			lock.Warning = fmt.Sprintf("locked, but the old version of %s was not kept locally: %v", pinned, err)
		}
	}
	return lock, nil
}

func (f *FileLock) Unlock(ctx context.Context, root, path, branch string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return domain.NewUserError("a path is required")
	}
	url, cfg, err := f.connect(ctx, root)
	if err != nil {
		return err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = cfg.Branch
	}
	if err := f.client.UnlockFile(ctx, url.Org, url.Project, path, branch); err != nil {
		return err
	}
	if pins := f.pins(); pins != nil {
		if err := pins.Unpin(ctx, root, path); err != nil {
			slog.Warn("unlocked, but unpinning the local copy failed", "path", path, "error", err)
		}
	}
	return nil
}

func (f *FileLock) pins() *LockPins {
	repo, ok := f.localRepo.(pinLocalRepo)
	if !ok {
		return nil
	}
	downloader, _ := f.client.(ContentDownloader)
	return NewLockPins(f.auth, downloader, repo)
}

func (f *FileLock) List(ctx context.Context, root string) ([]*domain.FileLock, error) {
	url, _, err := f.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	return f.client.ListFileLocks(ctx, url.Org, url.Project)
}

func (f *FileLock) connect(ctx context.Context, root string) (*domain.NipaUrl, *domain.Config, error) {
	if err := f.localRepo.Init(root); err != nil {
		return nil, nil, err
	}
	cfg, err := f.localRepo.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	url, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, nil, err
	}
	if err := f.client.Connect(ctx, url.Origin); err != nil {
		return nil, nil, err
	}
	if err := f.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, nil, err
	}
	return url, cfg, nil
}
