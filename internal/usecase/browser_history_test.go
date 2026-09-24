package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type testTreeSpec struct {
	id       int64
	name     string
	files    map[string]domain.Hash
	children []*testTreeSpec
}

func expectTreeManifest(repo *MockbranchRepository, spec *testTreeSpec) {
	repo.EXPECT().GetTreeNode(gomock.Any(), spec.id).
		DoAndReturn(func(context.Context, int64) (*domain.TreeNode, error) {
			return &domain.TreeNode{ID: spec.id, Name: spec.name}, nil
		}).AnyTimes()
	files := make([]*domain.File, 0, len(spec.files))
	for name, hash := range spec.files {
		files = append(files, &domain.File{ID: spec.id, Name: name, Hash: hash})
	}
	repo.EXPECT().ListFilesByTree(gomock.Any(), spec.id).Return(files, nil).AnyTimes()
	children := make([]*domain.TreeNode, 0, len(spec.children))
	for _, child := range spec.children {
		children = append(children, &domain.TreeNode{ID: child.id, Name: child.name})
	}
	repo.EXPECT().ListTreeChildren(gomock.Any(), spec.id).Return(children, nil).AnyTimes()
	for _, child := range spec.children {
		expectTreeManifest(repo, child)
	}
}

func expectCommitManifest(repo *MockbranchRepository, id snow.ID, parent *snow.ID, root *testTreeSpec) {
	repo.EXPECT().GetCommit(gomock.Any(), id).
		Return(&domain.Commit{ID: id, ProjectID: 1, TreeID: root.id, Parent1ID: parent}, nil).AnyTimes()
	expectTreeManifest(repo, root)
}

func flatHistory(t *testing.T) (headID, midID, rootID snow.ID, head, mid, root *testTreeSpec) {
	t.Helper()
	headID, midID, rootID = snow.ID(30), snow.ID(20), snow.ID(10)
	head = &testTreeSpec{id: 103, files: map[string]domain.Hash{"a.txt": {2}, "b.txt": {2}}}
	mid = &testTreeSpec{id: 102, files: map[string]domain.Hash{"a.txt": {2}, "b.txt": {1}}}
	root = &testTreeSpec{id: 101, files: map[string]domain.Hash{"a.txt": {1}, "b.txt": {1}}}
	return headID, midID, rootID, head, mid, root
}

func expectFlatLog(repo *MockbranchRepository, headID, midID, rootID snow.ID) {
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any()).Return([]*domain.CommitLogEntry{
		{Commit: domain.Commit{ID: headID, TreeID: 103, Parent1ID: &midID}, AuthorName: "alice"},
		{Commit: domain.Commit{ID: midID, TreeID: 102, Parent1ID: &rootID}, AuthorName: "bob"},
		{Commit: domain.Commit{ID: rootID, TreeID: 101}, AuthorName: "alice"},
	}, nil).AnyTimes()
}

func TestBranch_TreeAtWithHistory(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID, midID, rootID, head, mid, root := flatHistory(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &headID}, nil)
	expectCommitManifest(repo, headID, &midID, head)
	expectCommitManifest(repo, midID, &rootID, mid)
	expectCommitManifest(repo, rootID, nil, root)
	expectFlatLog(repo, headID, midID, rootID)

	node, history, err := uc.TreeAtWithHistory(context.Background(), snow.ID(1), "main", "")
	require.NoError(t, err)
	require.Len(t, node.FileChildren, 2)
	require.NotNil(t, history.Latest)
	require.Equal(t, headID, history.Latest.ID)
	require.Equal(t, midID, history.ByPath["a.txt"].ID)
	require.Equal(t, headID, history.ByPath["b.txt"].ID)
}

func TestBranch_TreeAtWithHistory_RootCommitOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(10)
	head := &testTreeSpec{id: 101, files: map[string]domain.Hash{"a.txt": {1}}}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &headID}, nil)
	expectCommitManifest(repo, headID, nil, head)
	repo.EXPECT().CommitLog(gomock.Any(), snow.ID(1), gomock.Any(), gomock.Any()).
		Return([]*domain.CommitLogEntry{{Commit: domain.Commit{ID: headID, TreeID: 101}}}, nil)

	_, history, err := uc.TreeAtWithHistory(context.Background(), snow.ID(1), "main", "")
	require.NoError(t, err)
	require.Equal(t, headID, history.Latest.ID)
	require.Equal(t, headID, history.ByPath["a.txt"].ID)
}

func TestBranch_TreeAtWithHistory_EmptyRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(&domain.Branch{Name: "main"}, nil)

	node, history, err := uc.TreeAtWithHistory(context.Background(), snow.ID(1), "", "")
	require.NoError(t, err)
	require.Empty(t, node.FileChildren)
	require.Nil(t, history.Latest)
	require.Empty(t, history.ByPath)
}

func TestBranch_TreeAtWithHistory_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)

	_, _, err := uc.TreeAtWithHistory(context.Background(), snow.ID(1), "main", "")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_PathCommitLog_File(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID, midID, rootID, head, mid, root := flatHistory(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &headID}, nil).AnyTimes()
	expectCommitManifest(repo, headID, &midID, head)
	expectCommitManifest(repo, midID, &rootID, mid)
	expectCommitManifest(repo, rootID, nil, root)
	expectFlatLog(repo, headID, midID, rootID)

	commits, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	require.Equal(t, midID, commits[0].ID)
	require.Equal(t, rootID, commits[1].ID)

	commits, err = uc.PathCommitLog(context.Background(), snow.ID(1), "main", "b.txt", nil, 0)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	require.Equal(t, headID, commits[0].ID)
	require.Equal(t, rootID, commits[1].ID)
}

func TestBranch_PathCommitLog_Directory(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID, midID, rootID := snow.ID(30), snow.ID(20), snow.ID(10)
	head := &testTreeSpec{id: 103, files: map[string]domain.Hash{"top.txt": {2}},
		children: []*testTreeSpec{{id: 203, name: "public", files: map[string]domain.Hash{"x.txt": {2}}}}}
	mid := &testTreeSpec{id: 102, files: map[string]domain.Hash{"top.txt": {2}},
		children: []*testTreeSpec{{id: 202, name: "public", files: map[string]domain.Hash{"x.txt": {1}}}}}
	root := &testTreeSpec{id: 101, files: map[string]domain.Hash{"top.txt": {1}},
		children: []*testTreeSpec{{id: 201, name: "public", files: map[string]domain.Hash{"x.txt": {1}}}}}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &headID}, nil).AnyTimes()
	expectCommitManifest(repo, headID, &midID, head)
	expectCommitManifest(repo, midID, &rootID, mid)
	expectCommitManifest(repo, rootID, nil, root)
	expectFlatLog(repo, headID, midID, rootID)

	commits, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "public", nil, 0)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	require.Equal(t, headID, commits[0].ID)
	require.Equal(t, rootID, commits[1].ID)

	commits, err = uc.PathCommitLog(context.Background(), snow.ID(1), "main", "top.txt", nil, 0)
	require.NoError(t, err)
	require.Len(t, commits, 2)
	require.Equal(t, midID, commits[0].ID)
	require.Equal(t, rootID, commits[1].ID)
}

func TestBranch_PathCommitLog_HiddenPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(30)
	restricted := &PathFilter{
		set:        &permissionSet{rules: []*domain.PBACRule{{PathPrefix: "public", Permission: domain.PermissionRead}}},
		permission: domain.PermissionRead,
	}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(restricted, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &headID}, nil)

	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "secret", nil, 0)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_PathCommitLog_EmptyRepo(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main"}, nil)

	commits, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
	require.NoError(t, err)
	require.Empty(t, commits)
}

func TestBranch_PathCommitLog_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)

	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_TreeFilesAt(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(10)
	root := &testTreeSpec{id: 101, files: map[string]domain.Hash{"a.txt": {1}},
		children: []*testTreeSpec{
			{id: 201, name: "public", files: map[string]domain.Hash{"x.txt": {2}}},
			{id: 202, name: "secret", files: map[string]domain.Hash{"y.bin": {3}}},
		}}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &headID}, nil).AnyTimes()
	expectCommitManifest(repo, headID, nil, root)

	files, err := uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "")
	require.NoError(t, err)
	require.Len(t, files, 3)
	require.Equal(t, "a.txt", files[0].Path)
	require.Equal(t, "public/x.txt", files[1].Path)
	require.Equal(t, "secret/y.bin", files[2].Path)

	files, err = uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "public")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "public/x.txt", files[0].Path)

	_, err = uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "missing")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_TreeFilesAt_HiddenPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranchWithChunks(perm, repo, newTestBranchNode(t), &stubChunkReader{})

	headID := snow.ID(10)
	restricted := &PathFilter{
		set:        &permissionSet{rules: []*domain.PBACRule{{PathPrefix: "public", Permission: domain.PermissionRead}}},
		permission: domain.PermissionRead,
	}
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(restricted, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{Name: "main", CommitID: &headID}, nil)

	_, err := uc.TreeFilesAt(context.Background(), snow.ID(1), "main", "secret")
	require.True(t, domain.IsErrorNotFound(err))
}
