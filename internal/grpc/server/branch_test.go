package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type mockUsecaseContainer struct {
	branch *usecase.Branch
	common *usecase.Common
}

func (m *mockUsecaseContainer) Auth() *usecase.Auth     { return nil }
func (m *mockUsecaseContainer) User() *usecase.User     { return nil }
func (m *mockUsecaseContainer) Branch() *usecase.Branch { return m.branch }
func (m *mockUsecaseContainer) Common() *usecase.Common { return m.common }

func newMockUsecaseContainer(t *testing.T, branch *usecase.Branch) *mockUsecaseContainer {
	t.Helper()
	return &mockUsecaseContainer{
		branch: branch,
		common: newTestCommon(),
	}
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
	repo := NewMockbranchRepository(ctrl)
	return usecase.NewBranch(perm, repo), perm, repo
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
			{ID: 1, Name: "a.txt", Mode: 0o644, SizeBytes: 10, Chunks: []domain.Chunk{{ID: 1, Hash: hash}}},
		}, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), int64(100)).
		Return([]*domain.TreeNode{{ID: 200, Name: "assets", Hash: hash}}, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), int64(200)).
		Return(nil, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), int64(200)).
		Return(nil, nil)

	resp, err := srv.GetTreeManifest(context.Background(), &pb.GetTreeManifestRequest{
		Context:   &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:    "main",
		Recursive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "main", resp.Branch)
	require.NotNil(t, resp.RootTree)
	require.Equal(t, hash.String(), resp.RootTree.TreeHash)
	require.Equal(t, "root", resp.RootTree.Path)
	require.Len(t, resp.RootTree.Files, 1)
	require.Equal(t, "a.txt", resp.RootTree.Files[0].Path)
	require.Equal(t, pb.FileMode_FILE_MODE_READ_WRITE, resp.RootTree.Files[0].Mode)
	require.Equal(t, int64(10), resp.RootTree.Files[0].SizeBytes)
	require.Equal(t, []string{hash.String()}, resp.RootTree.Files[0].ChunkHashes)
	require.Len(t, resp.RootTree.SubTrees, 1)
	require.Equal(t, "assets", resp.RootTree.SubTrees[0].Path)
	require.Equal(t, hash.String(), resp.RootTree.SubTrees[0].TreeHash)
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
