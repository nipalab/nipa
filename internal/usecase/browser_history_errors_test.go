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

func newHistoryTestBranch(t *testing.T) (*MockpermissionUsecase, *MockbranchRepository, *Branch) {
	t.Helper()
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})
	return perm, repo, uc
}

func branchAt(id snow.ID) *domain.Branch {
	return &domain.Branch{Name: "main", CommitID: &id}
}

func TestHashChanged(t *testing.T) {
	zero := domain.Hash{}
	one := domain.Hash{1}
	require.False(t, hashChanged(zero, false, zero, false))
	require.True(t, hashChanged(one, true, zero, false))
	require.True(t, hashChanged(zero, false, one, true))
	require.False(t, hashChanged(one, true, one, true))
	require.True(t, hashChanged(one, true, domain.Hash{2}, true))
}

func TestChildEntryHash(t *testing.T) {
	dir := &domain.TreeNode{
		FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}},
		TreeChildren: []*domain.TreeNode{{Name: "sub", Hash: domain.Hash{2}}},
	}
	hash, ok := childEntryHash(dir, "a.txt")
	require.True(t, ok)
	require.Equal(t, domain.Hash{1}, hash)
	hash, ok = childEntryHash(dir, "sub")
	require.True(t, ok)
	require.Equal(t, domain.Hash{2}, hash)
	_, ok = childEntryHash(dir, "missing")
	require.False(t, ok)
	_, ok = childEntryHash(nil, "a.txt")
	require.False(t, ok)
}

func TestResolveEntryCommits(t *testing.T) {
	t.Run("no head entries", func(t *testing.T) {
		remaining := 1
		resolveEntryCommits(nil, nil, nil, &domain.CommitLogEntry{}, "", map[string]*domain.CommitLogEntry{}, &remaining)
		require.Equal(t, 1, remaining)
	})

	t.Run("missing newer", func(t *testing.T) {
		remaining := 1
		head := &domain.TreeNode{FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}}
		resolveEntryCommits(nil, nil, head, &domain.CommitLogEntry{}, "", map[string]*domain.CommitLogEntry{}, &remaining)
		require.Equal(t, 1, remaining)
	})

	t.Run("tree child and already resolved", func(t *testing.T) {
		remaining := 2
		byPath := map[string]*domain.CommitLogEntry{"base/a.txt": {Commit: domain.Commit{ID: snow.ID(1)}}}
		head := &domain.TreeNode{
			FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}},
			TreeChildren: []*domain.TreeNode{{Name: "sub", Hash: domain.Hash{5}}},
		}
		newer := &domain.TreeNode{
			FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}},
			TreeChildren: []*domain.TreeNode{{Name: "sub", Hash: domain.Hash{5}}},
		}
		older := &domain.TreeNode{
			FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}},
			TreeChildren: []*domain.TreeNode{{Name: "sub", Hash: domain.Hash{4}}},
		}
		resolveEntryCommits(newer, older, head, &domain.CommitLogEntry{Commit: domain.Commit{ID: snow.ID(7)}}, "base", byPath, &remaining)
		require.Equal(t, snow.ID(1), byPath["base/a.txt"].ID)
		require.Equal(t, snow.ID(7), byPath["base/sub"].ID)
		require.Equal(t, 1, remaining)
	})

	t.Run("non-head entries are ignored", func(t *testing.T) {
		remaining := 1
		byPath := map[string]*domain.CommitLogEntry{}
		head := &domain.TreeNode{FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}}
		newer := &domain.TreeNode{
			FileChildren: []*domain.File{
				{Name: "a.txt", Hash: domain.Hash{1}},
				{Name: "deleted.txt", Hash: domain.Hash{5}},
			},
		}
		older := &domain.TreeNode{
			FileChildren: []*domain.File{
				{Name: "a.txt", Hash: domain.Hash{1}},
				{Name: "deleted.txt", Hash: domain.Hash{4}},
			},
		}
		resolveEntryCommits(newer, older, head, &domain.CommitLogEntry{Commit: domain.Commit{ID: snow.ID(7)}}, "", byPath, &remaining)
		require.Equal(t, 1, remaining)
		require.Empty(t, byPath)
	})
}

func TestBranch_TreeHistory_MissingDirectory(t *testing.T) {
	_, _, uc := newHistoryTestBranch(t)

	history, err := uc.treeHistory(context.Background(), snow.ID(1), snow.ID(10), "missing", &domain.TreeNode{})
	require.NoError(t, err)
	require.Nil(t, history.Latest)
	require.Empty(t, history.ByPath)
}

func TestBranch_TreeHistory_CommitLogError(t *testing.T) {
	wantErr := errors.New("db down")
	_, repo, uc := newHistoryTestBranch(t)
	root := &domain.TreeNode{FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}}
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), snow.ID(10), maxTreeHistoryCommits).Return(nil, wantErr)

	_, err := uc.treeHistory(context.Background(), snow.ID(1), snow.ID(10), "", root)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_TreeHistory_OlderCommitError(t *testing.T) {
	wantErr := errors.New("db down")
	_, repo, uc := newHistoryTestBranch(t)
	headID, olderID := snow.ID(20), snow.ID(10)
	root := &domain.TreeNode{FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}}
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxTreeHistoryCommits).Return([]*domain.CommitLogEntry{
		{Commit: domain.Commit{ID: headID, Parent1ID: &olderID}},
		{Commit: domain.Commit{ID: olderID, Parent1ID: &olderID}},
	}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), olderID).Return(nil, wantErr)

	_, err := uc.treeHistory(context.Background(), snow.ID(1), headID, "", root)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_TreeHistory_WindowTruncated(t *testing.T) {
	_, repo, uc := newHistoryTestBranch(t)
	headID, parentID := snow.ID(20), snow.ID(10)
	root := &domain.TreeNode{FileChildren: []*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}}
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxTreeHistoryCommits).Return([]*domain.CommitLogEntry{
		{Commit: domain.Commit{ID: headID, Parent1ID: &parentID}},
	}, nil)

	history, err := uc.treeHistory(context.Background(), snow.ID(1), headID, "", root)
	require.NoError(t, err)
	require.Nil(t, history.Latest)
	require.Empty(t, history.ByPath)
}

func TestBranch_PathCommitLog_ResolveError(t *testing.T) {
	wantErr := errors.New("db down")
	perm, repo, uc := newHistoryTestBranch(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, wantErr)

	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_PathCommitLog_StartCommit(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	headID, startID := snow.ID(20), snow.ID(15)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), startID, maxPathHistoryScan).Return([]*domain.CommitLogEntry{}, nil)

	commits, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", &startID, 0)
	require.NoError(t, err)
	require.Empty(t, commits)
}

func TestBranch_PathCommitLog_FilterError(t *testing.T) {
	wantErr := errors.New("db down")
	perm, repo, uc := newHistoryTestBranch(t)
	headID := snow.ID(20)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, wantErr)

	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_PathCommitLog_CommitLogError(t *testing.T) {
	wantErr := errors.New("db down")
	perm, repo, uc := newHistoryTestBranch(t)
	headID := snow.ID(20)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxPathHistoryScan).Return(nil, wantErr)

	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "", nil, 0)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_PathCommitLog_HashErrors(t *testing.T) {
	t.Run("head hash error", func(t *testing.T) {
		wantErr := errors.New("db down")
		perm, repo, uc := newHistoryTestBranch(t)
		headID := snow.ID(20)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxPathHistoryScan).Return([]*domain.CommitLogEntry{
			{Commit: domain.Commit{ID: headID, Parent1ID: &headID}},
		}, nil)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(nil, wantErr)

		_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("older hash error", func(t *testing.T) {
		wantErr := errors.New("db down")
		perm, repo, uc := newHistoryTestBranch(t)
		headID, olderID := snow.ID(20), snow.ID(10)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxPathHistoryScan).Return([]*domain.CommitLogEntry{
			{Commit: domain.Commit{ID: headID, Parent1ID: &olderID}},
			{Commit: domain.Commit{ID: olderID, Parent1ID: &olderID}},
		}, nil)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
			Return([]*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}, nil)
		repo.EXPECT().GetCommit(gomock.Any(), olderID).Return(nil, wantErr)

		_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
		require.ErrorIs(t, err, wantErr)
	})
}

func TestBranch_PathCommitLog_WindowTruncated(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	headID, parentID := snow.ID(20), snow.ID(10)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxPathHistoryScan).Return([]*domain.CommitLogEntry{
		{Commit: domain.Commit{ID: headID, Parent1ID: &parentID}},
	}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}, nil)

	commits, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
	require.NoError(t, err)
	require.Empty(t, commits)
}

func TestBranch_PathCommitLog_LimitReached(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	headID, olderID := snow.ID(20), snow.ID(10)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), headID, maxPathHistoryScan).Return([]*domain.CommitLogEntry{
		{Commit: domain.Commit{ID: headID, Parent1ID: &olderID}},
		{Commit: domain.Commit{ID: olderID, Parent1ID: &olderID}},
	}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{Name: "a.txt", Hash: domain.Hash{2}}}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), olderID).Return(&domain.Commit{ID: olderID, TreeID: 100}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).
		Return([]*domain.File{{Name: "a.txt", Hash: domain.Hash{1}}}, nil)

	commits, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 1)
	require.NoError(t, err)
	require.Len(t, commits, 1)
	require.Equal(t, headID, commits[0].ID)
}

func TestBranch_DirManifestAt_Errors(t *testing.T) {
	t.Run("commit error", func(t *testing.T) {
		wantErr := errors.New("db down")
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(nil, wantErr)

		_, err := uc.dirManifestAt(context.Background(), snow.ID(1), snow.ID(10), "")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("filter error", func(t *testing.T) {
		wantErr := errors.New("db down")
		perm, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, wantErr)

		_, err := uc.dirManifestAt(context.Background(), snow.ID(1), snow.ID(10), "")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("manifest error", func(t *testing.T) {
		wantErr := errors.New("db down")
		perm, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, wantErr)

		_, err := uc.dirManifestAt(context.Background(), snow.ID(1), snow.ID(10), "")
		require.ErrorIs(t, err, wantErr)
	})
}

func TestBranch_PathHashAtCommit(t *testing.T) {
	t.Run("deep file path", func(t *testing.T) {
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "src").
			Return(&domain.TreeNode{ID: 201, Name: "src"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(201)).
			Return([]*domain.File{{Name: "main.go", Hash: domain.Hash{9}}}, nil)

		hash, ok, err := uc.pathHashAtCommit(context.Background(), snow.ID(1), snow.ID(10), "src/main.go")
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, domain.Hash{9}, hash)
	})

	t.Run("parent missing", func(t *testing.T) {
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "missing").
			Return(nil, domain.NewErrorRecordNotFound())

		hash, ok, err := uc.pathHashAtCommit(context.Background(), snow.ID(1), snow.ID(10), "missing/a.txt")
		require.NoError(t, err)
		require.False(t, ok)
		require.Equal(t, domain.Hash{}, hash)
	})

	t.Run("file list error", func(t *testing.T) {
		wantErr := errors.New("db down")
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, wantErr)

		_, _, err := uc.pathHashAtCommit(context.Background(), snow.ID(1), snow.ID(10), "a.txt")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("child not found", func(t *testing.T) {
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "sub").
			Return(nil, domain.NewErrorRecordNotFound())

		_, ok, err := uc.pathHashAtCommit(context.Background(), snow.ID(1), snow.ID(10), "sub")
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("child error", func(t *testing.T) {
		wantErr := errors.New("db down")
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "sub").Return(nil, wantErr)

		_, _, err := uc.pathHashAtCommit(context.Background(), snow.ID(1), snow.ID(10), "sub")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("directory filter error", func(t *testing.T) {
		wantErr := errors.New("db down")
		perm, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "sub").
			Return(&domain.TreeNode{ID: 201, Name: "sub"}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, wantErr)

		_, _, err := uc.pathHashAtCommit(context.Background(), snow.ID(1), snow.ID(10), "sub")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("directory manifest error", func(t *testing.T) {
		wantErr := errors.New("db down")
		perm, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "sub").
			Return(&domain.TreeNode{ID: 201, Name: "sub"}, nil)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(201)).Return(nil, wantErr)

		_, _, err := uc.pathHashAtCommit(context.Background(), snow.ID(1), snow.ID(10), "sub")
		require.ErrorIs(t, err, wantErr)
	})
}

func TestBranch_TreeNodeAtCommit_Errors(t *testing.T) {
	t.Run("commit error", func(t *testing.T) {
		wantErr := errors.New("db down")
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(nil, wantErr)

		_, err := uc.treeNodeAtCommit(context.Background(), snow.ID(10), "")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("tree error", func(t *testing.T) {
		wantErr := errors.New("db down")
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(nil, wantErr)

		_, err := uc.treeNodeAtCommit(context.Background(), snow.ID(10), "")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("missing segment", func(t *testing.T) {
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "missing").
			Return(nil, domain.NewErrorRecordNotFound())

		node, err := uc.treeNodeAtCommit(context.Background(), snow.ID(10), "missing")
		require.NoError(t, err)
		require.Nil(t, node)
	})

	t.Run("child error", func(t *testing.T) {
		wantErr := errors.New("db down")
		_, repo, uc := newHistoryTestBranch(t)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101}, nil)
		repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(101), "sub").Return(nil, wantErr)

		_, err := uc.treeNodeAtCommit(context.Background(), snow.ID(10), "sub")
		require.ErrorIs(t, err, wantErr)
	})
}
