package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
)

type repoInterface interface {
	Connect(ctx context.Context, host string) error
	GetDefaultBranch(ctx context.Context, org, project string) (*domain.Branch, error)
	GetTreeNodeManifest(ctx context.Context, org, project, branch string) (*domain.TreeNode, error)
}

type Repo struct {
	auth          *Auth
	repoInterface repoInterface
}

func NewRepo(auth *Auth, repoInterface repoInterface) *Repo {
	return &Repo{
		auth:          auth,
		repoInterface: repoInterface,
	}
}

func (r *Repo) Clone(ctx context.Context, host, org, project, branch, path, target string) error {
	err := r.repoInterface.Connect(ctx, host)
	if err != nil {
		return err
	}
	err = r.auth.MakeSureLoggedIn(ctx, host)
	if err != nil {
		return err
	}
	domainBranch, err := r.repoInterface.GetDefaultBranch(ctx, org, project)
	if err != nil {
		return err
	}
	_, err = r.repoInterface.GetTreeNodeManifest(ctx, org, project, domainBranch.Name)
	if err != nil {
		return err
	}
	return nil
}
