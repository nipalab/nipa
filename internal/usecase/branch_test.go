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

func newTestBranchNode(t *testing.T) snow.Node {
	t.Helper()
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return node
}

func TestNewBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	require.NotNil(t, uc)
}

func TestBranch_ListBranches_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
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

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetTreeManifest(context.Background(), snow.ID(1), "main", "", "", true)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_CreateBranch_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{BranchName: "main"})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_CreateBranch_EmptyName(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "   ", BranchForkPoint{BranchName: "main"})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "branch name must not be empty", domErr.Message)
}

func TestBranch_CreateBranch_InvalidName(t *testing.T) {
	for _, name := range []string{"feat/ure", "..", ".", "feat ure"} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			perm := NewMockpermissionUsecase(ctrl)
			repo := NewMockbranchRepository(ctrl)

			perm.EXPECT().
				HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
				Return(true)

			uc := NewBranch(perm, repo, newTestBranchNode(t))
			_, err := uc.CreateBranch(context.Background(), snow.ID(1), name, BranchForkPoint{BranchName: "main"})

			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
			require.Equal(t, 400, domErr.Code)
		})
	}
}

func TestBranch_CreateBranch_AlreadyExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "feature"}, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{BranchName: "main"})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, `branch "feature" already exists`, domErr.Message)
}

func TestBranch_CreateBranch_FromBranchByCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(99)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &commitID}, nil),
	)

	var captured domain.Branch
	repo.EXPECT().
		CreateBranch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b domain.Branch) (*domain.Branch, error) {
			captured = b
			return &b, nil
		})

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{BranchName: "main"})
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
	require.Equal(t, snow.ID(1), created.ProjectID)
	require.NotZero(t, captured.ID)
	require.Equal(t, "feature", captured.Name)
	require.Equal(t, snow.ID(1), captured.ProjectID)
	require.NotNil(t, captured.CommitID)
	require.Equal(t, commitID, *captured.CommitID)
	require.False(t, captured.IsDefault)
	require.False(t, captured.IsProtected)
}

func TestBranch_CreateBranch_FromBranchNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{BranchName: "main"})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `branch "main" not found`, domErr.Message)
}

func TestBranch_CreateBranch_FromDefaultBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(7)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetDefaultBranch(gomock.Any(), snow.ID(1)).
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &commitID}, nil),
	)

	var captured domain.Branch
	repo.EXPECT().
		CreateBranch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b domain.Branch) (*domain.Branch, error) {
			captured = b
			return &b, nil
		})

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{})
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
	require.NotNil(t, captured.CommitID)
	require.Equal(t, commitID, *captured.CommitID)
}

func TestBranch_CreateBranch_NoDefaultBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetDefaultBranch(gomock.Any(), snow.ID(1)).
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, "no default branch found", domErr.Message)
}

func TestBranch_CreateBranch_UniquenessCheckError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("db down")
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(nil, wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{BranchName: "main"})
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_CreateBranch_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("tx failed")
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil),
		repo.EXPECT().
			CreateBranch(gomock.Any(), gomock.Any()).
			Return(nil, wantErr),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{BranchName: "main"})
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_CreateBranch_FromCommitID(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)
	commit := &domain.Commit{ID: snow.ID(99), ProjectID: projectID}
	forkID := commit.ID

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), forkID).
			Return(commit, nil),
	)

	var captured domain.Branch
	repo.EXPECT().
		CreateBranch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b domain.Branch) (*domain.Branch, error) {
			captured = b
			return &b, nil
		})

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateBranch(context.Background(), projectID, "feature", BranchForkPoint{CommitID: &forkID, BranchName: "main"})
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
	require.NotNil(t, captured.CommitID)
	require.Equal(t, commit.ID, *captured.CommitID, "an explicit commit ID must win over the branch name")
}

func TestBranch_CreateBranch_FromCommitID_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	missing := snow.ID(404)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), missing).
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{CommitID: &missing})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_CreateBranch_FromCommitID_OtherProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	forkID := snow.ID(99)
	otherProjectCommit := &domain.Commit{ID: forkID, ProjectID: snow.ID(2)}
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), forkID).
			Return(otherProjectCommit, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{CommitID: &forkID})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_CreateBranch_FromCommitHash(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)
	commit := &domain.Commit{ID: snow.ID(99), ProjectID: projectID}
	hash := commitHashFromBytes([]byte{0xbe, 0xef})
	forkHash := hash

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommitByHash(gomock.Any(), forkHash).
			Return(commit, nil),
	)

	var captured domain.Branch
	repo.EXPECT().
		CreateBranch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b domain.Branch) (*domain.Branch, error) {
			captured = b
			return &b, nil
		})

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateBranch(context.Background(), projectID, "feature", BranchForkPoint{CommitHash: &forkHash, BranchName: "main"})
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
	require.NotNil(t, captured.CommitID)
	require.Equal(t, commit.ID, *captured.CommitID, "a commit hash must win over the branch name")
}

func TestBranch_CreateBranch_FromCommitHash_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	missing := commitHashFromBytes([]byte{0xde, 0xad, 0xbe, 0xef})
	forkHash := missing
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommitByHash(gomock.Any(), forkHash).
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{CommitHash: &forkHash})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_CreateBranch_FromCommitHash_OtherProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	hash := commitHashFromBytes([]byte{0xf0, 0x0d})
	forkHash := hash
	otherProjectCommit := &domain.Commit{ID: snow.ID(99), ProjectID: snow.ID(2)}
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommitByHash(gomock.Any(), forkHash).
			Return(otherProjectCommit, nil),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{CommitHash: &forkHash})

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_CreateBranch_CommitLookupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	wantErr := errors.New("db down")
	forkID := snow.ID(99)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), forkID).
			Return(nil, wantErr),
	)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateBranch(context.Background(), snow.ID(1), "feature", BranchForkPoint{CommitID: &forkID})
	require.ErrorIs(t, err, wantErr)
}

func commitHashFromBytes(b []byte) domain.Hash {
	var h domain.Hash
	copy(h[:], b)
	return h
}

func TestBranch_GetCommitLog_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitLog(context.Background(), snow.ID(1), "main", nil, 10)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_GetCommitLog_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)
	commitID := snow.ID(99)
	startID := snow.ID(88)
	want := []*domain.CommitLogEntry{
		{Commit: domain.Commit{ID: 99, Message: "latest"}, AuthorName: "Alice"},
		{Commit: domain.Commit{ID: 88, Message: "older"}, AuthorName: "Bob"},
	}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		CommitLog(gomock.Any(), projectID, startID, 10).
		Return(want, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	got, err := uc.GetCommitLog(context.Background(), projectID, "main", &startID, 10)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestBranch_GetCommitLog_StartsAtBranchHead(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)
	commitID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		CommitLog(gomock.Any(), projectID, commitID, 50).
		Return([]*domain.CommitLogEntry{}, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	got, err := uc.GetCommitLog(context.Background(), projectID, "main", nil, 0)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestBranch_GetCommitLog_DefaultBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)
	commitID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetDefaultBranch(gomock.Any(), projectID).
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		CommitLog(gomock.Any(), projectID, commitID, 10).
		Return([]*domain.CommitLogEntry{}, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitLog(context.Background(), projectID, "", nil, 10)
	require.NoError(t, err)
}

func TestBranch_GetCommitLog_EmptyBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "empty").
		Return(&domain.Branch{ID: 3, ProjectID: projectID, Name: "empty", CommitID: nil}, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	got, err := uc.GetCommitLog(context.Background(), projectID, "empty", nil, 10)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestBranch_GetCommitLog_BranchNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "nope").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitLog(context.Background(), projectID, "nope", nil, 10)
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `branch "nope" not found`, domErr.Message)
}

func TestBranch_GetCommitLog_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	projectID := snow.ID(1)
	commitID := snow.ID(99)
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		CommitLog(gomock.Any(), projectID, commitID, 10).
		Return(nil, wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitLog(context.Background(), projectID, "main", nil, 10)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetCommitTree_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestBranch_GetCommitTree_MissingRef(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func commitTreeTestCommit() *domain.Commit {
	return &domain.Commit{
		ID:        snow.ID(99),
		Hash:      domain.Hash{0x01},
		ProjectID: snow.ID(1),
		TreeID:    7,
	}
}

func TestBranch_GetCommitTree_ByID(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commit := commitTreeTestCommit()
	root := &domain.TreeNode{ID: 7, Hash: domain.Hash{0x02}, Name: "root"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commit.ID).
		Return(commit, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(7)).
		Return(root, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), int64(7)).
		Return([]*domain.File{{ID: 1, Name: "a.txt"}}, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), int64(7)).
		Return(nil, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	got, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitID: &commit.ID})
	require.NoError(t, err)
	require.Equal(t, commit.ID, got.CommitID)
	require.Len(t, got.Tree.FileChildren, 1)
	require.Equal(t, "a.txt", got.Tree.FileChildren[0].Name)
}

func TestBranch_GetCommitTree_ByIDNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(99)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commitID).
		Return(nil, domain.NewErrorNotFound("nope"))

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitID: &commitID})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_GetCommitTree_ByIDWrongProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commit := commitTreeTestCommit()
	commit.ProjectID = snow.ID(2)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commit.ID).
		Return(commit, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitID: &commit.ID})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_GetCommitTree_ByIDRepoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commitID := snow.ID(99)
	wantErr := errors.New("db down")
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commitID).
		Return(nil, wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitID: &commitID})
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetCommitTree_ByHash(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commit := commitTreeTestCommit()
	root := &domain.TreeNode{ID: 7, Hash: domain.Hash{0x02}, Name: "root"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommitByHash(gomock.Any(), commit.Hash).
		Return(commit, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(7)).
		Return(root, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), int64(7)).
		Return(nil, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), int64(7)).
		Return(nil, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	got, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitHash: &commit.Hash})
	require.NoError(t, err)
	require.Equal(t, commit.ID, got.CommitID)
}

func TestBranch_GetCommitTree_ByHashNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	hash := domain.Hash{0x09}
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommitByHash(gomock.Any(), hash).
		Return(nil, domain.NewErrorNotFound("nope"))

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitHash: &hash})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_GetCommitTree_ByHashWrongProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commit := commitTreeTestCommit()
	commit.ProjectID = snow.ID(2)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommitByHash(gomock.Any(), commit.Hash).
		Return(commit, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitHash: &commit.Hash})
	require.Error(t, err)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestBranch_GetCommitTree_TreeNodeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commit := commitTreeTestCommit()
	wantErr := errors.New("db down")
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commit.ID).
		Return(commit, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(7)).
		Return(nil, wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitID: &commit.ID})
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_GetCommitTree_ListFilesError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)

	commit := commitTreeTestCommit()
	root := &domain.TreeNode{ID: 7, Hash: domain.Hash{0x02}, Name: "root"}
	wantErr := errors.New("db down")
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commit.ID).
		Return(commit, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(7)).
		Return(root, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), int64(7)).
		Return(nil, wantErr)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.GetCommitTree(context.Background(), snow.ID(1), CommitRef{CommitID: &commit.ID})
	require.ErrorIs(t, err, wantErr)
}
