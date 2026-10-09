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

func newManageBranchFixture(t *testing.T) (*Branch, *MockpermissionUsecase, *MockbranchRepository) {
	t.Helper()

	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	repo.EXPECT().RequiredReviewers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	repo.EXPECT().RequiredChecks(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	return NewBranch(perm, repo, newTestBranchNode(t)), perm, repo
}

func TestBranch_Rename_Success(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "renamed").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().RenameBranch(gomock.Any(), snow.ID(1), snow.ID(7), "renamed", "renamed").Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(7)).
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "renamed"}, nil)

	got, err := uc.Rename(ctx, snow.ID(1), "feature", " renamed ")
	require.NoError(t, err)
	require.Equal(t, "renamed", got.Name)
}

func TestBranch_Rename_ProtectedNeedsAdmin(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsProtected: true, IsDefault: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	_, err := uc.Rename(ctx, snow.ID(1), "main", "trunk")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_Rename_ProtectedByAdmin(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsProtected: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "trunk").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().RenameBranch(gomock.Any(), snow.ID(1), snow.ID(1), "trunk", "trunk").Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(1)).
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "trunk", IsProtected: true}, nil)

	got, err := uc.Rename(ctx, snow.ID(1), "main", "trunk")
	require.NoError(t, err)
	require.Equal(t, "trunk", got.Name)
}

func TestBranch_Rename_NoWritePermission(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(false)

	_, err := uc.Rename(permissionCtx(42), snow.ID(1), "feature", "renamed")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_Rename_ConflictsAndValidation(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).AnyTimes()
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil).AnyTimes()

	_, err := uc.Rename(ctx, snow.ID(1), "feature", "feature")
	require.NoError(t, err)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "taken").
		Return(&domain.Branch{ID: 9, ProjectID: 1, Name: "taken"}, nil)
	_, err = uc.Rename(ctx, snow.ID(1), "feature", "taken")
	require.True(t, domain.IsErrorConflict(err))

	_, err = uc.Rename(ctx, snow.ID(1), "feature", "bad/name")
	requireUserError(t, err)
}

func TestBranch_Rename_NotFound(t *testing.T) {
	uc, _, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "ghost").Return(nil, domain.NewErrorRecordNotFound())

	_, err := uc.Rename(permissionCtx(42), snow.ID(1), "ghost", "renamed")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_Delete_Success(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	branch := &domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}
	other := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch, other}, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(7)).Return(false, nil)
	repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)

	require.NoError(t, uc.Delete(permissionCtx(42), snow.ID(1), "feature"))
}

func TestBranch_Delete_LastBranchRefused(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	branch := &domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch}, nil)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "feature")
	require.True(t, domain.IsErrorConflict(err))
	require.Contains(t, err.Error(), "last branch")
}

func TestBranch_Delete_DefaultBranchRefused(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "main")
	require.True(t, domain.IsErrorConflict(err))
}

func TestBranch_Delete_ProtectedNeedsAdmin(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "release")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_Delete_ProtectedRefused(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "release")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Contains(t, err.Error(), "protected")
}

func TestBranch_Delete_ListBranchesError(t *testing.T) {
	wantErr := errors.New("db down")
	uc, perm, repo := newManageBranchFixture(t)
	branch := &domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).Return(nil, wantErr)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_Delete_OpenMergeRequestsRefused(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	branch := &domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}
	other := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch, other}, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(7)).Return(true, nil)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "feature")
	require.True(t, domain.IsErrorConflict(err))
	require.Contains(t, err.Error(), "open merge requests")
}

func TestBranch_Delete_MergeRequestCheckError(t *testing.T) {
	wantErr := errors.New("db down")
	uc, perm, repo := newManageBranchFixture(t)
	branch := &domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}
	other := &domain.Branch{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branch, nil)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{branch, other}, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(7)).Return(false, wantErr)

	err := uc.Delete(permissionCtx(42), snow.ID(1), "feature")
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_Delete_NotFound(t *testing.T) {
	uc, _, repo := newManageBranchFixture(t)

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "ghost").Return(nil, domain.NewErrorRecordNotFound())

	err := uc.Delete(permissionCtx(42), snow.ID(1), "ghost")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_DeleteThenRecreate(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	ctx := permissionCtx(42)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true).Times(2)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 7, ProjectID: 1, Name: "feature"}, nil)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 2, nil, snow.ID(0)).
		Return([]*domain.Branch{{ID: 7, ProjectID: 1, Name: "feature"}, {ID: 1, ProjectID: 1, Name: "main", IsDefault: true}}, nil)
	repo.EXPECT().HasOpenMergeRequests(gomock.Any(), snow.ID(1), snow.ID(7)).Return(false, nil)
	repo.EXPECT().DeleteBranch(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)
	require.NoError(t, uc.Delete(ctx, snow.ID(1), "feature"))

	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).
		Return(&domain.Branch{ID: 1, ProjectID: 1, Name: "main"}, nil)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 1, nil, snow.ID(0)).
		Return([]*domain.Branch{{ID: 1, ProjectID: 1, Name: "main", IsDefault: true}}, nil)
	repo.EXPECT().CreateBranch(gomock.Any(), gomock.Any()).
		Return(&domain.Branch{ID: 8, ProjectID: 1, Name: "feature"}, nil)

	created, err := uc.CreateBranch(ctx, snow.ID(1), "feature", BranchForkPoint{})
	require.NoError(t, err)
	require.Equal(t, "feature", created.Name)
	require.NotEqual(t, snow.ID(7), created.ID)
}

func TestBranch_SetDefault(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().SetDefaultBranch(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsDefault: true}, nil)

	got, err := uc.SetDefault(permissionCtx(42), snow.ID(1), "release")
	require.NoError(t, err)
	require.True(t, got.IsDefault)
}

func TestBranch_SetDefault_NoPermission(t *testing.T) {
	uc, perm, _ := newManageBranchFixture(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	_, err := uc.SetDefault(permissionCtx(42), snow.ID(1), "release")
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_SetProtection(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{Protected: true}).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)

	got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{Protected: true})
	require.NoError(t, err)
	require.True(t, got.IsProtected)
}

func TestBranch_SetProtection_RequiredApprovals(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	approvals := int64(2)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true, RequiredApprovals: 1}, nil)
	repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{Protected: true, RequiredApprovals: 2}).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true, RequiredApprovals: 2}, nil)

	got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{Protected: true, RequiredApprovals: &approvals})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.RequiredApprovals)
}

func TestBranch_SetProtection_RequiredApprovalsKeptWhenAbsent(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", RequiredApprovals: 2}, nil)

	got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.RequiredApprovals)
}

func TestBranch_SetProtection_NegativeApprovals(t *testing.T) {
	uc, perm, repo := newManageBranchFixture(t)
	approvals := int64(-1)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)

	_, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{Protected: true, RequiredApprovals: &approvals})
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestBranch_SetProtection_NoPermission(t *testing.T) {
	uc, perm, _ := newManageBranchFixture(t)
	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)

	_, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{Protected: true})
	require.True(t, domain.IsErrorNoPermission(err))
}

func TestBranch_SetProtection_KeepsStoredReviewersAndChecksWhenAbsent(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).
		Return([]domain.ReviewActor{{UserID: 7, Name: "alice"}}, nil).Times(2)
	repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return([]string{"build"}, nil).Times(2)
	repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{
		Protected:         true,
		RequiredReviewers: []snow.ID{7},
		RequiredChecks:    []string{"build"},
	}).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)

	got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{Protected: true})
	require.NoError(t, err)
	require.True(t, got.IsProtected)
}

func TestBranch_SetProtection_UnknownRequiredReviewer(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	users := NewMockuserLookup(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t)).WithUsers(users)

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, nil)
	repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, nil)
	users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(nil, domain.NewErrorRecordNotFound())

	_, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{
		RequiredReviewers: &[]snow.ID{7},
	})
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "user "+snow.ID(7).Base36()+" not found", domErr.Message)
}

func TestBranch_SetProtection_TogglesAndNormalizes(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))
	dismiss := false
	requireChecks := true

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", DismissStaleApprovals: true}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, nil)
	repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, nil)
	repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{
		Protected:             true,
		DismissStaleApprovals: false,
		RequireStatusChecks:   true,
		RequiredReviewers:     []snow.ID{8, 7},
		RequiredChecks:        []string{"build", "test"},
	}).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, nil)
	repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, nil)

	got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{
		Protected:             true,
		DismissStaleApprovals: &dismiss,
		RequireStatusChecks:   &requireChecks,
		RequiredReviewers:     &[]snow.ID{8, 7, 8},
		RequiredChecks:        &[]string{" test ", "build", "build", " "},
	})
	require.NoError(t, err)
	require.True(t, got.IsProtected)
}

func TestBranch_SetProtection_ClearsReviewersWithEmptyList(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	gomock.InOrder(
		repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return([]domain.ReviewActor{{UserID: 7}}, nil),
		repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, nil),
		repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{}).Return(nil),
		repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil),
		repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, nil),
		repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, nil),
	)

	got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{
		RequiredReviewers: &[]snow.ID{},
	})
	require.NoError(t, err)
	require.Empty(t, got.RequiredReviewers)
}

func TestBranch_SetProtection_AttachReviewersError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, errors.New("boom"))

	_, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{})
	require.EqualError(t, err, "boom")
}

func TestBranch_SetProtection_AttachChecksError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, nil)
	repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, errors.New("boom"))

	_, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{})
	require.EqualError(t, err, "boom")
}

func TestBranch_SetProtection_ReloadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, nil)
	repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, nil)
	repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{Protected: true}).Return(nil)
	repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).Return(nil, errors.New("boom"))

	_, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{Protected: true})
	require.EqualError(t, err, "boom")
}

func TestBranch_ListBranches_AttachError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().ListBranches(gomock.Any(), snow.ID(1), 10, gomock.Any(), gomock.Any()).
		Return([]*domain.Branch{{ID: 3, ProjectID: 1, Name: "release"}}, nil)
	repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return(nil, errors.New("boom"))

	_, err := uc.ListBranches(permissionCtx(42), snow.ID(1), 10, nil, 0)
	require.EqualError(t, err, "boom")
}

func TestBranch_SetProtection_ComparesLists(t *testing.T) {
	t.Run("different values force a write", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranch(perm, repo, newTestBranchNode(t))

		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
		gomock.InOrder(
			repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return([]domain.ReviewActor{{UserID: 7}}, nil),
			repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return([]string{"build"}, nil),
			repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{
				Protected:         true,
				RequiredReviewers: []snow.ID{8},
				RequiredChecks:    []string{"test"},
			}).Return(nil),
			repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
				Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", IsProtected: true}, nil),
			repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return([]domain.ReviewActor{{UserID: 8}}, nil),
			repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return([]string{"test"}, nil),
		)

		got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{
			Protected:         true,
			RequiredReviewers: &[]snow.ID{8},
			RequiredChecks:    &[]string{"test"},
		})
		require.NoError(t, err)
		require.True(t, got.IsProtected)
	})

	t.Run("an empty list clears stored checks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := NewMockpermissionUsecase(ctrl)
		repo := NewMockbranchRepository(ctrl)
		uc := NewBranch(perm, repo, newTestBranchNode(t))

		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "release").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil)
		gomock.InOrder(
			repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return([]domain.ReviewActor{{UserID: 7}}, nil),
			repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return([]string{"build"}, nil),
			repo.EXPECT().SetBranchProtection(gomock.Any(), snow.ID(1), snow.ID(3), domain.BranchProtection{
				RequiredReviewers: []snow.ID{7},
			}).Return(nil),
			repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(3)).
				Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release"}, nil),
			repo.EXPECT().RequiredReviewers(gomock.Any(), snow.ID(3)).Return([]domain.ReviewActor{{UserID: 7}}, nil),
			repo.EXPECT().RequiredChecks(gomock.Any(), snow.ID(3)).Return(nil, nil),
		)

		got, err := uc.SetProtection(permissionCtx(42), snow.ID(1), "release", BranchProtectionOptions{
			RequiredReviewers: &[]snow.ID{7},
			RequiredChecks:    &[]string{},
		})
		require.NoError(t, err)
		require.Empty(t, got.RequiredChecks)
	})
}

func TestPush_ProtectedBranch_DeniedEvenForAdmins(t *testing.T) {
	uc, perm, repo, _, ctx := newPushFixture(t)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", IsProtected: true}, nil)

	_, err := uc.Push(ctx, snow.ID(1), "main", "", "msg", nil, nil, "", "")
	require.True(t, domain.IsErrorNoPermission(err))
	require.Contains(t, err.Error(), "merge request")
}

func TestBranch_FastForward_ProtectedAlwaysDenied(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)

	targetHead := snow.ID(11)
	sourceHead := snow.ID(12)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead, IsProtected: true}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)

	uc := NewBranch(perm, repo, newTestBranchNode(t))
	_, err := uc.FastForward(context.Background(), snow.ID(1), "main", "feature")
	require.True(t, domain.IsErrorNoPermission(err))
	require.Contains(t, err.Error(), "merge request")
}

func TestBranch_FastForwardForMergeRequest_Protected(t *testing.T) {
	t.Run("project admin lands", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := newAllowAllPerm(ctrl)
		repo := NewMockbranchRepository(ctrl)

		targetHead := snow.ID(11)
		sourceHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead, IsProtected: true}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)
		repo.EXPECT().GetCommit(gomock.Any(), targetHead).
			Return(&domain.Commit{ID: targetHead, ProjectID: 1, TreeID: 101, Parent1ID: &targetHead}, nil).Times(2)
		repo.EXPECT().GetCommit(gomock.Any(), sourceHead).
			Return(&domain.Commit{ID: sourceHead, ProjectID: 1, TreeID: 102, Parent1ID: &targetHead}, nil).Times(2)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Name: "root"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(101)).Return(nil, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).Return(&domain.TreeNode{ID: 102, Name: "root"}, nil)
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(102)).Return(nil, nil)
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(102)).Return(nil, nil)
		repo.EXPECT().UpdateCommitIf(gomock.Any(), snow.ID(2), &targetHead, &sourceHead).Return(nil)
		repo.EXPECT().GetByProjectIDAndID(gomock.Any(), snow.ID(1), snow.ID(2)).
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead, IsProtected: true}, nil)

		uc := NewBranch(perm, repo, newTestBranchNode(t))
		updated, err := uc.FastForwardForMergeRequest(context.Background(), snow.ID(1), "main", "feature")
		require.NoError(t, err)
		require.Equal(t, &sourceHead, updated.CommitID)
	})

	t.Run("without project admin is denied", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		perm := newAllowAllPerm(ctrl)
		repo := NewMockbranchRepository(ctrl)

		targetHead := snow.ID(11)
		sourceHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &targetHead, IsProtected: true}, nil)
		repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "feature", CommitID: &sourceHead}, nil)

		uc := NewBranch(perm, repo, newTestBranchNode(t))
		_, err := uc.FastForwardForMergeRequest(context.Background(), snow.ID(1), "main", "feature")
		require.True(t, domain.IsErrorNoPermission(err))
	})
}
