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

func newTestProject(t *testing.T) (*Project, *MockprojectRepository, *MockprojectAccess, *MockorgAuthorizer) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockprojectRepository(ctrl)
	perm := NewMockprojectAccess(ctrl)
	orgs := NewMockorgAuthorizer(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return NewProject(repo, node, perm, orgs), repo, perm, orgs
}

func TestProject_List_FiltersHiddenProjects(t *testing.T) {
	project, repo, perm, _ := newTestProject(t)
	ctx := permissionCtx(42)

	repo.EXPECT().ListByOrgID(gomock.Any(), snow.ID(1)).Return([]domain.Project{
		{ID: 10, OrgID: 1, Name: "visible"},
		{ID: 11, OrgID: 1, Name: "hidden"},
	}, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(10), domain.PermissionRead).Return(true)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(11), domain.PermissionRead).Return(false)

	projects, err := project.List(ctx, snow.ID(1))
	require.NoError(t, err)
	require.Len(t, projects, 1)
	require.Equal(t, "visible", projects[0].Name)
}

func TestProject_List_RepoError(t *testing.T) {
	wantErr := errors.New("db down")
	project, repo, _, _ := newTestProject(t)

	repo.EXPECT().ListByOrgID(gomock.Any(), snow.ID(1)).Return(nil, wantErr)

	_, err := project.List(permissionCtx(42), snow.ID(1))
	require.ErrorIs(t, err, wantErr)
}

func TestProject_Get(t *testing.T) {
	project, repo, perm, _ := newTestProject(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(10), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, Name: "game"}, nil)

	got, err := project.Get(permissionCtx(42), snow.ID(10))
	require.NoError(t, err)
	require.Equal(t, "game", got.Name)
}

func TestProject_Get_HiddenIsNotFound(t *testing.T) {
	project, _, perm, _ := newTestProject(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(10), domain.PermissionRead).Return(false)

	_, err := project.Get(permissionCtx(42), snow.ID(10))
	require.True(t, domain.IsErrorNotFound(err))
}

func TestProject_Create_GlobalAdmin(t *testing.T) {
	project, repo, _, _ := newTestProject(t)
	ctx := permissionCtx(42, withAdmin())

	repo.EXPECT().GetByOrgIDAndSlug(gomock.Any(), snow.ID(1), "my-game").
		Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.Project) (*domain.Project, error) {
			require.NotZero(t, created.ID)
			require.Equal(t, snow.ID(1), created.OrgID)
			require.Equal(t, "My Game", created.Name)
			require.Equal(t, "my-game", created.Slug)
			require.Equal(t, "fun", created.Description)
			return &created, nil
		},
	)

	got, err := project.Create(ctx, snow.ID(1), " My Game ", " fun ", "")
	require.NoError(t, err)
	require.Equal(t, "my-game", got.Slug)
}

func TestProject_Create_OrgOwner(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	ctx := permissionCtx(42)

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().GetByOrgIDAndSlug(gomock.Any(), snow.ID(1), "assets").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.Project) (*domain.Project, error) {
			return &created, nil
		},
	)

	_, err := project.Create(ctx, snow.ID(1), "Assets", "", "assets")
	require.NoError(t, err)
}

func TestProject_Create_NoPermission(t *testing.T) {
	project, _, _, orgs := newTestProject(t)

	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(false, nil)

	_, err := project.Create(permissionCtx(42), snow.ID(1), "Assets", "", "")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestProject_Create_Validation(t *testing.T) {
	project, _, _, _ := newTestProject(t)
	ctx := permissionCtx(42, withAdmin())

	_, err := project.Create(ctx, snow.ID(1), "  ", "", "")
	requireUserError(t, err)

	_, err = project.Create(ctx, snow.ID(1), "Assets", "", "Bad Slug")
	requireUserError(t, err)

	_, err = project.Create(ctx, snow.ID(1), "!!!", "", "")
	requireUserError(t, err)
}

func TestProject_Create_DuplicateSlug(t *testing.T) {
	project, repo, _, _ := newTestProject(t)

	repo.EXPECT().GetByOrgIDAndSlug(gomock.Any(), snow.ID(1), "assets").
		Return(&domain.Project{ID: 9, Slug: "assets"}, nil)

	_, err := project.Create(permissionCtx(42, withAdmin()), snow.ID(1), "Assets", "", "assets")
	require.True(t, domain.IsErrorConflict(err))
}

func TestProject_Update_ProjectAdmin(t *testing.T) {
	project, repo, perm, _ := newTestProject(t)
	ctx := permissionCtx(42)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(10)).Return(true)
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, updated domain.Project) (*domain.Project, error) {
			require.Equal(t, "Renamed", updated.Name)
			require.Equal(t, "new description", updated.Description)
			return &updated, nil
		},
	)

	got, err := project.Update(ctx, snow.ID(10), " Renamed ", " new description ")
	require.NoError(t, err)
	require.Equal(t, "Renamed", got.Name)
}

func TestProject_Update_OrgOwner(t *testing.T) {
	project, repo, perm, orgs := newTestProject(t)
	ctx := permissionCtx(42)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(10)).Return(false)
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(&domain.Project{ID: 10, Name: "Renamed"}, nil)

	_, err := project.Update(ctx, snow.ID(10), "Renamed", "")
	require.NoError(t, err)
}

func TestProject_Update_NoPermission(t *testing.T) {
	project, repo, perm, orgs := newTestProject(t)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(10)).Return(false)
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(false, nil)

	_, err := project.Update(permissionCtx(42), snow.ID(10), "Renamed", "")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestProject_Update_EmptyName(t *testing.T) {
	project, repo, perm, _ := newTestProject(t)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(10)).Return(true)

	_, err := project.Update(permissionCtx(42), snow.ID(10), "  ", "")
	requireUserError(t, err)
}

func TestProject_Update_NotFound(t *testing.T) {
	project, repo, _, _ := newTestProject(t)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(nil, domain.NewErrorRecordNotFound())

	_, err := project.Update(permissionCtx(42), snow.ID(10), "Renamed", "")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestProject_Delete_OrgOwner(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)
	ctx := permissionCtx(42)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(true, nil)
	repo.EXPECT().Delete(gomock.Any(), snow.ID(10)).Return(nil)

	require.NoError(t, project.Delete(ctx, snow.ID(10)))
}

func TestProject_Delete_NoPermission(t *testing.T) {
	project, repo, _, orgs := newTestProject(t)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(&domain.Project{ID: 10, OrgID: 1}, nil)
	orgs.EXPECT().IsOrgOwner(gomock.Any(), snow.ID(1)).Return(false, nil)

	err := project.Delete(permissionCtx(42), snow.ID(10))
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestProject_Delete_NotFound(t *testing.T) {
	project, repo, _, _ := newTestProject(t)

	repo.EXPECT().Get(gomock.Any(), snow.ID(10)).Return(nil, domain.NewErrorRecordNotFound())

	err := project.Delete(permissionCtx(42), snow.ID(10))
	require.True(t, domain.IsErrorNotFound(err))
}
