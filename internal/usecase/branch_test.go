package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestNewBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	uc := NewBranch(perm, repo)
	require.NotNil(t, uc)
}

func TestBranch_ListBranches_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo)
	_, err := uc.ListBranches(context.Background(), snow.ID(1), 10, nil, 0)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_ListBranches_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	want := []*domain.Branch{
		{ID: 1, ProjectID: 1, Name: "main"},
		{ID: 2, ProjectID: 1, Name: "develop"},
	}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	after := time.Now().Add(-time.Hour)
	lastID := snow.ID(0)
	repo.EXPECT().
		ListBranches(gomock.Any(), snow.ID(1), 10, &after, lastID).
		Return(want, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.ListBranches(context.Background(), snow.ID(1), 10, &after, lastID)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestBranch_ListBranches_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListBranches(gomock.Any(), snow.ID(1), 10, gomock.Nil(), snow.ID(0)).
		Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.ListBranches(context.Background(), snow.ID(1), 10, nil, 0)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_ListBranches_Empty(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListBranches(gomock.Any(), snow.ID(1), 10, gomock.Nil(), snow.ID(0)).
		Return([]*domain.Branch{}, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.ListBranches(context.Background(), snow.ID(1), 10, nil, 0)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestBranch_GetByProjectIDAndID_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo)
	_, err := uc.GetByProjectIDAndID(context.Background(), snow.ID(1), snow.ID(2))
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_GetByProjectIDAndID_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	want := &domain.Branch{ID: 2, ProjectID: 1, Name: "main"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).
		Return(want, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetByProjectIDAndID(context.Background(), snow.ID(1), snow.ID(2))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestBranch_GetByProjectIDAndID_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).
		Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetByProjectIDAndID(context.Background(), snow.ID(1), snow.ID(2))
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetByProjectIDAndID_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo)
	_, err := uc.GetByProjectIDAndID(context.Background(), snow.ID(1), snow.ID(2))
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_GetDefault_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo)
	_, err := uc.GetDefault(context.Background(), snow.ID(1))
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_GetDefault_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	want := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetDefaultBranch(gomock.Any(), snow.ID(1)).
		Return(want, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetDefault(context.Background(), snow.ID(1))
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestBranch_GetDefault_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetDefaultBranch(gomock.Any(), snow.ID(1)).
		Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetDefault(context.Background(), snow.ID(1))
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_GetTreeManifest_BranchNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `branch "main" not found`, domErr.Message)
}

func TestBranch_GetTreeManifest_NoCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main"}, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestBranch_GetTreeManifest_NoCommit_PathNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main"}, nil)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "sedotan", "", false)
	require.Error(t, err)
	require.True(t, domain.IsErrorNotFound(err))
	require.Equal(t, `path "sedotan" not found in branch "main"`, err.Error())
}

func TestBranch_GetTreeManifest_Recursive(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}
	rootFiles := []*domain.File{{ID: 1, Name: "a.txt"}}
	child := &domain.TreeNode{ID: 200, Name: "assets"}
	childFiles := []*domain.File{{ID: 2, Name: "b.png"}}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(rootFiles, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return([]*domain.TreeNode{child}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(200)).Return(childFiles, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(200)).Return(nil, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.NoError(t, err)
	require.Equal(t, root, got)
	require.Equal(t, rootFiles, got.FileChildren)
	require.Len(t, got.TreeChildren, 1)
	require.Equal(t, child, got.TreeChildren[0])
	require.Equal(t, childFiles, got.TreeChildren[0].FileChildren)
}

func TestBranch_GetTreeManifest_NotRecursive(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}
	rootFiles := []*domain.File{{ID: 1, Name: "a.txt"}}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(rootFiles, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", false)
	require.NoError(t, err)
	require.Equal(t, rootFiles, got.FileChildren)
	require.Empty(t, got.TreeChildren)
}

func TestBranch_GetTreeManifest_WithPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}
	assets := &domain.TreeNode{ID: 200, Name: "assets"}
	shaders := &domain.TreeNode{ID: 300, Name: "shaders"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(100), "assets").Return(assets, nil)
	repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(200), "shaders").Return(shaders, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(300)).Return(nil, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "assets/shaders", "", false)
	require.NoError(t, err)
	require.Equal(t, shaders, got)
}

func TestBranch_GetTreeManifest_PathNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(100), "missing").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "missing", "", false)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `path "missing" not found in branch "main"`, domErr.Message)
}

func TestBranch_GetTreeManifest_TreeHashMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	var hash domain.Hash
	hash[0] = 1
	root := &domain.TreeNode{ID: 100, Name: "root", Hash: hash}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", hash.String(), true)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestBranch_GetBranchByName_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo)
	_, err := uc.GetBranchByName(context.Background(), snow.ID(1), "main")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_GetBranchByName_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	want := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(want, nil)

	uc := NewBranch(perm, repo)
	got, err := uc.GetBranchByName(context.Background(), snow.ID(1), "main")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestBranch_GetBranchByName_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo)
	_, err := uc.GetBranchByName(context.Background(), snow.ID(1), "main")
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `branch "main" not found`, domErr.Message)
}

func TestBranch_GetBranchByName_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetBranchByName(context.Background(), snow.ID(1), "main")
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_BranchRepoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_GetCommitError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_GetTreeNodeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_PathRepoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(100), "assets").Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "assets", "", true)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_ListFilesByTreeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_ListTreeChildrenError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetTreeManifest_ChildLoadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(9)
	branch := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", CommitID: &commitID}
	commit := &domain.Commit{ID: commitID, TreeID: 100}
	root := &domain.TreeNode{ID: 100, Name: "root"}
	child := &domain.TreeNode{ID: 200, Name: "assets"}
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branch, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(commit, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(root, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return([]*domain.TreeNode{child}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(200)).Return(nil, wantErr)

	uc := NewBranch(perm, repo)
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.ErrorIs(t, err, wantErr)
}
