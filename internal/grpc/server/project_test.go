package server

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type listProjectRepo struct {
	byOrg map[snow.ID][]domain.Project
	err   error
}

func (r *listProjectRepo) Create(context.Context, domain.Project) (*domain.Project, error) {
	return nil, nil
}
func (r *listProjectRepo) CreateWithDefaultBranch(context.Context, domain.Project, domain.Branch) (*domain.Project, error) {
	return nil, nil
}
func (r *listProjectRepo) Get(context.Context, snow.ID) (*domain.Project, error) { return nil, nil }
func (r *listProjectRepo) GetByOrgIDAndSlug(context.Context, snow.ID, string) (*domain.Project, error) {
	return nil, nil
}
func (r *listProjectRepo) ListByOrgID(_ context.Context, orgID snow.ID) ([]domain.Project, error) {
	return r.byOrg[orgID], r.err
}
func (r *listProjectRepo) Update(context.Context, domain.Project) (*domain.Project, error) {
	return nil, nil
}
func (r *listProjectRepo) Delete(context.Context, snow.ID) error { return nil }

type readableProjects map[snow.ID]bool

func (r readableProjects) HasProjectAccess(_ context.Context, id snow.ID, _ domain.Permission) bool {
	return r[id]
}
func (r readableProjects) AdminHasProject(context.Context, snow.ID) bool { return false }

func newProjectListServer(t *testing.T, repo *listProjectRepo, readable readableProjects) (*nipaServer, *MockorgMemberRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	orgs := NewMockorgMemberRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	authz := NewMockorgAuthorizer(ctrl)
	uc := &mockUsecaseContainer{
		org:     usecase.NewOrg(orgs, node),
		project: usecase.NewProject(repo, node, readable, authz),
	}
	return New(uc), orgs
}

func TestNipaServer_ListMyProjects(t *testing.T) {
	repo := &listProjectRepo{byOrg: map[snow.ID][]domain.Project{
		1: {{ID: 10, Slug: "game", Name: "Game", Description: "main title"}, {ID: 11, Slug: "secret", Name: "Secret"}},
		2: {{ID: 20, Slug: "tools", Name: "Tools"}},
	}}
	srv, orgs := newProjectListServer(t, repo, readableProjects{10: true, 20: true})
	orgs.EXPECT().ListForUser(gomock.Any(), snow.ID(7)).Return([]*domain.OrgMembership{
		{Org: domain.Organization{ID: 1, Slug: "studio", Name: "Studio"}, Role: "owner"},
		{Org: domain.Organization{ID: 2, Slug: "infra", Name: "Infra"}, Role: "member"},
	}, nil)

	resp, err := srv.ListMyProjects(adminClaimCtx(7), &pb.ListMyProjectsRequest{})
	require.NoError(t, err)
	require.Len(t, resp.GetProjects(), 2)
	require.Equal(t, "studio", resp.GetProjects()[0].GetOrg())
	require.Equal(t, "Studio", resp.GetProjects()[0].GetOrgName())
	require.Equal(t, "owner", resp.GetProjects()[0].GetOrgRole())
	require.Equal(t, "game", resp.GetProjects()[0].GetProject())
	require.Equal(t, "Game", resp.GetProjects()[0].GetName())
	require.Equal(t, "main title", resp.GetProjects()[0].GetDescription())
	require.Equal(t, "tools", resp.GetProjects()[1].GetProject())
}

func TestNipaServer_ListMyProjectsErrors(t *testing.T) {
	repo := &listProjectRepo{}
	srv, orgs := newProjectListServer(t, repo, readableProjects{})

	_, err := srv.ListMyProjects(context.Background(), &pb.ListMyProjectsRequest{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	orgs.EXPECT().ListForUser(gomock.Any(), gomock.Any()).Return(nil, domain.NewErrorDatabase("boom"))
	_, err = srv.ListMyProjects(adminClaimCtx(7), &pb.ListMyProjectsRequest{})
	require.Error(t, err)

	orgs.EXPECT().ListForUser(gomock.Any(), gomock.Any()).Return([]*domain.OrgMembership{{Org: domain.Organization{ID: 1}}}, nil)
	repo.err = errors.New("db down")
	_, err = srv.ListMyProjects(adminClaimCtx(7), &pb.ListMyProjectsRequest{})
	require.Error(t, err)

	orgs.EXPECT().ListForUser(gomock.Any(), gomock.Any()).Return(nil, nil)
	resp, err := srv.ListMyProjects(adminClaimCtx(7), &pb.ListMyProjectsRequest{})
	require.NoError(t, err)
	require.Empty(t, resp.GetProjects())
}

func TestNipaServer_ListDirectory(t *testing.T) {
	branchUc, perm, repo := newTestBranchUc(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionRead).Return(true).AnyTimes()
	srv := New(newMockUsecaseContainer(t, branchUc))
	commitID := snow.ID(9)
	repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(42)).Return(&domain.Branch{Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(&domain.Commit{ID: commitID, TreeID: 100}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).Return(&domain.TreeNode{ID: 100}, nil)
	repo.EXPECT().GetTreeChildByName(gomock.Any(), int64(100), "art").Return(&domain.TreeNode{ID: 101, Name: "art"}, nil)
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return([]*domain.File{{Name: "hero.png", SizeBytes: 64}}, nil)
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return([]*domain.TreeNode{{Name: "props"}}, nil)

	resp, err := srv.ListDirectory(adminClaimCtx(1), &pb.ListDirectoryRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"}, Path: "art",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetEntries(), 2)
	require.Equal(t, "props", resp.GetEntries()[0].GetName())
	require.True(t, resp.GetEntries()[0].GetIsDir())
	require.Equal(t, "hero.png", resp.GetEntries()[1].GetName())
	require.EqualValues(t, 64, resp.GetEntries()[1].GetSizeBytes())

	_, err = srv.ListDirectory(adminClaimCtx(1), &pb.ListDirectoryRequest{Context: &pb.ProjectContext{Org: "nope", Project: "proj"}})
	require.Error(t, err)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "gone-branch").Return(nil, domain.NewErrorNotFound("branch not found"))
	_, err = srv.ListDirectory(adminClaimCtx(1), &pb.ListDirectoryRequest{Context: &pb.ProjectContext{Org: "org", Project: "proj"}, Revision: "gone-branch"})
	require.Equal(t, codes.NotFound, status.Code(err))
}
