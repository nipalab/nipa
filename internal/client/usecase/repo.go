package usecase

import (
	"context"
	"fmt"
	"os"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type repoInterface interface {
	GetDefaultBranch(ctx context.Context, org, project string) (*serverDomain.Branch, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error)
	ListBranches(ctx context.Context, org, project string) ([]*serverDomain.Branch, error)
	DownloadChunks(ctx context.Context, hashes []serverDomain.Hash) (map[serverDomain.Hash][]byte, error)
}

type localRepo interface {
	Init(target string) error
	SaveConfig(cfg domain.Config) error
	SaveTree(root *serverDomain.TreeNode) error
	Snapshot() (*domain.Snapshot, error)
	ListStaged() ([]string, error)
	StageAdd(path string) error
	StageRemove(paths []string) error
	MissingChunks(hashes []serverDomain.Hash) ([]serverDomain.Hash, error)
	StoreChunk(hash serverDomain.Hash, data []byte) error
	LoadChunk(hash serverDomain.Hash) ([]byte, error)
}

type Repo struct {
	auth          *Auth
	repoInterface repoInterface
	localRepo     localRepo
}

func NewRepo(auth *Auth, repoInterface repoInterface, localRepo localRepo) *Repo {
	return &Repo{
		auth:          auth,
		repoInterface: repoInterface,
		localRepo:     localRepo,
	}
}

func (r *Repo) Clone(ctx context.Context, url, host, org, project, branch, path, target string) error {
	if err := ensureEmptyTarget(target); err != nil {
		return err
	}
	err := r.auth.MakeSureLoggedIn(ctx, host)
	if err != nil {
		return err
	}
	if branch == "" {
		domainBranch, err := r.repoInterface.GetDefaultBranch(ctx, org, project)
		if err != nil {
			return err
		}
		branch = domainBranch.Name
	}
	root, err := r.repoInterface.GetTreeNodeManifest(ctx, org, project, branch, path)
	if err != nil {
		return err
	}
	if err := r.localRepo.Init(target); err != nil {
		return err
	}
	if err := r.localRepo.SaveConfig(domain.Config{Url: url, Branch: branch}); err != nil {
		return err
	}
	if path == "" {
		if err := syncWorkingCopy(ctx, r.repoInterface, r.localRepo, target, root); err != nil {
			return err
		}
	}
	return r.localRepo.SaveTree(root)
}

func (r *Repo) ListBranches(ctx context.Context, host, org, project string) ([]*serverDomain.Branch, error) {
	if err := r.auth.MakeSureLoggedIn(ctx, host); err != nil {
		return nil, err
	}
	return r.repoInterface.ListBranches(ctx, org, project)
}

func ensureEmptyTarget(target string) error {
	info, err := os.Stat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("target %q is not a directory", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("target directory %q is not empty", target)
	}
	return nil
}
