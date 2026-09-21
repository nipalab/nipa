package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubChunkReader struct {
	data map[domain.Hash][]byte
	err  error
}

func (s *stubChunkReader) Get(_ context.Context, hash domain.Hash) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	data, ok := s.data[hash]
	if !ok {
		return nil, errors.New("chunk not found")
	}
	return data, nil
}

func TestBranch_TreeAt_EmptyRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(&domain.Branch{Name: "main"}, nil).AnyTimes()

	node, err := uc.TreeAt(context.Background(), snow.ID(1), "", "")
	require.NoError(t, err)
	require.NotNil(t, node)
	require.Empty(t, node.FileChildren)

	_, err = uc.TreeAt(context.Background(), snow.ID(1), "", "dir")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_TreeAt_BranchRevision(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	commitID := snow.ID(11)
	chunkHash := domain.Hash{1}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: chunkHash, SizeBytes: 5}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	node, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "")
	require.NoError(t, err)
	require.Len(t, node.FileChildren, 1)
	require.Equal(t, "a.txt", node.FileChildren[0].Name)
}

func TestBranch_TreeAt_HiddenPathIsNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	commitID := snow.ID(11)
	restricted := &PathFilter{
		set:        &permissionSet{rules: []*domain.PBACRule{{PathPrefix: "public", Permission: domain.PermissionRead}}},
		permission: domain.PermissionRead,
	}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(restricted, nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	_, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "secret")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_TreeAt_CommitRevisionNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "abc").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().GetCommit(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound())

	_, err := uc.TreeAt(context.Background(), snow.ID(1), "abc", "")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_TreeAt_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)

	_, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_FileContent(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	chunks := &stubChunkReader{data: map[domain.Hash][]byte{{1}: []byte("hello")}}
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), chunks)

	commitID := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil).AnyTimes()
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
	}, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil).AnyTimes()

	content, entry, err := uc.FileContent(context.Background(), snow.ID(1), "main", "a.txt")
	require.NoError(t, err)
	require.Equal(t, "hello", string(content))
	require.Equal(t, "a.txt", entry.Path)

	_, _, err = uc.FileContent(context.Background(), snow.ID(1), "main", "missing.txt")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_FileContent_ChunkError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	chunks := &stubChunkReader{err: errors.New("chunk store down")}
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), chunks)

	commitID := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	_, _, err := uc.FileContent(context.Background(), snow.ID(1), "main", "a.txt")
	require.Error(t, err)
}

func TestBranch_FileContent_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)

	_, _, err := uc.FileContent(context.Background(), snow.ID(1), "main", "a.txt")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_CommitDiff_RootCommit(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	commitID := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil).Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	files, base, err := uc.CommitDiff(context.Background(), snow.ID(1), commitID, nil)
	require.NoError(t, err)
	require.Nil(t, base)
	require.Len(t, files, 1)
	require.Equal(t, diff.Added, files[0].Change.Status)
	require.Equal(t, "a.txt", files[0].Change.Path)
}

func TestBranch_CommitDiff_ExplicitBase(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(11)
	baseID := snow.ID(10)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), headID).
		Return(&domain.Commit{ID: headID, ProjectID: 1, TreeID: 101}, nil).Times(2)
	repo.EXPECT().GetCommit(gomock.Any(), baseID).
		Return(&domain.Commit{ID: baseID, ProjectID: 1, TreeID: 100}, nil).Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return([]*domain.File{
		{ID: 2, Name: "a.txt", Hash: domain.Hash{8}, SizeBytes: 3, Chunks: []domain.Chunk{{Hash: domain.Hash{2}, SizeBytes: 3}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil)

	files, base, err := uc.CommitDiff(context.Background(), snow.ID(1), headID, &baseID)
	require.NoError(t, err)
	require.NotNil(t, base)
	require.Equal(t, baseID, *base)
	require.Len(t, files, 1)
	require.Equal(t, diff.Modified, files[0].Change.Status)
}

func TestBranch_CommitDiff_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)

	_, _, err := uc.CommitDiff(context.Background(), snow.ID(1), snow.ID(11), nil)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_TreeAt_NestedSubtree(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	commitID := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return([]*domain.TreeNode{{ID: 201, Name: "public"}}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(201)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(201)).Return(nil, nil)

	node, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "public")
	require.NoError(t, err)
	require.Equal(t, "public", node.Name)
	require.Len(t, node.FileChildren, 1)
	require.Equal(t, "a.txt", node.FileChildren[0].Name)
}

func TestBranch_TreeAt_PathNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	commitID := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	_, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "nope")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_TreeAt_FilterError(t *testing.T) {
	wantErr := errors.New("db down")
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	commitID := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	gomock.InOrder(
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil),
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, wantErr),
	)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	_, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "dir")
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_TreeAt_RevisionErrors(t *testing.T) {
	t.Run("default branch error", func(t *testing.T) {
		wantErr := errors.New("db down")
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(nil, wantErr)

		_, err := uc.TreeAt(context.Background(), snow.ID(1), "", "")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("branch lookup error", func(t *testing.T) {
		wantErr := errors.New("db down")
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, wantErr)

		_, err := uc.TreeAt(context.Background(), snow.ID(1), "main", "")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("invalid revision", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "!!!").Return(nil, domain.NewErrorRecordNotFound())

		_, err := uc.TreeAt(context.Background(), snow.ID(1), "!!!", "")
		require.True(t, domain.IsErrorNotFound(err))
	})
}

func TestBranch_FileContent_EmptyRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(&domain.Branch{Name: "main"}, nil)

	_, _, err := uc.FileContent(context.Background(), snow.ID(1), "", "a.txt")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_FileContent_NoChunkStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	commitID := snow.ID(11)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, ProjectID: 1, TreeID: 101}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

	_, _, err := uc.FileContent(context.Background(), snow.ID(1), "main", "a.txt")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 500, domErr.Code)
}

func TestBranch_CommitDiff_DefaultParent(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(11)
	baseID := snow.ID(10)
	headCommit := &domain.Commit{ID: headID, ProjectID: 1, TreeID: 101, Parent1ID: &baseID}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), headID).Return(headCommit, nil).Times(2)
	repo.EXPECT().GetCommit(gomock.Any(), baseID).
		Return(&domain.Commit{ID: baseID, ProjectID: 1, TreeID: 100}, nil).Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
		{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
	}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil)

	files, base, err := uc.CommitDiff(context.Background(), snow.ID(1), headID, nil)
	require.NoError(t, err)
	require.NotNil(t, base)
	require.Equal(t, baseID, *base)
	require.Len(t, files, 1)
	require.Equal(t, diff.Added, files[0].Change.Status)
}

func TestBranch_CommitDiff_BaseNotInProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(11)
	baseID := snow.ID(10)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetCommit(gomock.Any(), headID).
		Return(&domain.Commit{ID: headID, ProjectID: 1, TreeID: 101}, nil).Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().GetCommit(gomock.Any(), baseID).Return(nil, domain.NewErrorRecordNotFound())

	_, _, err := uc.CommitDiff(context.Background(), snow.ID(1), headID, &baseID)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_TreeDiffBetween(t *testing.T) {
	t.Run("with merge base", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		baseID := snow.ID(10)
		headID := snow.ID(11)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
		repo.EXPECT().GetCommit(gomock.Any(), baseID).Return(&domain.Commit{ID: baseID, ProjectID: 1, TreeID: 100}, nil).Times(2)
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, ProjectID: 1, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100, Name: "root"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
			{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
		}, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

		files, err := uc.TreeDiffBetween(context.Background(), snow.ID(1), &baseID, headID)
		require.NoError(t, err)
		require.Len(t, files, 1)
		require.Equal(t, diff.Added, files[0].Change.Status)
		require.Equal(t, "a.txt", files[0].Change.Path)
	})

	t.Run("root commit has no base", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		headID := snow.ID(11)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
		repo.EXPECT().GetCommit(gomock.Any(), headID).Return(&domain.Commit{ID: headID, ProjectID: 1, TreeID: 101}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{
			{ID: 1, Name: "a.txt", Hash: domain.Hash{9}, SizeBytes: 5, Chunks: []domain.Chunk{{Hash: domain.Hash{1}, SizeBytes: 5}}},
		}, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)

		files, err := uc.TreeDiffBetween(context.Background(), snow.ID(1), nil, headID)
		require.NoError(t, err)
		require.Len(t, files, 1)
	})

	t.Run("no read access", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := uc.TreeDiffBetween(context.Background(), snow.ID(1), nil, snow.ID(11))
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("base commit missing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

		baseID := snow.ID(10)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().GetCommit(gomock.Any(), baseID).Return(nil, domain.NewErrorRecordNotFound())

		_, err := uc.TreeDiffBetween(context.Background(), snow.ID(1), &baseID, snow.ID(11))
		require.True(t, domain.IsErrorNotFound(err))
	})
}
