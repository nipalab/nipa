package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=merge_request_mock_test.go -package=usecase
type mergeRequestRepository interface {
	Create(ctx context.Context, mr domain.MergeRequest) (*domain.MergeRequest, error)
	Get(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, error)
	List(ctx context.Context, projectID snow.ID, status string, limit int) ([]*domain.MergeRequest, error)
	Update(ctx context.Context, projectID snow.ID, number int64, title, description string) (*domain.MergeRequest, error)
	UpdateStatus(ctx context.Context, projectID snow.ID, number int64, status string, mergeCommitID *snow.ID) error
}

type branchMerger interface {
	GetMergeBase(ctx context.Context, projectID snow.ID, target, source MergeRef) (*MergeBaseInfo, error)
	FastForwardForMergeRequest(ctx context.Context, projectID snow.ID, targetBranch, sourceBranch string) (*domain.Branch, error)
	TreeDiffBetween(ctx context.Context, projectID snow.ID, baseID *snow.ID, headID snow.ID) ([]diff.FileDiff, error)
	BinaryChangesBetween(ctx context.Context, projectID snow.ID, fromCommitID, toCommitID *snow.ID) ([]string, error)
}

type MergeRequest struct {
	repo       mergeRequestRepository
	branchRepo branchRepository
	perm       permissionUsecase
	merger     branchMerger
	snowNode   snow.Node
	fileLocks  fileLockGate
}

func NewMergeRequest(repo mergeRequestRepository, branchRepo branchRepository, perm permissionUsecase, merger branchMerger, snowNode snow.Node) *MergeRequest {
	return &MergeRequest{
		repo:       repo,
		branchRepo: branchRepo,
		perm:       perm,
		merger:     merger,
		snowNode:   snowNode,
	}
}

// WithFileLocks enables binary lock checks on merge-request transitions.
func (m *MergeRequest) WithFileLocks(locks fileLockGate) *MergeRequest {
	m.fileLocks = locks
	return m
}

func (m *MergeRequest) Create(ctx context.Context, projectID snow.ID, title, description, sourceBranch, targetBranch string) (*domain.MergeRequest, error) {
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
	if m.fileLocks != nil {
		paths, err := m.merger.BinaryChangesBetween(ctx, projectID, info.MergeBaseCommitID, source.CommitID)
		if err != nil {
			return nil, err
		}
		if err := m.fileLocks.EnsureMergeRequestLocks(ctx, projectID, mrID, target, paths, claim.UserID); err != nil {
			_ = m.fileLocks.ReleaseForMergeRequest(ctx, projectID, mrID)
			return nil, err
		}
	}

	created, err := m.repo.Create(ctx, domain.MergeRequest{
		ID:                mrID.Int64(),
		ProjectID:         projectID,
		SourceBranchID:    source.ID,
		TargetBranchID:    target.ID,
		SourceBranch:      sourceBranch,
		TargetBranch:      targetBranch,
		Title:             title,
		Description:       strings.TrimSpace(description),
		Status:            domain.MergeRequestOpen,
		MergeBaseCommitID: info.MergeBaseCommitID,
		CreatedBy:         claim.UserID,
	})
	if err != nil {
		if m.fileLocks != nil {
			_ = m.fileLocks.ReleaseForMergeRequest(ctx, projectID, mrID)
		}
		return nil, err
	}
	return created, nil
}

func (m *MergeRequest) List(ctx context.Context, projectID snow.ID, status string, limit int) ([]*domain.MergeRequest, error) {
	if !m.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	status = strings.TrimSpace(status)
	if status != "" && !domain.IsValidMergeRequestStatus(status) {
		return nil, domain.NewErrorUser("invalid merge request status")
	}
	if limit <= 0 {
		limit = 50
	}
	return m.repo.List(ctx, projectID, status, limit)
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
	return m.repo.Update(ctx, projectID, number, title, description)
}

func (m *MergeRequest) Check(ctx context.Context, projectID snow.ID, number int64) (*domain.Mergeability, error) {
	mr, err := m.load(ctx, projectID, number)
	if err != nil {
		return nil, err
	}
	return m.check(ctx, projectID, mr)
}

func (m *MergeRequest) Merge(ctx context.Context, projectID snow.ID, number int64) (*domain.MergeRequest, *domain.Mergeability, error) {
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
		return nil, info, domain.NewErrorConflict("source branch is behind the target; update it first")
	case domain.MergeabilityUpToDate:
		return nil, info, domain.NewErrorConflict("source branch is already up to date with the target")
	default:
		return nil, info, domain.NewErrorConflict(fmt.Sprintf("merge request is %s", info.Status))
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
		claim, ok := domain.ClaimFromContext(ctx)
		if !ok {
			return nil, info, domain.NewErrorNoPermission()
		}
		if err := m.fileLocks.EnsureMergeRequestLocks(ctx, projectID, snow.ID(mr.ID), target, paths, claim.UserID); err != nil {
			return nil, info, err
		}
	}

	updated, err := m.merger.FastForwardForMergeRequest(ctx, projectID, mr.TargetBranch, mr.SourceBranch)
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
	return m.fileLocks.EnsureMergeRequestLocks(ctx, projectID, snow.ID(mr.ID), target, paths, holder)
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
	if base.MergeBaseCommitID == nil || *base.MergeBaseCommitID != *target.CommitID {
		info.Status = domain.MergeabilityBehind
		return info, nil
	}
	info.Status = domain.MergeabilityMergeable
	return info, nil
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
