package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=merge_request_mock_test.go -package=usecase
type mergeRequestRepository interface {
	Create(ctx context.Context, mr domain.MergeRequest) (*domain.MergeRequest, error)
	Get(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error)
	List(ctx context.Context, projectID snow.ID, opts domain.MergeRequestListOptions) ([]*domain.MergeRequest, error)
	Update(ctx context.Context, projectID snow.ID, number int64, title, description string) (*domain.MergeRequest, error)
	SetDraft(ctx context.Context, projectID snow.ID, number int64, draft bool) (*domain.MergeRequest, error)
	UpdateStatus(ctx context.Context, projectID snow.ID, number int64, status string, mergeCommitID *snow.ID) error
}

// transactor runs a function inside a database transaction; repositories
// constructed with dbtx.New resolve that transaction from the context.
type transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type branchMerger interface {
	GetMergeBase(ctx context.Context, projectID snow.ID, target, source MergeRef) (*MergeBaseInfo, error)
	FastForwardForMergeRequest(ctx context.Context, projectID snow.ID, targetBranch, sourceBranch string) (*domain.Branch, error)
	MergeForMergeRequest(ctx context.Context, projectID snow.ID, targetBranch, sourceBranch string, opts MergeCommitOptions) (*domain.Branch, error)
	Delete(ctx context.Context, projectID snow.ID, name string) error
	TreeDiffBetween(ctx context.Context, projectID snow.ID, baseID *snow.ID, headID snow.ID) ([]diff.FileDiff, error)
	BinaryChangesBetween(ctx context.Context, projectID snow.ID, fromCommitID, toCommitID *snow.ID) ([]string, error)
}

// hookMergeRequestGate is the subset of the webhook emitter used by the merge
// request lifecycle.
type hookMergeRequestGate interface {
	EmitMergeRequest(ctx context.Context, event string, projectID snow.ID, mr *domain.MergeRequest, actor snow.ID) error
}

type MergeRequest struct {
	repo       mergeRequestRepository
	branchRepo branchRepository
	perm       permissionUsecase
	merger     branchMerger
	snowNode   snow.Node
	fileLocks  fileLockGate
	hooks      hookMergeRequestGate
	transactor transactor
	review     *MergeRequestReview
}

// mergeRequestCommitLimit caps how many commits a merge request lists.
const mergeRequestCommitLimit = 250

func NewMergeRequest(repo mergeRequestRepository, branchRepo branchRepository, perm permissionUsecase, merger branchMerger, snowNode snow.Node, tx transactor) *MergeRequest {
	return &MergeRequest{
		repo:       repo,
		branchRepo: branchRepo,
		perm:       perm,
		merger:     merger,
		snowNode:   snowNode,
		transactor: tx,
	}
}

// WithFileLocks enables binary lock checks on merge-request transitions.
func (m *MergeRequest) WithFileLocks(locks fileLockGate) *MergeRequest {
	m.fileLocks = locks
	return m
}

// WithHooks enables webhook events on this usecase.
func (m *MergeRequest) WithHooks(hooks hookMergeRequestGate) *MergeRequest {
	m.hooks = hooks
	return m
}

// WithReview attaches the review usecase: it enables the approval gate on
// merge and the lifecycle timeline events.
func (m *MergeRequest) WithReview(review *MergeRequestReview) *MergeRequest {
	m.review = review
	return m
}

func (m *MergeRequest) Create(ctx context.Context, projectID snow.ID, title, description, sourceBranch, targetBranch string, draft bool) (*domain.MergeRequest, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if !m.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, domain.NewErrorUser("title is required")
	}
	sourceBranch = strings.TrimSpace(sourceBranch)
	targetBranch = strings.TrimSpace(targetBranch)
	if sourceBranch == "" || targetBranch == "" {
		return nil, domain.NewErrorUser("source and target branches are required")
	}
	if sourceBranch == targetBranch {
		return nil, domain.NewErrorUser("source and target branches must differ")
	}

	source, err := m.branchRepo.GetBranchByName(ctx, projectID, sourceBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", sourceBranch))
	}
	if err != nil {
		return nil, err
	}
	if source.CommitID == nil {
		return nil, domain.NewErrorUser(fmt.Sprintf("branch %q has no commits", sourceBranch))
	}
	target, err := m.branchRepo.GetBranchByName(ctx, projectID, targetBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", targetBranch))
	}
	if err != nil {
		return nil, err
	}
	if target.CommitID == nil {
		return nil, domain.NewErrorUser(fmt.Sprintf("branch %q has no commits", targetBranch))
	}

	info, err := m.merger.GetMergeBase(ctx, projectID,
		MergeRef{CommitID: target.CommitID}, MergeRef{CommitID: source.CommitID})
	if err != nil {
		return nil, err
	}

	mrID := m.snowNode.Generate()

	var paths []string
	if m.fileLocks != nil {
		paths, err = m.merger.BinaryChangesBetween(ctx, projectID, info.MergeBaseCommitID, source.CommitID)
		if err != nil {
			return nil, err
		}
	}

	// Locks reference the merge request row, so both are written in one
	// transaction: a lock failure rolls the row back.
	var created *domain.MergeRequest
	err = m.transactor.WithinTx(ctx, func(txCtx context.Context) error {
		var createErr error
		created, createErr = m.repo.Create(txCtx, domain.MergeRequest{
			ID:                mrID.Int64(),
			ProjectID:         projectID,
			SourceBranchID:    source.ID,
			TargetBranchID:    target.ID,
			SourceBranch:      sourceBranch,
			TargetBranch:      targetBranch,
			Title:             title,
			Description:       strings.TrimSpace(description),
			Status:            domain.MergeRequestOpen,
			Draft:             draft,
			MergeBaseCommitID: info.MergeBaseCommitID,
			CreatedBy:         claim.UserID,
		})
		if createErr != nil {
			return createErr
		}
		if m.fileLocks != nil {
			if err := m.fileLocks.EnsureMergeRequestLocks(txCtx, projectID, mrID, target, paths, claim.UserID, claim.UserID); err != nil {
				return err
			}
		}
		return m.appendEvent(txCtx, domain.MergeRequestEventOpened, created, nil, "", claim.UserID)
	})
	if err != nil {
		return nil, err
	}

	m.emitHook(ctx, domain.WebhookEventMRCreated, projectID, created, claim.UserID)
	return created, nil
}

func (m *MergeRequest) List(ctx context.Context, projectID snow.ID, opts domain.MergeRequestListOptions) ([]*domain.MergeRequest, error) {
	if !m.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	opts.Status = strings.TrimSpace(opts.Status)
	if opts.Status != "" && !domain.IsValidMergeRequestStatus(opts.Status) {
		return nil, domain.NewErrorUser("invalid merge request status")
	}
	opts.SourceBranch = strings.TrimSpace(opts.SourceBranch)
	opts.TargetBranch = strings.TrimSpace(opts.TargetBranch)
	if opts.After < 0 {
		return nil, domain.NewErrorUser("invalid merge request cursor")
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	return m.repo.List(ctx, projectID, opts)
}

func (m *MergeRequest) Get(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	return m.load(ctx, projectID, number)
}

func (m *MergeRequest) Update(ctx context.Context, projectID snow.ID, number int64, title, description string) (*domain.MergeRequest, error) {
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.UserID != mr.CreatedBy && !m.perm.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	if mr.Status != domain.MergeRequestOpen {
		return nil, domain.NewErrorConflict(fmt.Sprintf("merge request is %s", mr.Status))
	}
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	if title == "" && description == "" {
		return nil, domain.NewErrorUser("title or description is required")
	}
	if title == "" {
		title = mr.Title
	}
	if description == "" {
		description = mr.Description
	}
	updated, err := m.repo.Update(ctx, projectID, number, title, description)
	if err != nil {
		return nil, err
	}
	m.emitHook(ctx, domain.WebhookEventMRUpdated, projectID, updated, claim.UserID)
	return updated, nil
}

// SetDraft toggles the draft state of an open merge request. Marking a draft
// ready emits the ready_for_review event; entering the draft state only
// updates the request. Draft requests cannot be merged.
func (m *MergeRequest) SetDraft(ctx context.Context, projectID snow.ID, number int64, draft bool) (*domain.MergeRequest, error) {
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.UserID != mr.CreatedBy && !m.perm.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	if mr.Status != domain.MergeRequestOpen {
		return nil, domain.NewErrorConflict(fmt.Sprintf("merge request is %s", mr.Status))
	}
	if mr.Draft == draft {
		return mr, nil
	}
	updated, err := m.repo.SetDraft(ctx, projectID, number, draft)
	if err != nil {
		return nil, err
	}
	if draft {
		m.emitHook(ctx, domain.WebhookEventMRUpdated, projectID, updated, claim.UserID)
		return updated, nil
	}
	m.emitHook(ctx, domain.WebhookEventMRReady, projectID, updated, claim.UserID)
	m.noteEvent(ctx, domain.MergeRequestEventReady, updated, nil, claim.UserID)
	return updated, nil
}

func (m *MergeRequest) Check(ctx context.Context, projectID snow.ID, number int64) (*domain.Mergeability, error) {
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	return m.check(ctx, projectID, mr)
}

func (m *MergeRequest) Merge(ctx context.Context, projectID snow.ID, number int64, strategy string, deleteSource bool) (*domain.MergeRequest, *domain.Mergeability, error) {
	strategy = strings.TrimSpace(strategy)
	if strategy == "" {
		strategy = domain.MergeStrategyFastForward
	}
	if !domain.IsValidMergeStrategy(strategy) {
		return nil, nil, domain.NewErrorUser("strategy must be one of ff, merge, squash, rebase")
	}
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, nil, err
	}
	info, err := m.check(ctx, projectID, mr)
	if err != nil {
		return nil, nil, err
	}
	switch info.Status {
	case domain.MergeabilityMergeable:
	case domain.MergeabilityBehind:
		if strategy == domain.MergeStrategyFastForward {
			return nil, info, domain.NewErrorConflict("source branch is behind the target; update it first")
		}
	case domain.MergeabilityUpToDate:
		return nil, info, domain.NewErrorConflict("source branch is already up to date with the target")
	default:
		return nil, info, domain.NewErrorConflict(fmt.Sprintf("merge request is %s", info.Status))
	}
	if info.BlockedBy != "" {
		return nil, info, blockedMergeError(info.BlockedBy)
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, info, domain.NewErrorNoPermission()
	}

	if m.fileLocks != nil {
		target, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.TargetBranch)
		if err != nil {
			return nil, info, err
		}
		paths, err := m.merger.BinaryChangesBetween(ctx, projectID, info.MergeBaseCommitID, info.SourceCommitID)
		if err != nil {
			return nil, info, err
		}
		if err := m.fileLocks.EnsureMergeRequestLocks(ctx, projectID, snow.ID(mr.ID), target, paths, claim.UserID, mr.CreatedBy); err != nil {
			return nil, info, err
		}
	}

	opts := MergeCommitOptions{Strategy: strategy, Author: claim.UserID}
	if strategy == domain.MergeStrategySquash {
		opts.Message = mr.Title
		opts.Author = mr.CreatedBy
	}
	updated, err := m.merger.MergeForMergeRequest(ctx, projectID, mr.TargetBranch, mr.SourceBranch, opts)
	if err != nil {
		return nil, info, err
	}
	if err := m.repo.UpdateStatus(ctx, projectID, mr.Number, domain.MergeRequestMerged, updated.CommitID); err != nil {
		return nil, info, err
	}
	if m.fileLocks != nil {
		if err := m.fileLocks.ReleaseForMergeRequest(ctx, projectID, snow.ID(mr.ID)); err != nil {
			return nil, info, err
		}
	}
	merged, err := m.repo.Get(ctx, projectID, number)
	if err != nil {
		return nil, info, err
	}
	m.emitHook(ctx, domain.WebhookEventMRMerged, projectID, merged, claim.UserID)
	m.noteEvent(ctx, domain.MergeRequestEventMerged, merged, updated.CommitID, claim.UserID)
	if deleteSource {
		if err := m.merger.Delete(ctx, projectID, mr.SourceBranch); err != nil {
			slog.Warn("deleting merged source branch failed",
				"branch", mr.SourceBranch, "merge_request", mr.Number, "error", err)
		}
	}
	return merged, info, nil
}

func (m *MergeRequest) Close(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	mr, err := m.setStatus(ctx, projectID, number, domain.MergeRequestOpen, domain.MergeRequestClosed)
	if err != nil {
		return nil, err
	}
	if m.fileLocks != nil {
		if err := m.fileLocks.ReleaseForMergeRequest(ctx, projectID, snow.ID(mr.ID)); err != nil {
			return nil, err
		}
	}
	if claim, ok := domain.ClaimFromContext(ctx); ok {
		m.emitHook(ctx, domain.WebhookEventMRClosed, projectID, mr, claim.UserID)
		m.noteEvent(ctx, domain.MergeRequestEventClosed, mr, nil, claim.UserID)
	}
	return mr, nil
}

func (m *MergeRequest) Reopen(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	mr, err := m.setStatus(ctx, projectID, number, domain.MergeRequestClosed, domain.MergeRequestOpen)
	if err != nil {
		return nil, err
	}
	if claim, ok := domain.ClaimFromContext(ctx); ok {
		if err := m.ensureLocksForRequest(ctx, projectID, mr, claim.UserID); err != nil {
			return nil, err
		}
		m.emitHook(ctx, domain.WebhookEventMRReopened, projectID, mr, claim.UserID)
		m.noteEvent(ctx, domain.MergeRequestEventReopened, mr, nil, claim.UserID)
	}
	return mr, nil
}

func (m *MergeRequest) ensureLocksForRequest(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest, holder snow.ID) error {
	if m.fileLocks == nil {
		return nil
	}
	target, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.TargetBranch)
	if err != nil {
		return err
	}
	source, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.SourceBranch)
	if err != nil {
		return err
	}
	var baseID *snow.ID
	if target.CommitID != nil && source.CommitID != nil {
		info, err := m.merger.GetMergeBase(ctx, projectID, MergeRef{CommitID: target.CommitID}, MergeRef{CommitID: source.CommitID})
		if err != nil {
			return err
		}
		baseID = info.MergeBaseCommitID
	}
	paths, err := m.merger.BinaryChangesBetween(ctx, projectID, baseID, source.CommitID)
	if err != nil {
		return err
	}
	return m.fileLocks.EnsureMergeRequestLocks(ctx, projectID, snow.ID(mr.ID), target, paths, holder, mr.CreatedBy)
}

func (m *MergeRequest) Diff(ctx context.Context, projectID snow.ID, number int64) ([]diff.FileDiff, error) {
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	source, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.SourceBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", mr.SourceBranch))
	}
	if err != nil {
		return nil, err
	}
	if source.CommitID == nil {
		return []diff.FileDiff{}, nil
	}

	var baseID *snow.ID
	target, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.TargetBranch)
	if err == nil && target.CommitID != nil {
		info, err := m.merger.GetMergeBase(ctx, projectID,
			MergeRef{CommitID: target.CommitID}, MergeRef{CommitID: source.CommitID})
		if err != nil {
			return nil, err
		}
		baseID = info.MergeBaseCommitID
	} else if err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	return m.merger.TreeDiffBetween(ctx, projectID, baseID, *source.CommitID)
}

// Commits lists the source-branch commits the merge request adds on top of the
// merge base with its target, newest first, with their authors. Commits already
// on the target are not part of the request, so the log stops at the merge base.
func (m *MergeRequest) Commits(ctx context.Context, projectID snow.ID, number int64) ([]*domain.CommitLogEntry, error) {
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	source, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.SourceBranch)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", mr.SourceBranch))
	}
	if err != nil {
		return nil, err
	}
	if source.CommitID == nil {
		return []*domain.CommitLogEntry{}, nil
	}

	var stop snow.ID
	target, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.TargetBranch)
	if err == nil && target.CommitID != nil {
		info, err := m.merger.GetMergeBase(ctx, projectID,
			MergeRef{CommitID: target.CommitID}, MergeRef{CommitID: source.CommitID})
		if err != nil {
			return nil, err
		}
		if info.MergeBaseCommitID != nil {
			stop = *info.MergeBaseCommitID
		}
	} else if err != nil && !domain.IsErrorNotFound(err) {
		return nil, err
	}
	return m.branchRepo.CommitLogUntil(ctx, projectID, *source.CommitID, stop, mergeRequestCommitLimit)
}

func (m *MergeRequest) load(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error) {
	if number <= 0 {
		return nil, domain.NewErrorUser("invalid merge request number")
	}
	if !m.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := m.repo.Get(ctx, projectID, number)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("merge request #%d not found", number))
	}
	if err != nil {
		return nil, err
	}
	return mr, nil
}

func (m *MergeRequest) check(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest) (*domain.Mergeability, error) {
	info := &domain.Mergeability{Status: mr.Status}
	switch mr.Status {
	case domain.MergeRequestMerged, domain.MergeRequestClosed:
		return info, nil
	}

	source, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.SourceBranch)
	if domain.IsErrorNotFound(err) {
		info.Status = domain.MergeabilityInvalid
		return info, nil
	}
	if err != nil {
		return nil, err
	}
	target, err := m.branchRepo.GetBranchByName(ctx, projectID, mr.TargetBranch)
	if domain.IsErrorNotFound(err) {
		info.Status = domain.MergeabilityInvalid
		return info, nil
	}
	if err != nil {
		return nil, err
	}
	if source.ID != mr.SourceBranchID || target.ID != mr.TargetBranchID {
		info.Status = domain.MergeabilityInvalid
		return info, nil
	}
	info.SourceCommitID = source.CommitID
	info.TargetCommitID = target.CommitID
	if mr.Draft {
		info.BlockedBy = domain.MergeabilityBlockedDraft
	}
	if source.CommitID == nil || target.CommitID == nil {
		info.Status = domain.MergeabilityInvalid
		return info, nil
	}
	if *source.CommitID == *target.CommitID {
		info.Status = domain.MergeabilityUpToDate
		return info, nil
	}

	base, err := m.merger.GetMergeBase(ctx, projectID,
		MergeRef{CommitID: target.CommitID}, MergeRef{CommitID: source.CommitID})
	if err != nil {
		return nil, err
	}
	info.MergeBaseCommitID = base.MergeBaseCommitID
	switch {
	case base.MergeBaseCommitID == nil:
		info.Status = domain.MergeabilityBehind
	case *base.MergeBaseCommitID == *source.CommitID:
		// The source is contained in the target: every strategy has nothing to
		// land.
		info.Status = domain.MergeabilityUpToDate
	case *base.MergeBaseCommitID != *target.CommitID:
		info.Status = domain.MergeabilityBehind
	default:
		info.Status = domain.MergeabilityMergeable
	}
	// The review policy applies to every shape the merge could land, including
	// a diverged source that a non-fast-forward strategy would merge.
	if info.Status == domain.MergeabilityMergeable || info.Status == domain.MergeabilityBehind {
		if info.BlockedBy == "" {
			blocked, err := m.reviewBlockedBy(ctx, projectID, mr, target)
			if err != nil {
				return nil, err
			}
			info.BlockedBy = blocked
		}
	}
	return info, nil
}

// reviewBlockedBy reports the review-policy reason the target branch refuses
// the merge for: live change requests always block, and a target branch can
// additionally require a number of live approvals.
func (m *MergeRequest) reviewBlockedBy(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest, target *domain.Branch) (string, error) {
	if m.review == nil {
		return "", nil
	}
	state, err := m.review.ReviewState(ctx, projectID, mr.Number)
	if err != nil {
		return "", err
	}
	if state.ChangesRequested > 0 {
		return domain.MergeabilityBlockedChangesRequested, nil
	}
	if target.RequiredApprovals > int64(state.Approvals) {
		return domain.MergeabilityBlockedApprovals, nil
	}
	return "", nil
}

func blockedMergeError(blockedBy string) error {
	switch blockedBy {
	case domain.MergeabilityBlockedChangesRequested:
		return domain.NewErrorConflict("merge request has unresolved change requests")
	case domain.MergeabilityBlockedDraft:
		return domain.NewErrorConflict("merge request is a draft; mark it ready for review first")
	}
	return domain.NewErrorConflict("merge request does not have the approvals required by the target branch")
}

// appendEvent writes a lifecycle timeline event; a nil review usecase disables
// events.
func (m *MergeRequest) appendEvent(ctx context.Context, kind string, mr *domain.MergeRequest, commitID *snow.ID, commitHash string, actor snow.ID) error {
	if m.review == nil {
		return nil
	}
	return m.review.NoteEvent(ctx, mr, kind, commitID, commitHash, actor)
}

// noteEvent records a lifecycle event after the state change is already
// stored, so a bookkeeping failure must not fail the operation. A commit ID is
// enriched with its hash so the timeline can display the landed commit.
func (m *MergeRequest) noteEvent(ctx context.Context, kind string, mr *domain.MergeRequest, commitID *snow.ID, actor snow.ID) {
	if m.review == nil {
		return
	}
	var commitHash string
	if commitID != nil {
		if commit, err := m.branchRepo.GetCommit(ctx, *commitID); err == nil {
			commitHash = commit.Hash.String()
		}
	}
	if err := m.appendEvent(ctx, kind, mr, commitID, commitHash, actor); err != nil {
		slog.Warn("writing merge request timeline event failed", "event", kind, "merge_request", mr.Number, "error", err)
	}
}

// emitHook publishes a merge request event. The state change is already
// stored, so a delivery failure must not fail the operation.
func (m *MergeRequest) emitHook(ctx context.Context, event string, projectID snow.ID, mr *domain.MergeRequest, actor snow.ID) {
	if m.hooks == nil {
		return
	}
	if err := m.hooks.EmitMergeRequest(ctx, event, projectID, mr, actor); err != nil {
		slog.Warn("emitting webhook merge request event failed", "event", event, "project", projectID, "error", err)
	}
}

func (m *MergeRequest) setStatus(ctx context.Context, projectID snow.ID, number int64, from, to string) (*domain.MergeRequest, error) {
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if claim.UserID != mr.CreatedBy && !m.perm.AdminHasProject(ctx, projectID) {
		return nil, domain.NewErrorNoPermission()
	}
	if mr.Status != from {
		return nil, domain.NewErrorConflict(fmt.Sprintf("merge request is %s", mr.Status))
	}
	var mergeCommitID *snow.ID
	if to == domain.MergeRequestMerged {
		mergeCommitID = mr.MergeCommitID
	}
	if err := m.repo.UpdateStatus(ctx, projectID, number, to, mergeCommitID); err != nil {
		return nil, err
	}
	return m.repo.Get(ctx, projectID, number)
}
