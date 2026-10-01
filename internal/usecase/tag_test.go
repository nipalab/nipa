package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestNewTag(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	require.NotNil(t, uc)
}

func TestTag_ListTags_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.ListTags(context.Background(), snow.ID(1), 10, nil, 0)

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestTag_ListTags_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	want := []*domain.Tag{
		{ID: 1, ProjectID: 1, Name: "v1.0.0"},
		{ID: 2, ProjectID: 1, Name: "v1.0.1"},
	}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	after := time.Now().Add(-time.Hour)
	lastID := snow.ID(0)
	repo.EXPECT().
		ListTags(gomock.Any(), snow.ID(1), 10, &after, lastID).
		Return(want, nil)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	got, err := uc.ListTags(context.Background(), snow.ID(1), 10, &after, lastID)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestTag_ListTags_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListTags(gomock.Any(), snow.ID(1), 10, gomock.Nil(), snow.ID(0)).
		Return(nil, wantErr)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.ListTags(context.Background(), snow.ID(1), 10, nil, 0)
	require.ErrorIs(t, err, wantErr)
}

func TestTag_GetTagByName_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(false)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.GetTagByName(context.Background(), snow.ID(1), "v1.0.0")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestTag_GetTagByName_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	want := &domain.Tag{ID: 2, ProjectID: 1, Name: "v1.0.0", CommitID: 9}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").
		Return(want, nil)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	got, err := uc.GetTagByName(context.Background(), snow.ID(1), "v1.0.0")
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestTag_GetTagByName_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), snow.ID(1), "missing").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.GetTagByName(context.Background(), snow.ID(1), "missing")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `tag "missing" not found`, domErr.Message)
}

func TestTag_GetTagByName_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").
		Return(nil, wantErr)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.GetTagByName(context.Background(), snow.ID(1), "v1.0.0")
	require.ErrorIs(t, err, wantErr)
}

func TestTag_CreateTag_NoClaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(context.Background(), snow.ID(1), "v1.0.0", TagTarget{}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestTag_CreateTag_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(false)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v1.0.0", TagTarget{}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestTag_CreateTag_EmptyName(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "   ", TagTarget{}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, "tag name must not be empty", domErr.Message)
}

func TestTag_CreateTag_InvalidName(t *testing.T) {
	for _, name := range []string{"release/1.0", "..", ".", "tag name"} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			perm := newAllowAllPerm(ctrl)
			repo := NewMocktagRepository(ctrl)

			perm.EXPECT().
				HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
				Return(true)

			uc := NewTag(perm, repo, newTestBranchNode(t))
			_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), name, TagTarget{}, "")

			var domErr *domain.Error
			require.ErrorAs(t, err, &domErr)
			require.Equal(t, 400, domErr.Code)
		})
	}
}

func TestTag_CreateTag_AlreadyExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").
		Return(&domain.Tag{ID: 5, ProjectID: 1, Name: "v1.0.0"}, nil)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v1.0.0", TagTarget{}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 409, domErr.Code)
	require.Equal(t, `tag "v1.0.0" already exists`, domErr.Message)
}

func TestTag_CreateTag_UniquenessCheckError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").
		Return(nil, wantErr)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v1.0.0", TagTarget{}, "")
	require.ErrorIs(t, err, wantErr)
}

func TestTag_CreateTag_FromDefaultBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetDefaultBranch(gomock.Any(), snow.ID(1)).
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &commitID}, nil),
	)

	var captured domain.Tag
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			captured = tag
			return &tag, nil
		})

	uc := NewTag(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateTag(permissionCtx(7), snow.ID(1), " v1.0.0 ", TagTarget{}, "release the kraken")
	require.NoError(t, err)
	require.Equal(t, "v1.0.0", created.Name)
	require.NotZero(t, captured.ID)
	require.Equal(t, snow.ID(1), captured.ProjectID)
	require.Equal(t, commitID, captured.CommitID)
	require.Equal(t, "release the kraken", captured.Message)
	require.Equal(t, snow.ID(7), captured.UserID)
}

func TestTag_CreateTag_NoCommits(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetDefaultBranch(gomock.Any(), snow.ID(1)).
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main"}, nil),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v1.0.0", TagTarget{}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
	require.Equal(t, `cannot create tag "v1.0.0": the project has no commits`, domErr.Message)
}

func TestTag_CreateTag_NoDefaultBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetDefaultBranch(gomock.Any(), snow.ID(1)).
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v1.0.0", TagTarget{}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestTag_CreateTag_FromBranchName(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v2.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "release").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "release", CommitID: &commitID}, nil),
	)

	var captured domain.Tag
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			captured = tag
			return &tag, nil
		})

	uc := NewTag(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v2.0.0", TagTarget{BranchName: "release"}, "")
	require.NoError(t, err)
	require.Equal(t, "v2.0.0", created.Name)
	require.Equal(t, commitID, captured.CommitID)
}

func TestTag_CreateTag_BranchNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v2.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "release").
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v2.0.0", TagTarget{BranchName: "release"}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `branch "release" not found`, domErr.Message)
}

func TestTag_CreateTag_BranchNoCommits(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v2.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetBranchByName(gomock.Any(), snow.ID(1), "empty").
			Return(&domain.Branch{ID: 3, ProjectID: 1, Name: "empty"}, nil),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v2.0.0", TagTarget{BranchName: "empty"}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestTag_CreateTag_FromCommitID(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v3.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), commitID).
			Return(&domain.Commit{ID: commitID, ProjectID: 1}, nil),
	)

	var captured domain.Tag
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			captured = tag
			return &tag, nil
		})

	uc := NewTag(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v3.0.0", TagTarget{CommitID: &commitID}, "")
	require.NoError(t, err)
	require.Equal(t, "v3.0.0", created.Name)
	require.Equal(t, commitID, captured.CommitID)
}

func TestTag_CreateTag_FromCommitID_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v3.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), commitID).
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v3.0.0", TagTarget{CommitID: &commitID}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, "commit "+commitID.Base36()+" not found", domErr.Message)
}

func TestTag_CreateTag_FromCommitID_OtherProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v3.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), commitID).
			Return(&domain.Commit{ID: commitID, ProjectID: 2}, nil),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v3.0.0", TagTarget{CommitID: &commitID}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestTag_CreateTag_FromCommitID_LookupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(42)
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v3.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommit(gomock.Any(), commitID).
			Return(nil, wantErr),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v3.0.0", TagTarget{CommitID: &commitID}, "")
	require.ErrorIs(t, err, wantErr)
}

func TestTag_CreateTag_FromCommitHash(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(42)
	hash := domain.Hash{0x1a, 0x2b}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v4.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommitByHash(gomock.Any(), hash).
			Return(&domain.Commit{ID: commitID, ProjectID: 1}, nil),
	)

	var captured domain.Tag
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			captured = tag
			return &tag, nil
		})

	uc := NewTag(perm, repo, newTestBranchNode(t))
	created, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v4.0.0", TagTarget{CommitHash: &hash}, "")
	require.NoError(t, err)
	require.Equal(t, "v4.0.0", created.Name)
	require.Equal(t, commitID, captured.CommitID)
}

func TestTag_CreateTag_FromCommitHash_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	hash := domain.Hash{0x1a}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v4.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommitByHash(gomock.Any(), hash).
			Return(nil, domain.NewErrorRecordNotFound()),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v4.0.0", TagTarget{CommitHash: &hash}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, "commit "+hash.String()+" not found", domErr.Message)
}

func TestTag_CreateTag_FromCommitHash_OtherProject(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	hash := domain.Hash{0x1a}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v4.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetCommitByHash(gomock.Any(), hash).
			Return(&domain.Commit{ID: 42, ProjectID: 2}, nil),
	)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v4.0.0", TagTarget{CommitHash: &hash}, "")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
}

func TestTag_CreateTag_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	commitID := snow.ID(42)
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), snow.ID(1), "v5.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		repo.EXPECT().
			GetDefaultBranch(gomock.Any(), snow.ID(1)).
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &commitID}, nil),
	)
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		Return(nil, wantErr)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	_, err := uc.CreateTag(permissionCtx(7), snow.ID(1), "v5.0.0", TagTarget{}, "")
	require.ErrorIs(t, err, wantErr)
}

func TestTag_DeleteTag_NoClaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	err := uc.DeleteTag(context.Background(), snow.ID(1), "v1.0.0")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestTag_DeleteTag_NoPermission(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(false)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	err := uc.DeleteTag(permissionCtx(7), snow.ID(1), "v1.0.0")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 403, domErr.Code)
}

func TestTag_DeleteTag_NotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), snow.ID(1), "missing").
		Return(nil, domain.NewErrorRecordNotFound())

	uc := NewTag(perm, repo, newTestBranchNode(t))
	err := uc.DeleteTag(permissionCtx(7), snow.ID(1), "missing")

	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 404, domErr.Code)
	require.Equal(t, `tag "missing" not found`, domErr.Message)
}

func TestTag_DeleteTag_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	tag := &domain.Tag{ID: 7, ProjectID: 1, Name: "v1.0.0"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	repo.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").Return(tag, nil)
	repo.EXPECT().DeleteTag(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	require.NoError(t, uc.DeleteTag(permissionCtx(7), snow.ID(1), "v1.0.0"))
}

func TestTag_DeleteTag_RepositoryError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)

	tag := &domain.Tag{ID: 7, ProjectID: 1, Name: "v1.0.0"}
	wantErr := errors.New("db down")

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).
		Return(true)

	repo.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").Return(tag, nil)
	repo.EXPECT().DeleteTag(gomock.Any(), snow.ID(1), snow.ID(7)).Return(wantErr)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	err := uc.DeleteTag(permissionCtx(7), snow.ID(1), "v1.0.0")
	require.ErrorIs(t, err, wantErr)
}

func TestTag_CreateEmitsWebhookEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)
	ctx := permissionCtx(7)
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().CreateTag(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			created := tag
			return &created, nil
		})
	hooks := NewMockhookTagGate(ctrl)
	hooks.EXPECT().EmitTag(gomock.Any(), domain.WebhookEventTagCreated, snow.ID(1), gomock.Any(), snow.ID(7)).DoAndReturn(
		func(_ context.Context, _ string, _ snow.ID, tag *domain.Tag, _ snow.ID) error {
			require.Equal(t, "v1.0.0", tag.Name)
			require.Equal(t, head, tag.CommitID)
			return nil
		})

	uc := NewTag(perm, repo, newTestBranchNode(t))
	created, err := uc.WithHooks(hooks).CreateTag(ctx, snow.ID(1), "v1.0.0", TagTarget{}, "release")
	require.NoError(t, err)
	require.Equal(t, "v1.0.0", created.Name)
}

func TestTag_DeleteEmitsWebhookEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)
	ctx := permissionCtx(7)
	tag := &domain.Tag{ID: 7, ProjectID: 1, Name: "v1.0.0"}

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").Return(tag, nil)
	repo.EXPECT().DeleteTag(gomock.Any(), snow.ID(1), snow.ID(7)).Return(nil)
	hooks := NewMockhookTagGate(ctrl)
	hooks.EXPECT().EmitTag(gomock.Any(), domain.WebhookEventTagDeleted, snow.ID(1), tag, snow.ID(7)).Return(nil)

	uc := NewTag(perm, repo, newTestBranchNode(t))
	require.NoError(t, uc.WithHooks(hooks).DeleteTag(ctx, snow.ID(1), "v1.0.0"))
}

func TestTag_WebhookHookFailureIsNotFatal(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMocktagRepository(ctrl)
	ctx := permissionCtx(7)
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v1.0.0").Return(nil, domain.NewErrorRecordNotFound())
	repo.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().CreateTag(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			created := tag
			return &created, nil
		})
	hooks := NewMockhookTagGate(ctrl)
	hooks.EXPECT().EmitTag(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("dispatcher down"))

	uc := NewTag(perm, repo, newTestBranchNode(t))
	created, err := uc.WithHooks(hooks).CreateTag(ctx, snow.ID(1), "v1.0.0", TagTarget{}, "")
	require.NoError(t, err)
	require.NotZero(t, created.ID)
}
