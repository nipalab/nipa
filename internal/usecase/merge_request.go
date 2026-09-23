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
	Get(ctx context.Context, projectID snow.ID, id int64) (*domain.MergeRequest, error)
	List(ctx context.Context, projectID snow.ID, status string, limit int) ([]*domain.MergeRequest, error)
	Update(ctx context.Context, projectID snow.ID, id int64, title, description string) (*domain.MergeRequest, error)
	UpdateStatus(ctx context.Context, projectID snow.ID, id int64, status string, mergeCommitID *snow.ID) error
}

type branchMerger interface {
	GetMergeBase(ctx context.Context, projectID snow.ID, target, source MergeRef) (*MergeBaseInfo, error)
	FastForwardForMergeRequest(ctx context.Context, projectID snow.ID, targetBranch, sourceBranch string) (*domain.Branch, error)
	TreeDiffBetween(ctx context.Context, projectID snow.ID, baseID *snow.ID, headID snow.ID) ([]diff.FileDiff, error)
}

type MergeRequest struct {
	repo       mergeRequestRepository
	branchRepo branchRepository
	perm       permissionUsecase
	merger     branchMerger
	snowNode   snow.Node
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

	return m.repo.Create(ctx, domain.MergeRequest{
		ID:                m.snowNode.Generate().Int64(),
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

func (m *MergeRequest) Get(ctx context.Context, projectID snow.ID, id int64) (*domain.MergeRequest, error) {
	return m.load(ctx, projectID, id)
}

func (m *MergeRequest) Update(ctx context.Context, projectID snow.ID, id int64, title, description string) (*domain.MergeRequest, error) {
	mr, err := m.load(ctx, projectID, id)
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
	return m.repo.Update(ctx, projectID, id, title, description)
}

func (m *MergeRequest) Check(ctx context.Context, projectID snow.ID, id int64) (*domain.Mergeability, error) {
	mr, err := m.load(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	return m.check(ctx, projectID, mr)
}

func (m *MergeRequest) Merge(ctx context.Context, projectID snow.ID, id int64) (*domain.MergeRequest, *domain.Mergeability, error) {
	mr, err := m.load(ctx, projectID, id)
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

	updated, err := m.merger.FastForwardForMergeRequest(ctx, projectID, mr.TargetBranch, mr.SourceBranch)
	if err != nil {
		return nil, info, err
	}
	if err := m.repo.UpdateStatus(ctx, projectID, mr.ID, domain.MergeRequestMerged, updated.CommitID); err != nil {
		return nil, info, err
	}
	merged, err := m.repo.Get(ctx, projectID, id)
	if err != nil {
		return nil, info, err
	}
	return merged, info, nil
}

func (m *MergeRequest) Close(ctx context.Context, projectID snow.ID, id int64) (*domain.MergeRequest, error) {
	return m.setStatus(ctx, projectID, id, domain.MergeRequestOpen, domain.MergeRequestClosed)
}

func (m *MergeRequest) Reopen(ctx context.Context, projectID snow.ID, id int64) (*domain.MergeRequest, error) {
	return m.setStatus(ctx, projectID, id, domain.MergeRequestClosed, domain.MergeRequestOpen)
}

func (m *MergeRequest) Diff(ctx context.Context, projectID snow.ID, id int64) ([]diff.FileDiff, error) {
	mr, err := m.load(ctx, projectID, id)
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

func (m *MergeRequest) load(ctx context.Context, projectID snow.ID, id int64) (*domain.MergeRequest, error) {
	if !m.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	mr, err := m.repo.Get(ctx, projectID, id)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("merge request %d not found", id))
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

func (m *MergeRequest) setStatus(ctx context.Context, projectID snow.ID, id int64, from, to string) (*domain.MergeRequest, error) {
	mr, err := m.load(ctx, projectID, id)
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
	if err := m.repo.UpdateStatus(ctx, projectID, id, to, mergeCommitID); err != nil {
		return nil, err
	}
	return m.repo.Get(ctx, projectID, id)
}
