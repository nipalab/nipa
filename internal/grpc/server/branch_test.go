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
	require.Equal(t, commitID.Base36(), b.CommitId)
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
	require.Equal(t, commitID.Base36(), resp.Branch.CommitId)
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

func timePtrToTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
