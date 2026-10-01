package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type tagClient interface {
	Connect(ctx context.Context, host string) error
	CreateTag(ctx context.Context, org, project, name, message, fromBranch, fromCommitID, fromCommitHash string) (*domain.Tag, error)
	ListTags(ctx context.Context, org, project string) ([]*domain.Tag, error)
	DeleteTag(ctx context.Context, org, project, name string) error
}

type tagLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
	LoadCommit() (*domain.LocalCommit, error)
}

// TagTarget selects the commit a new tag points at. An empty target tags the
// pinned HEAD commit of the local working copy.
type TagTarget struct {
	Branch string
	Commit string
}

type Tag struct {
	auth      *Auth
	client    tagClient
	localRepo tagLocalRepo
}

func NewTag(auth *Auth, client tagClient, localRepo tagLocalRepo) *Tag {
	return &Tag{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
	}
}

func (t *Tag) Create(ctx context.Context, root, name, message string, target TagTarget) (*domain.Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewUserError("tag name is required")
	}
	branch := strings.TrimSpace(target.Branch)
	commit := strings.TrimSpace(target.Commit)
	if branch != "" && commit != "" {
		return nil, domain.NewUserError("--branch and --commit are mutually exclusive")
	}
	url, _, err := t.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	if branch == "" && commit == "" {
		localCommit, err := t.localRepo.LoadCommit()
		if err != nil {
			return nil, err
		}
		if localCommit == nil || localCommit.CommitID == "" {
			return nil, domain.NewUserError("no commit checked out; run nipa update first, or pass --branch/--commit")
		}
		return t.client.CreateTag(ctx, url.Org, url.Project, name, message, "", localCommit.CommitID, localCommit.CommitHash)
	}
	if branch != "" {
		return t.client.CreateTag(ctx, url.Org, url.Project, name, message, branch, "", "")
	}
	if hash, err := serverDomain.ParseHashHex(commit); err == nil {
		return t.client.CreateTag(ctx, url.Org, url.Project, name, message, "", "", hash.String())
	}
	id, err := snow.ParseBase36(commit)
	if err != nil {
		return nil, domain.NewUserError(fmt.Sprintf("invalid commit %q: expected a base36 commit id or hex hash", commit))
	}
	return t.client.CreateTag(ctx, url.Org, url.Project, name, message, "", id.Base36(), "")
}

func (t *Tag) List(ctx context.Context, root string) ([]*domain.Tag, error) {
	url, _, err := t.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	return t.client.ListTags(ctx, url.Org, url.Project)
}

func (t *Tag) Delete(ctx context.Context, root, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.NewUserError("tag name is required")
	}
	url, _, err := t.connect(ctx, root)
	if err != nil {
		return err
	}
	return t.client.DeleteTag(ctx, url.Org, url.Project, name)
}

func (t *Tag) connect(ctx context.Context, root string) (*domain.NipaUrl, *domain.Config, error) {
	if err := t.localRepo.Init(root); err != nil {
		return nil, nil, err
	}
	cfg, err := t.localRepo.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	url, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, nil, err
	}
	if err := t.client.Connect(ctx, url.Host); err != nil {
		return nil, nil, err
	}
	if err := t.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, nil, err
	}
	return url, cfg, nil
}
