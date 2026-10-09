package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=merge_request_check_mock_test.go -package=usecase
type mergeRequestCheckRepository interface {
	Upsert(ctx context.Context, check domain.MergeRequestCheck) (*domain.MergeRequestCheck, error)
	List(ctx context.Context, mergeRequestID int64, headCommitID snow.ID) ([]*domain.MergeRequestCheck, error)
}

// mergeRequestCheckNameLimit caps a check name; it must fit a branch's
// required-check list.
const mergeRequestCheckNameLimit = 128

// MergeRequestCheck manages external status checks reported for a merge
// request's head commit. Checks are head-keyed: a new push starts with a clean
// slate, and a required check must pass for the current head before merging.
type MergeRequestCheck struct {
	repo       mergeRequestCheckRepository
	mrRepo     mergeRequestRepository
	branchRepo branchRepository
	perm       permissionUsecase
	snowNode   snow.Node
	hooks      hookMergeRequestGate
}

func NewMergeRequestCheck(repo mergeRequestCheckRepository, mrRepo mergeRequestRepository,
	branchRepo branchRepository, perm permissionUsecase, snowNode snow.Node,
) *MergeRequestCheck {
	return &MergeRequestCheck{
		repo:       repo,
		mrRepo:     mrRepo,
		branchRepo: branchRepo,
		perm:       perm,
		snowNode:   snowNode,
	}
}

// WithHooks enables webhook events on this usecase.
func (c *MergeRequestCheck) WithHooks(hooks hookMergeRequestGate) *MergeRequestCheck {
	c.hooks = hooks
	return c
}

// Report records the latest result of a named check for the request's current
// source head. Any project writer may report.
func (c *MergeRequestCheck) Report(ctx context.Context, projectID snow.ID, number int64, name, state, detailsURL string) (*domain.MergeRequestCheck, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if !c.perm.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewErrorUser("a check name is required")
	}
	if len(name) > mergeRequestCheckNameLimit {
		return nil, domain.NewErrorUser("check name is too long")
	}
	state = strings.TrimSpace(state)
	if !domain.IsValidMergeRequestCheckState(state) {
		return nil, domain.NewErrorUser("state must be one of pending, success, failed")
	}
	mr, err := c.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	head, err := c.sourceHead(ctx, projectID, mr)
	if err != nil {
		return nil, err
	}
	if head == nil {
		return nil, domain.NewErrorConflict("the source branch has no commits")
	}
	check, err := c.repo.Upsert(ctx, domain.MergeRequestCheck{
		ID:             c.snowNode.Generate(),
		MergeRequestID: mr.ID,
		HeadCommitID:   *head,
		Name:           name,
		State:          state,
		DetailsURL:     strings.TrimSpace(detailsURL),
		Reporter:       domain.ReviewActor{UserID: claim.UserID},
	})
	if err != nil {
		return nil, err
	}
	if c.hooks != nil {
		if err := c.hooks.EmitMergeRequest(ctx, domain.WebhookEventMRCheckReported, projectID, mr, claim.UserID); err != nil {
			slog.Warn("emitting check webhook failed", "merge_request", mr.Number, "error", err)
		}
	}
	return check, nil
}

// List returns the checks reported for the request's current source head.
func (c *MergeRequestCheck) List(ctx context.Context, projectID snow.ID, number int64) ([]*domain.MergeRequestCheck, error) {
	mr, err := c.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	head, err := c.sourceHead(ctx, projectID, mr)
	if err != nil {
		return nil, err
	}
	if head == nil {
		return []*domain.MergeRequestCheck{}, nil
	}
	return c.repo.List(ctx, mr.ID, *head)
}

// BlockedBy reports whether the target branch's required checks have all
// succeeded for the given head. It is a no-op when the branch does not require
// checks or none are configured.
func (c *MergeRequestCheck) BlockedBy(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest, target *domain.Branch, head snow.ID) (string, error) {
	if !target.RequireStatusChecks {
		return "", nil
	}
	required, err := c.branchRepo.RequiredChecks(ctx, target.ID)
	if err != nil {
		return "", err
	}
	if len(required) == 0 {
		return "", nil
	}
	checks, err := c.repo.List(ctx, mr.ID, head)
	if err != nil {
		return "", err
	}
	passed := make(map[string]bool, len(checks))
	for _, check := range checks {
		if check.State == domain.MergeRequestCheckSuccess {
			passed[check.Name] = true
		}
	}
	for _, name := range required {
		if !passed[name] {
			return domain.MergeabilityBlockedChecks, nil
		}
	}
	return "", nil
}

func (c *MergeRequestCheck) load(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	if number <= 0 {
		return nil, domain.NewErrorUser("invalid merge request number")
	}
	if !c.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := c.mrRepo.Get(ctx, projectID, number)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("merge request #%d not found", number))
	}
	if err != nil {
		return nil, err
	}
	return mr, nil
}

func (c *MergeRequestCheck) sourceHead(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest) (*snow.ID, error) {
	source, err := c.branchRepo.GetBranchByName(ctx, projectID, mr.SourceBranch)
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return source.CommitID, nil
}
