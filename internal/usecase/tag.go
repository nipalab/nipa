package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=tag_mock_test.go -package=usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type tagRepository interface {
	ListTags(ctx context.Context, projectID snow.ID, limit int, createdBefore *time.Time, lastID snow.ID) ([]*domain.Tag, error)
	GetTagByName(ctx context.Context, projectID snow.ID, name string) (*domain.Tag, error)
	CreateTag(ctx context.Context, tag domain.Tag) (*domain.Tag, error)
	DeleteTag(ctx context.Context, projectID, tagID snow.ID) error
}

// TagTarget selects the commit a tag points at. CommitID and CommitHash take
// precedence over BranchName; an empty target resolves the default branch head.
type TagTarget struct {
	BranchName string
	CommitID   *snow.ID
	CommitHash *domain.Hash
}

type Tag struct {
	permUc     permissionUsecase
	tagRepo    tagRepository
	branchRepo branchRepository
	snowNode   snow.Node
	hooks      hookTagGate
}

// hookTagGate is the subset of the webhook emitter used by tag management.
type hookTagGate interface {
	EmitTag(ctx context.Context, event string, projectID snow.ID, tag *domain.Tag, actor snow.ID) error
}

func NewTag(permUc permissionUsecase, tagRepo tagRepository, branchRepo branchRepository, snowNode snow.Node) *Tag {
	return &Tag{
		permUc:     permUc,
		tagRepo:    tagRepo,
		branchRepo: branchRepo,
		snowNode:   snowNode,
	}
}

// WithHooks enables webhook events on this usecase.
func (t *Tag) WithHooks(hooks hookTagGate) *Tag {
	t.hooks = hooks
	return t
}

func (t *Tag) ListTags(ctx context.Context, projectID snow.ID, limit int, createdBefore *time.Time, lastID snow.ID) ([]*domain.Tag, error) {
	if !t.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	if lastID != 0 && createdBefore == nil {
		return nil, domain.NewErrorUser("last_created_at is required with last_id")
	}
	return t.tagRepo.ListTags(ctx, projectID, limit, createdBefore, lastID)
}

func (t *Tag) GetTagByName(ctx context.Context, projectID snow.ID, name string) (*domain.Tag, error) {
	if !t.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	return t.tagByName(ctx, projectID, name)
}

func (t *Tag) CreateTag(ctx context.Context, projectID snow.ID, name string, target TagTarget, message string) (*domain.Tag, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if !t.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	name = strings.TrimSpace(name)
	if err := validateTagName(name); err != nil {
		return nil, err
	}

	if _, err := t.tagRepo.GetTagByName(ctx, projectID, name); err == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("tag %q already exists", name))
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}

	commitID, err := t.resolveTarget(ctx, projectID, target)
	if err != nil {
		return nil, err
	}
	if commitID == nil {
		return nil, domain.NewErrorUser(fmt.Sprintf("cannot create tag %q: the project has no commits", name))
	}

	created, err := t.tagRepo.CreateTag(ctx, domain.Tag{
		ID:        t.snowNode.Generate(),
		ProjectID: projectID,
		Name:      name,
		CommitID:  *commitID,
		Message:   message,
		UserID:    claim.UserID,
	})
	if err != nil {
		return nil, err
	}
	t.emitHook(ctx, domain.WebhookEventTagCreated, projectID, created)
	return created, nil
}

func (t *Tag) DeleteTag(ctx context.Context, projectID snow.ID, name string) error {
	if _, ok := domain.ClaimFromContext(ctx); !ok {
		return domain.NewErrorNoPermission()
	}
	if !t.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return domain.NewErrorNoPermission()
	}
	tag, err := t.tagByName(ctx, projectID, name)
	if err != nil {
		return err
	}
	if err := t.tagRepo.DeleteTag(ctx, projectID, tag.ID); err != nil {
		return err
	}
	t.emitHook(ctx, domain.WebhookEventTagDeleted, projectID, tag)
	return nil
}

func (t *Tag) tagByName(ctx context.Context, projectID snow.ID, name string) (*domain.Tag, error) {
	tag, err := t.tagRepo.GetTagByName(ctx, projectID, name)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("tag %q not found", name))
	}
	if err != nil {
		return nil, err
	}
	return tag, nil
}

func (t *Tag) resolveTarget(ctx context.Context, projectID snow.ID, target TagTarget) (*snow.ID, error) {
	if target.CommitID != nil {
		commit, err := t.branchRepo.GetCommit(ctx, *target.CommitID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", target.CommitID.Base36()))
			}
			return nil, err
		}
		if commit.ProjectID != projectID {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", target.CommitID.Base36()))
		}
		id := commit.ID
		return &id, nil
	}

	if target.CommitHash != nil {
		commit, err := t.branchRepo.GetCommitByHash(ctx, *target.CommitHash)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", target.CommitHash.String()))
			}
			return nil, err
		}
		if commit.ProjectID != projectID {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", target.CommitHash.String()))
		}
		id := commit.ID
		return &id, nil
	}

	if target.BranchName != "" {
		branch, err := t.branchRepo.GetBranchByName(ctx, projectID, target.BranchName)
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", target.BranchName))
		}
		if err != nil {
			return nil, err
		}
		return branch.CommitID, nil
	}

	def, err := t.branchRepo.GetDefaultBranch(ctx, projectID)
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return def.CommitID, nil
}

func validateTagName(name string) error {
	if name == "" {
		return domain.NewErrorUser("tag name must not be empty")
	}
	if strings.ContainsRune(name, '/') {
		return domain.NewErrorUser(fmt.Sprintf("invalid tag name %q", name))
	}
	if name == "." || name == ".." {
		return domain.NewErrorUser(fmt.Sprintf("invalid tag name %q", name))
	}
	for _, r := range name {
		if r <= 0x20 || r == 0x7f {
			return domain.NewErrorUser(fmt.Sprintf("invalid tag name %q", name))
		}
	}
	return nil
}

// emitHook publishes a tag event. The change is already stored, so a delivery
// failure must not fail the operation.
func (t *Tag) emitHook(ctx context.Context, event string, projectID snow.ID, tag *domain.Tag) {
	if t.hooks == nil {
		return
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return
	}
	if err := t.hooks.EmitTag(ctx, event, projectID, tag, claim.UserID); err != nil {
		slog.Warn("emitting webhook tag event failed", "event", event, "project", projectID, "error", err)
	}
}
