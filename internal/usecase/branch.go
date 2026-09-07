package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type permissionUsecase interface {
	HasProjectAccess(ctx context.Context, projectID snow.ID, permission domain.Permission) bool
}

type branchRepository interface {
	ListBranches(ctx context.Context, projectID snow.ID, limit int, updatedAfter *time.Time, lastID snow.ID) ([]*domain.Branch, error)
	GetByProjectIDAndID(ctx context.Context, projectID snow.ID, branchID snow.ID) (*domain.Branch, error)
	GetDefaultBranch(ctx context.Context, projectID snow.ID) (*domain.Branch, error)
	GetBranchByName(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error)
	GetCommit(ctx context.Context, commitID snow.ID) (*domain.Commit, error)
	GetTreeNode(ctx context.Context, id int64) (*domain.TreeNode, error)
	GetTreeChildByName(ctx context.Context, parentID int64, name string) (*domain.TreeNode, error)
	ListTreeChildren(ctx context.Context, parentID int64) ([]*domain.TreeNode, error)
	ListFilesByTree(ctx context.Context, treeID int64) ([]*domain.File, error)
}

type Branch struct {
	permUc     permissionUsecase
	branchRepo branchRepository
}

func NewBranch(permUc permissionUsecase, branchRepo branchRepository) *Branch {
	return &Branch{
		permUc:     permUc,
		branchRepo: branchRepo,
	}
}

func (b *Branch) ListBranches(ctx context.Context, projectID snow.ID, limit int, updatedAfter *time.Time, lastID snow.ID) ([]*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	return b.branchRepo.ListBranches(ctx, projectID, limit, updatedAfter, lastID)
}

func (b *Branch) GetByProjectIDAndID(ctx context.Context, projectID snow.ID, branchID snow.ID) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	return b.branchRepo.GetByProjectIDAndID(ctx, projectID, branchID)
}

func (b *Branch) GetBranchByName(ctx context.Context, projectID snow.ID, name string) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, name)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", name))
	}
	return branch, err
}

func (b *Branch) GetDefault(ctx context.Context, projectID snow.ID) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	return b.branchRepo.GetDefaultBranch(ctx, projectID)
}

func (b *Branch) GetTreeManifest(ctx context.Context, projectID snow.ID, branchName, path, treeHash string, recursive bool) (*domain.TreeNode, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	branch, err := b.branchRepo.GetBranchByName(ctx, projectID, branchName)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", branchName))
	}
	if err != nil {
		return nil, err
	}
	if branch.CommitID == nil {
		if path != "" && path != "/" {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found in branch %q", path, branchName))
		}
		return nil, nil
	}
	commit, err := b.branchRepo.GetCommit(ctx, *branch.CommitID)
	if err != nil {
		return nil, err
	}
	root, err := b.branchRepo.GetTreeNode(ctx, commit.TreeID)
	if err != nil {
		return nil, err
	}
	if path != "" && path != "/" {
		for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
			if segment == "" {
				continue
			}
			root, err = b.branchRepo.GetTreeChildByName(ctx, root.ID, segment)
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("path %q not found in branch %q", path, branchName))
			}
			if err != nil {
				return nil, err
			}
		}
	}
	if treeHash != "" && strings.EqualFold(treeHash, root.Hash.String()) {
		return nil, nil
	}
	if err := b.loadTreeManifest(ctx, root, recursive); err != nil {
		return nil, err
	}
	return root, nil
}

func (b *Branch) loadTreeManifest(ctx context.Context, node *domain.TreeNode, recursive bool) error {
	files, err := b.branchRepo.ListFilesByTree(ctx, node.ID)
	if err != nil {
		return err
	}
	node.FileChildren = files
	node.TreeChildren = nil
	if !recursive {
		return nil
	}
	children, err := b.branchRepo.ListTreeChildren(ctx, node.ID)
	if err != nil {
		return err
	}
	node.TreeChildren = children
	for _, child := range children {
		if err := b.loadTreeManifest(ctx, child, true); err != nil {
			return err
		}
	}
	return nil
}
