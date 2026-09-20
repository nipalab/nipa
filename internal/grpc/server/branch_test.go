package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

type mockUsecaseContainer struct {
	branch     *usecase.Branch
	common     *usecase.Common
	push       *usecase.Push
	chunk      *usecase.Chunk
	permission *usecase.Permission
	group      *usecase.Group
}

func (m *mockUsecaseContainer) Auth() *usecase.Auth     { return nil }
func (m *mockUsecaseContainer) User() *usecase.User     { return nil }
func (m *mockUsecaseContainer) Branch() *usecase.Branch { return m.branch }
func (m *mockUsecaseContainer) Common() *usecase.Common { return m.common }
func (m *mockUsecaseContainer) Push() *usecase.Push     { return m.push }
func (m *mockUsecaseContainer) Chunk() *usecase.Chunk   { return m.chunk }
func (m *mockUsecaseContainer) Permission() *usecase.Permission {
	return m.permission
}
func (m *mockUsecaseContainer) Group() *usecase.Group { return m.group }

func newMockUsecaseContainer(t *testing.T, branch *usecase.Branch) *mockUsecaseContainer {
	t.Helper()
	return &mockUsecaseContainer{
		branch: branch,
		common: newTestCommon(),
	}
}

func ptrSnow(id int64) *snow.ID {
	i := snow.ID(id)
	return &i
}

type stubOrgRepository struct {
	orgs map[string]*domain.Organization
}

func (s *stubOrgRepository) GetBySlug(_ context.Context, slug string) (*domain.Organization, error) {
	org, ok := s.orgs[slug]
	if !ok {
		return nil, errors.New("organization not found")
	}
	return org, nil
}

type stubProjectRepository struct {
	projects map[snow.ID]map[string]*domain.Project
}

func (s *stubProjectRepository) GetByOrgIDAndSlug(_ context.Context, orgID snow.ID, slug string) (*domain.Project, error) {
	projects, ok := s.projects[orgID]
	if !ok {
		return nil, errors.New("project not found")
	}
	project, ok := projects[slug]
	if !ok {
		return nil, errors.New("project not found")
	}
	return project, nil
}

func newTestCommon() *usecase.Common {
	orgRepo := &stubOrgRepository{
		orgs: map[string]*domain.Organization{
			"org": {ID: snow.ID(1), Slug: "org"},
		},
	}
	projectRepo := &stubProjectRepository{
		projects: map[snow.ID]map[string]*domain.Project{
			snow.ID(1): {
				"proj": {ID: snow.ID(42), OrgID: snow.ID(1), Slug: "proj"},
			},
		},
	}
	return usecase.NewCommon(orgRepo, projectRepo)
}

func mustBase36(id snow.ID) string {
	return id.Base36()
}

func newTestBranchUc(t *testing.T) (*usecase.Branch, *MockpermissionUsecase, *MockbranchRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	perm.EXPECT().
		CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(usecase.AllowAllFilter(), nil).
		AnyTimes()
	perm.EXPECT().
		HasPathAccess(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(true).
		AnyTimes()
	repo := NewMockbranchRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return usecase.NewBranch(perm, repo, node), perm, repo
}

func TestNew(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))
	require.NotNil(t, srv)
}

func TestGetListBranch_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	now := time.Now().Truncate(time.Second)
	branchID := snow.ID(99)
	commitID := snow.ID(7)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListBranches(gomock.Any(), projectID, 10, gomock.Any(), snow.ID(0)).
		Return([]*domain.Branch{
			{
				ID:          branchID,
				ProjectID:   projectID,
				Name:        "main",
				IsProtected: true,
				CommitID:    &commitID,
				UpdatedAt:   now,
				CreatedAt:   now,
			},
		}, nil)

	resp, err := srv.GetListBranch(context.Background(), &pb.GetListBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, resp.Branches, 1)

	b := resp.Branches[0]
	require.Equal(t, branchID.Base36(), b.Id)
	require.Equal(t, "main", b.Name)
	require.True(t, b.IsProtected)
	require.Equal(t, commitID.Base36(), b.GetCommitId())
	require.Equal(t, now.Unix(), b.CreatedAt.AsTime().Unix())
	require.Equal(t, now.Unix(), b.UpdatedAt.AsTime().Unix())
}

func TestGetListBranch_Empty(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListBranches(gomock.Any(), projectID, 10, gomock.Nil(), snow.ID(0)).
		Return([]*domain.Branch{}, nil)

	resp, err := srv.GetListBranch(context.Background(), &pb.GetListBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Branches)
}

func TestGetListBranch_WithPagination(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	lastID := snow.ID(50)
	lastUpdate := time.Now().Truncate(time.Second)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListBranches(gomock.Any(), projectID, 5, gomock.Any(), lastID).
		Return([]*domain.Branch{}, nil)

	lastIDStr := lastID.Base36()
	resp, err := srv.GetListBranch(context.Background(), &pb.GetListBranchRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		Limit:         5,
		LastUpdatedAt: timePtrToTimestamp(&lastUpdate),
		LastId:        &lastIDStr,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Branches)
}

func TestGetListBranch_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetListBranch(context.Background(), &pb.GetListBranchRequest{
		Context: &pb.ProjectContext{Org: "unknown", Project: "unknown"},
	})
	require.Error(t, err)
}

func TestGetListBranch_InvalidLastID(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	invalidID := "!!!invalid!!!"
	_, err := srv.GetListBranch(context.Background(), &pb.GetListBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		LastId:  &invalidID,
	})
	require.Error(t, err)
}

func TestGetListBranch_UsecaseError(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListBranches(gomock.Any(), projectID, 10, gomock.Nil(), snow.ID(0)).
		Return(nil, domain.NewErrorNoPermission())

	_, err := srv.GetListBranch(context.Background(), &pb.GetListBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Limit:   10,
	})
	require.Error(t, err)
}

func TestGetBranch_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	branchID := snow.ID(99)
	now := time.Now().Truncate(time.Second)
	commitID := snow.ID(7)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetByProjectIDAndID(gomock.Any(), projectID, branchID).
		Return(&domain.Branch{
			ID:          branchID,
			ProjectID:   projectID,
			Name:        "develop",
			IsProtected: false,
			CommitID:    &commitID,
			UpdatedAt:   now,
			CreatedAt:   now,
		}, nil)

	resp, err := srv.GetBranch(context.Background(), &pb.GetBranchRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		BranchId: mustBase36(branchID),
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Branch)
	require.Equal(t, branchID.Base36(), resp.Branch.Id)
	require.Equal(t, "develop", resp.Branch.Name)
	require.False(t, resp.Branch.IsProtected)
	require.Equal(t, commitID.Base36(), resp.Branch.GetCommitId())
}

func TestGetBranchByName_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	branchID := snow.ID(99)
	commitID := snow.ID(7)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "develop").
		Return(&domain.Branch{
			ID:        branchID,
			ProjectID: projectID,
			Name:      "develop",
			CommitID:  &commitID,
		}, nil)

	resp, err := srv.GetBranchByName(context.Background(), &pb.GetBranchByNameRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "develop",
	})
	require.NoError(t, err)
	require.Equal(t, branchID.Base36(), resp.Branch.Id)
	require.Equal(t, "develop", resp.Branch.Name)
	require.Equal(t, commitID.Base36(), resp.Branch.GetCommitId())
}

func TestGetBranchByName_NotFound(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "missing").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetBranchByName(context.Background(), &pb.GetBranchByNameRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "missing",
	})
	require.Error(t, err)
}

func TestGetBranch_NotFound(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	branchID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetByProjectIDAndID(gomock.Any(), projectID, branchID).
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetBranch(context.Background(), &pb.GetBranchRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		BranchId: mustBase36(branchID),
	})
	require.Error(t, err)
}

func TestGetBranch_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetBranch(context.Background(), &pb.GetBranchRequest{
		Context:  &pb.ProjectContext{Org: "unknown", Project: "unknown"},
		BranchId: "1",
	})
	require.Error(t, err)
}

func TestGetBranch_InvalidBranchID(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetBranch(context.Background(), &pb.GetBranchRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		BranchId: "!@#",
	})
	require.Error(t, err)
}

func TestGetBranch_UsecaseError(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	branchID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetByProjectIDAndID(gomock.Any(), projectID, branchID).
		Return(nil, errors.New("db down"))

	_, err := srv.GetBranch(context.Background(), &pb.GetBranchRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		BranchId: mustBase36(branchID),
	})
	require.Error(t, err)
}

func TestGetDefaultBranch_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	now := time.Now().Truncate(time.Second)
	branchID := snow.ID(99)
	commitID := snow.ID(7)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetDefaultBranch(gomock.Any(), projectID).
		Return(&domain.Branch{
			ID:          branchID,
			ProjectID:   projectID,
			Name:        "main",
			IsProtected: true,
			IsDefault:   true,
			CommitID:    &commitID,
			UpdatedAt:   now,
			CreatedAt:   now,
		}, nil)

	resp, err := srv.GetDefaultBranch(context.Background(), &pb.GetDefaultBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Branch)
	require.Equal(t, branchID.Base36(), resp.Branch.Id)
	require.Equal(t, "main", resp.Branch.Name)
	require.True(t, resp.Branch.IsProtected)
	require.True(t, resp.Branch.IsDefault)
	require.Equal(t, commitID.Base36(), resp.Branch.GetCommitId())
	require.Equal(t, now.Unix(), resp.Branch.CreatedAt.AsTime().Unix())
	require.Equal(t, now.Unix(), resp.Branch.UpdatedAt.AsTime().Unix())
}

func TestGetDefaultBranch_NotFound(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetDefaultBranch(gomock.Any(), projectID).
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetDefaultBranch(context.Background(), &pb.GetDefaultBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
	})
	require.Error(t, err)
}

func TestGetDefaultBranch_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetDefaultBranch(context.Background(), &pb.GetDefaultBranchRequest{
		Context: &pb.ProjectContext{Org: "unknown", Project: "unknown"},
	})
	require.Error(t, err)
}

func TestGetDefaultBranch_UsecaseError(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetDefaultBranch(gomock.Any(), projectID).
		Return(nil, errors.New("db down"))

	_, err := srv.GetDefaultBranch(context.Background(), &pb.GetDefaultBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
	})
	require.Error(t, err)
}

func timePtrToTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func TestGetTreeManifest_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(9)
	var hash domain.Hash
	for i := range hash {
		hash[i] = byte(i)
	}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, TreeID: 100}, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Name: "root", Hash: hash}, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), int64(100)).
		Return([]*domain.File{
			{ID: 1, Name: "a.txt", Mode: 0o644, SizeBytes: 10, Hash: hash, Chunks: []domain.Chunk{{ID: 1, Hash: hash}}},
		}, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), int64(100)).
		Return([]*domain.TreeNode{{ID: 200, Name: "assets", Hash: hash}}, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), int64(200)).
		Return([]*domain.File{{ID: 2, Name: "logo.png", Mode: 0o644, Hash: hash}}, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), int64(200)).
		Return(nil, nil)

	resp, err := srv.GetTreeManifest(context.Background(), &pb.GetTreeManifestRequest{
		Context:   &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:    "main",
		Recursive: true,
	})
	wantAssetsHash := treehash.TreeHash([]treehash.FileEntry{{Name: "logo.png", Hash: hash, Mode: 0o644}}, nil)
	wantRootHash := treehash.TreeHash(
		[]treehash.FileEntry{{Name: "a.txt", Hash: hash, Mode: 0o644}},
		[]treehash.TreeEntry{{Name: "assets", Hash: wantAssetsHash}},
	)

	require.NoError(t, err)
	require.Equal(t, "main", resp.Branch)
	require.NotNil(t, resp.RootTree)
	require.Equal(t, wantRootHash.String(), resp.RootTree.TreeHash)
	require.Equal(t, "root", resp.RootTree.Path)
	require.Len(t, resp.RootTree.Files, 1)
	require.Equal(t, "a.txt", resp.RootTree.Files[0].Path)
	require.Equal(t, pb.FileMode_FILE_MODE_READ_WRITE, resp.RootTree.Files[0].Mode)
	require.Equal(t, int64(10), resp.RootTree.Files[0].SizeBytes)
	require.Equal(t, []string{hash.String()}, resp.RootTree.Files[0].ChunkHashes)
	require.Len(t, resp.RootTree.SubTrees, 1)
	require.Equal(t, "assets", resp.RootTree.SubTrees[0].Path)
	require.Equal(t, wantAssetsHash.String(), resp.RootTree.SubTrees[0].TreeHash)
}

func TestGetTreeManifest_NotRecursive(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(9)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, TreeID: 100}, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Name: "root"}, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), int64(100)).
		Return(nil, nil)

	resp, err := srv.GetTreeManifest(context.Background(), &pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:  "main",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.RootTree)
	require.Empty(t, resp.RootTree.SubTrees)
}

func TestGetTreeManifest_LegacyPathFallback(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(9)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, TreeID: 100}, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Name: "root"}, nil)
	repo.EXPECT().
		GetTreeChildByName(gomock.Any(), int64(100), "missing").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetTreeManifest(context.Background(), &pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:  "main",
		Path:    "missing",
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestGetTreeManifest_TreeHashMatch(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(9)
	var hash domain.Hash
	hash[0] = 1

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		GetCommit(gomock.Any(), commitID).
		Return(&domain.Commit{ID: commitID, TreeID: 100}, nil)
	repo.EXPECT().
		GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Name: "root", Hash: hash}, nil)

	resp, err := srv.GetTreeManifest(context.Background(), &pb.GetTreeManifestRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:   "main",
		TreeHash: &[]string{hash.String()}[0],
	})
	require.NoError(t, err)
	require.Equal(t, "main", resp.Branch)
	require.Nil(t, resp.RootTree)
}

func TestGetTreeManifest_BranchNotFound(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "unknown").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetTreeManifest(context.Background(), &pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:  "unknown",
	})
	require.Error(t, err)
}

func TestGetTreeManifest_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetTreeManifest(context.Background(), &pb.GetTreeManifestRequest{
		Context: &pb.ProjectContext{Org: "unknown", Project: "unknown"},
		Branch:  "main",
	})
	require.Error(t, err)
}

func TestCreateBranch_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(7)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "main").
			Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil),
	)

	repo.EXPECT().
		CreateBranch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b domain.Branch) (*domain.Branch, error) {
			return &b, nil
		})

	resp, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context:    &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:       "feature",
		FromBranch: "main",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.GetBranch())
	require.Equal(t, "feature", resp.GetBranch().GetName())
	require.False(t, resp.GetBranch().GetIsDefault())
	require.NotEmpty(t, resp.GetBranch().GetId())
}

func TestCreateBranch_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context: &pb.ProjectContext{Org: "unknown", Project: "unknown"},
		Name:    "feature",
	})
	require.Error(t, err)
}

func TestCreateBranch_UsecaseError(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "feature").
		Return(nil, errors.New("db down"))

	_, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "feature",
	})
	require.Error(t, err)
}

func TestCreateBranch_Conflict(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "feature").
		Return(&domain.Branch{ID: 5, ProjectID: projectID, Name: "feature"}, nil)

	_, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "feature",
	})
	require.Error(t, err)
}

func TestCreateBranch_FromCommitID(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	forkID := snow.ID(77)
	commit := &domain.Commit{ID: forkID, ProjectID: projectID}

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

	resp, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context:        &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:           "feature",
		FromBranch:     "main",
		FromCommitId:   mustBase36(forkID),
		FromCommitHash: someHashHex(t, 0xab),
	})
	require.NoError(t, err)
	require.Equal(t, "feature", resp.GetBranch().GetName())
	require.NotNil(t, captured.CommitID)
	require.Equal(t, forkID, *captured.CommitID, "commit id must win over commit hash and branch name")
}

func TestCreateBranch_FromCommitHash(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commit := &domain.Commit{ID: snow.ID(88), ProjectID: projectID}
	wantHash, err := domain.ParseHashHex(someHashHex(t, 0xcd))
	require.NoError(t, err)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "feature").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommitByHash(gomock.Any(), wantHash).
			Return(commit, nil),
	)

	var captured domain.Branch
	repo.EXPECT().
		CreateBranch(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, b domain.Branch) (*domain.Branch, error) {
			captured = b
			return &b, nil
		})

	resp, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context:        &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:           "feature",
		FromBranch:     "main",
		FromCommitHash: someHashHex(t, 0xcd),
	})
	require.NoError(t, err)
	require.Equal(t, "feature", resp.GetBranch().GetName())
	require.NotNil(t, captured.CommitID)
	require.Equal(t, commit.ID, *captured.CommitID, "commit hash must win over the branch name")
}

func TestCreateBranch_InvalidCommitID(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	_, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:         "feature",
		FromCommitId: "!!!not-base36!!!",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid commit id")
}

func TestCreateBranch_InvalidCommitHash(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	_, err := srv.CreateBranch(context.Background(), &pb.CreateBranchRequest{
		Context:        &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:           "feature",
		FromCommitHash: "zzz",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid commit hash")
}

func someHashHex(t *testing.T, b byte) string {
	t.Helper()
	h := bytes.Repeat([]byte{b}, 32)
	return hex.EncodeToString(h)
}

func TestGetMergeBase_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	base := snow.ID(10)
	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "main").
			Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: &targetHead}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "feature").
			Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "feature", CommitID: &sourceHead}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, TreeID: 101, Hash: domain.Hash{8}, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, TreeID: 102, Hash: domain.Hash{9}, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, TreeID: 101, Hash: domain.Hash{8}, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, TreeID: 102, Hash: domain.Hash{9}, Parent1ID: &base}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), base).Return(&domain.Commit{ID: base, TreeID: 100}, nil),
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
			Return(&domain.TreeNode{ID: 100, Name: "root"}, nil),
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil),
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil),
	)

	resp, err := srv.GetMergeBase(context.Background(), &pb.GetMergeBaseRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		TargetBranch: "main",
		SourceBranch: "feature",
	})
	require.NoError(t, err)
	require.Equal(t, "main", resp.TargetBranch)
	require.Equal(t, "feature", resp.SourceBranch)
	require.Equal(t, targetHead.Base36(), resp.TargetCommitId)
	require.Equal(t, sourceHead.Base36(), resp.SourceCommitId)
	require.Equal(t, base.Base36(), resp.MergeBaseCommitId)
	require.NotNil(t, resp.MergeBaseTree)
	require.Equal(t, "root", resp.MergeBaseTree.Path)
	require.Equal(t, domain.Hash{8}.String(), resp.TargetCommitHash)
	require.Equal(t, domain.Hash{9}.String(), resp.SourceCommitHash)
}

func TestGetMergeBase_EmptyBranches(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "main").
			Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main"}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "feature").
			Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "feature"}, nil),
	)

	resp, err := srv.GetMergeBase(context.Background(), &pb.GetMergeBaseRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		TargetBranch: "main",
		SourceBranch: "feature",
	})
	require.NoError(t, err)
	require.Empty(t, resp.TargetCommitId)
	require.Empty(t, resp.SourceCommitId)
	require.Empty(t, resp.MergeBaseCommitId)
	require.Nil(t, resp.MergeBaseTree)
}

func TestGetMergeBase_BranchNotFound(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "unknown").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetMergeBase(context.Background(), &pb.GetMergeBaseRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		TargetBranch: "unknown",
		SourceBranch: "feature",
	})
	require.Error(t, err)
}

func TestGetMergeBase_CommitIds(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	base := snow.ID(10)
	targetID := snow.ID(11)
	sourceID := snow.ID(12)
	targetCommit := &domain.Commit{ID: targetID, ProjectID: projectID, TreeID: 101, Hash: domain.Hash{8}, Parent1ID: &base}
	sourceCommit := &domain.Commit{ID: sourceID, ProjectID: projectID, TreeID: 102, Hash: domain.Hash{9}, Parent1ID: &base}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().GetCommit(gomock.Any(), targetID).Return(targetCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceID).Return(sourceCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), targetID).Return(targetCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), sourceID).Return(sourceCommit, nil),
		repo.EXPECT().GetCommit(gomock.Any(), base).Return(&domain.Commit{ID: base, TreeID: 100}, nil),
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
			Return(&domain.TreeNode{ID: 100, Name: "root"}, nil),
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil),
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil),
	)

	targetIDStr := mustBase36(targetID)
	sourceIDStr := mustBase36(sourceID)
	resp, err := srv.GetMergeBase(context.Background(), &pb.GetMergeBaseRequest{
		Context:        &pb.ProjectContext{Org: "org", Project: "proj"},
		TargetCommitId: &targetIDStr,
		SourceCommitId: &sourceIDStr,
		TargetBranch:   "ignored",
		SourceBranch:   "ignored",
	})
	require.NoError(t, err)
	require.Empty(t, resp.TargetBranch)
	require.Empty(t, resp.SourceBranch)
	require.Equal(t, targetID.Base36(), resp.TargetCommitId)
	require.Equal(t, sourceID.Base36(), resp.SourceCommitId)
	require.Equal(t, base.Base36(), resp.MergeBaseCommitId)
	require.NotNil(t, resp.MergeBaseTree)
	require.Equal(t, domain.Hash{8}.String(), resp.TargetCommitHash)
}

func TestGetMergeBase_InvalidCommitID(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	invalid := "!!!not-base36!!!"
	_, err := srv.GetMergeBase(context.Background(), &pb.GetMergeBaseRequest{
		Context:        &pb.ProjectContext{Org: "org", Project: "proj"},
		TargetCommitId: &invalid,
		SourceBranch:   "feature",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid commit id")
}

func TestGetMergeBase_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetMergeBase(context.Background(), &pb.GetMergeBaseRequest{
		Context:      &pb.ProjectContext{Org: "unknown", Project: "unknown"},
		TargetBranch: "main",
		SourceBranch: "feature",
	})
	require.Error(t, err)
}

func TestMergeFastForward_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	head := snow.ID(12)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: ptrSnow(11)}, nil)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "feature").
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "feature", CommitID: &head}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(11)).
		Return(&domain.Commit{ID: 11, ProjectID: projectID, TreeID: 101, Parent1ID: ptrSnow(11)}, nil).
		Times(2)
	repo.EXPECT().GetCommit(gomock.Any(), head).
		Return(&domain.Commit{ID: head, ProjectID: projectID, TreeID: 102, Parent1ID: ptrSnow(11)}, nil).
		Times(2)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).
		Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).
		Return([]*domain.File{{ID: 1, Name: "a.txt", TreeID: 101}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).
		Return(&domain.TreeNode{ID: 102, Name: "root"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).
		Return([]*domain.File{{ID: 2, Name: "b.txt", TreeID: 102}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil)
	repo.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(1), ptrSnow(11), &head).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), projectID, snow.ID(1)).
		Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: &head}, nil)

	resp, err := srv.MergeFastForward(context.Background(), &pb.MergeFastForwardRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		TargetBranch: "main",
		SourceBranch: "feature",
	})
	require.NoError(t, err)
	require.Equal(t, "main", resp.Branch.Name)
	require.Equal(t, head.Base36(), resp.MovedToCommitId)
}

func TestMergeFastForward_Diverged(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)
	gomock.InOrder(
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "main").
			Return(&domain.Branch{ID: 1, ProjectID: projectID, Name: "main", CommitID: ptrSnow(11)}, nil),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "feature").
			Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "feature", CommitID: ptrSnow(12)}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(11)).
			Return(&domain.Commit{ID: 11, TreeID: 101, Parent1ID: ptrSnow(10)}, nil),
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(12)).
			Return(&domain.Commit{ID: 12, TreeID: 102, Parent1ID: ptrSnow(10)}, nil),
	)

	_, err := srv.MergeFastForward(context.Background(), &pb.MergeFastForwardRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		TargetBranch: "main",
		SourceBranch: "feature",
	})
	require.Error(t, err)
}

func TestMergeFastForward_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.MergeFastForward(context.Background(), &pb.MergeFastForwardRequest{
		Context:      &pb.ProjectContext{Org: "unknown", Project: "unknown"},
		TargetBranch: "main",
		SourceBranch: "feature",
	})
	require.Error(t, err)
}
