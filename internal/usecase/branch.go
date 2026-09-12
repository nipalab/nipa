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
	CreateBranch(ctx context.Context, branch domain.Branch) (*domain.Branch, error)
	GetCommit(ctx context.Context, commitID snow.ID) (*domain.Commit, error)
	GetCommitByHash(ctx context.Context, hash domain.Hash) (*domain.Commit, error)
	GetTreeNode(ctx context.Context, id int64) (*domain.TreeNode, error)
	GetTreeChildByName(ctx context.Context, parentID int64, name string) (*domain.TreeNode, error)
	ListTreeChildren(ctx context.Context, parentID int64) ([]*domain.TreeNode, error)
	ListFilesByTree(ctx context.Context, treeID int64) ([]*domain.File, error)
}

type BranchForkPoint struct {
	BranchName string
	CommitID   *snow.ID
	CommitHash *domain.Hash
}

type Branch struct {
	permUc     permissionUsecase
	branchRepo branchRepository
	snowNode   snow.Node
}

func NewBranch(permUc permissionUsecase, branchRepo branchRepository, snowNode snow.Node) *Branch {
	return &Branch{
		permUc:     permUc,
		branchRepo: branchRepo,
		snowNode:   snowNode,
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

func (b *Branch) CreateBranch(ctx context.Context, projectID snow.ID, name string, fork BranchForkPoint) (*domain.Branch, error) {
	if !b.permUc.HasProjectAccess(ctx, projectID, domain.PermissionWrite) {
		return nil, domain.NewErrorNoPermission()
	}
	name = strings.TrimSpace(name)
	if err := validateBranchName(name); err != nil {
		return nil, err
	}

	if _, err := b.branchRepo.GetBranchByName(ctx, projectID, name); err == nil {
		return nil, domain.NewErrorConflict(fmt.Sprintf("branch %q already exists", name))
	} else if !domain.IsErrorNotFound(err) {
		return nil, err
	}

	fromCommitID, err := b.resolveForkPoint(ctx, projectID, fork)
	if err != nil {
		return nil, err
	}

	return b.branchRepo.CreateBranch(ctx, domain.Branch{
		ID:        b.snowNode.Generate(),
		ProjectID: projectID,
		Name:      name,
		CommitID:  fromCommitID,
	})
}

func (b *Branch) resolveForkPoint(ctx context.Context, projectID snow.ID, fork BranchForkPoint) (*snow.ID, error) {
	if fork.CommitID != nil {
		commit, err := b.branchRepo.GetCommit(ctx, *fork.CommitID)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitID.Base36()))
			}
			return nil, err
		}
		if commit.ProjectID != projectID {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitID.Base36()))
		}
		id := commit.ID
		return &id, nil
	}

	if fork.CommitHash != nil {
		commit, err := b.branchRepo.GetCommitByHash(ctx, *fork.CommitHash)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitHash.String()))
			}
			return nil, err
		}
		if commit.ProjectID != projectID {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("commit %s not found", fork.CommitHash.String()))
		}
		id := commit.ID
		return &id, nil
	}

	if fork.BranchName != "" {
		from, err := b.branchRepo.GetBranchByName(ctx, projectID, fork.BranchName)
		if domain.IsErrorNotFound(err) {
			return nil, domain.NewErrorNotFound(fmt.Sprintf("branch %q not found", fork.BranchName))
		}
		if err != nil {
			return nil, err
		}
		return from.CommitID, nil
	}

	def, err := b.branchRepo.GetDefaultBranch(ctx, projectID)
	if domain.IsErrorNotFound(err) {
		return nil, domain.NewErrorNotFound("no default branch found")
	}
	if err != nil {
		return nil, err
	}
	return def.CommitID, nil
}

func validateBranchName(name string) error {
	if name == "" {
		return domain.NewErrorUser("branch name must not be empty")
	}
	if strings.ContainsRune(name, '/') {
		return domain.NewErrorUser(fmt.Sprintf("invalid branch name %q", name))
	}
	if name == "." || name == ".." {
		return domain.NewErrorUser(fmt.Sprintf("invalid branch name %q", name))
	}
	for _, r := range name {
		if r <= 0x20 || r == 0x7f {
			return domain.NewErrorUser(fmt.Sprintf("invalid branch name %q", name))
		}
	}
	return nil
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
