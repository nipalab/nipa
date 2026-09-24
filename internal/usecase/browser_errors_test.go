package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestBranch_TreeAt_ManifestError(t *testing.T) {
	wantErr := errors.New("db down")
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(20)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, wantErr)

	_, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "")
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_TreeAtWithHistory_HistoryError(t *testing.T) {
	wantErr := errors.New("db down")
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(20)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxTreeHistoryCommits).Return(nil, wantErr)

	_, _, err := uc.TreeAtWithHistory(context.Background(), snow.ID(1), "main", "")
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_TreeFilesAt_Errors(t *testing.T) {
	t.Run("no permission", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)

		_, err := uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("revision error", func(t *testing.T) {
		wantErr := errors.New("db down")
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, wantErr)

		_, err := uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("empty repo", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(&domain.Branch{Name: "main"}, nil)

		files, err := uc.TreeFilesAt(context.Background(), snow.ID(1), "", "")
		require.NoError(t, err)
		require.Empty(t, files)
	})

	t.Run("filter error", func(t *testing.T) {
		wantErr := errors.New("db down")
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		headID := snow.ID(20)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, wantErr)

		_, err := uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "src")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("manifest error", func(t *testing.T) {
		wantErr := errors.New("db down")
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		headID := snow.ID(20)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, wantErr)

		_, err := uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "")
		require.ErrorIs(t, err, wantErr)
	})
}
