package usecase

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type mrClient interface {
	Connect(ctx context.Context, host string) error
	CreateMergeRequest(ctx context.Context, org, project, title, description, sourceBranch, targetBranch string) (*domain.MergeRequest, error)
	UpdateMergeRequest(ctx context.Context, org, project string, number int64, title, description string) (*domain.MergeRequest, error)
	ListMergeRequests(ctx context.Context, org, project, status string, limit int) ([]*domain.MergeRequest, error)
	MergeMergeRequest(ctx context.Context, org, project string, number int64) (*domain.MergeRequest, *domain.Mergeability, error)
	CloseMergeRequest(ctx context.Context, org, project string, number int64) (*domain.MergeRequest, error)
	GetDefaultBranch(ctx context.Context, org, project string) (*serverDomain.Branch, error)
}

type mrLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
}

type CreateMergeRequestOptions struct {
	Title       string
	Description string
	Source      string
	Target      string
}

type MergeRequest struct {
	auth      *Auth
	client    mrClient
	localRepo mrLocalRepo
}

func NewMergeRequest(auth *Auth, client mrClient, localRepo mrLocalRepo) *MergeRequest {
	return &MergeRequest{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
	}
}

func (m *MergeRequest) Create(ctx context.Context, root string, opts CreateMergeRequestOptions) (*domain.MergeRequest, error) {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return nil, domain.NewUserError("a title is required")
	}
	url, cfg, err := m.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = cfg.Branch
	}
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		branch, err := m.client.GetDefaultBranch(ctx, url.Org, url.Project)
		if err != nil {
			return nil, err
		}
		target = branch.Name
	}
	if source == target {
		return nil, domain.NewUserError(fmt.Sprintf("source and target branches must differ (both are %q); pass --target", source))
	}
	return m.client.CreateMergeRequest(ctx, url.Org, url.Project, title, opts.Description, source, target)
}

func (m *MergeRequest) Update(ctx context.Context, root, id, title, description string) (*domain.MergeRequest, error) {
	number, err := mergeRequestNumber(id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" && strings.TrimSpace(description) == "" {
		return nil, domain.NewUserError("pass --title or --description to update a merge request")
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	return m.client.UpdateMergeRequest(ctx, url.Org, url.Project, number, title, description)
}

func (m *MergeRequest) List(ctx context.Context, root, status string, limit int) ([]*domain.MergeRequest, error) {
	status = strings.TrimSpace(status)
	if status != "" && !domain.IsValidMergeRequestStatus(status) {
		return nil, domain.NewUserError("status must be one of open, merged, closed")
	}
	if limit <= 0 {
		limit = 50
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	return m.client.ListMergeRequests(ctx, url.Org, url.Project, status, limit)
}

func (m *MergeRequest) Close(ctx context.Context, root, id string) (*domain.MergeRequest, error) {
	number, err := mergeRequestNumber(id)
	if err != nil {
		return nil, err
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	return m.client.CloseMergeRequest(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) Merge(ctx context.Context, root, id string) (*domain.MergeRequest, *domain.Mergeability, error) {
	number, err := mergeRequestNumber(id)
	if err != nil {
		return nil, nil, err
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	return m.client.MergeMergeRequest(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) connect(ctx context.Context, root string) (*domain.NipaUrl, *domain.Config, error) {
	if err := m.localRepo.Init(root); err != nil {
		return nil, nil, err
	}
	cfg, err := m.localRepo.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	url, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, nil, err
	}
	if err := m.client.Connect(ctx, url.Host); err != nil {
		return nil, nil, err
	}
	if err := m.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, nil, err
	}
	return url, cfg, nil
}

func mergeRequestNumber(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, domain.NewUserError("a merge request number is required")
	}
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number <= 0 {
		return 0, domain.NewUserError(fmt.Sprintf("invalid merge request number %q", raw))
	}
	return number, nil
}
