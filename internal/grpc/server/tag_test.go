package server

import (
	"context"
	"strings"
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
	"github.com/nipalab/nipa/internal/usecase"
)

func newTestTagUc(t *testing.T) (*usecase.Tag, *MockpermissionUsecase, *MocktagRepository, *MockbranchRepository) {
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
	repo := NewMocktagRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return usecase.NewTag(perm, repo, branchRepo, node), perm, repo, branchRepo
}

func newTestTagServer(t *testing.T) (*nipaServer, *MockpermissionUsecase, *MocktagRepository, *MockbranchRepository) {
	t.Helper()
	tag, perm, repo, branchRepo := newTestTagUc(t)
	return New(&mockUsecaseContainer{tag: tag, common: newTestCommon()}), perm, repo, branchRepo
}

func tagClaimCtx() context.Context {
	return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
}

func TestListTags_Success(t *testing.T) {
	srv, perm, repo, _ := newTestTagServer(t)
	projectID := snow.ID(42)
	now := time.Now().UTC().Truncate(time.Second)
	commitID := snow.ID(11)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		ListTags(gomock.Any(), projectID, 10, gomock.Nil(), snow.ID(0)).
		Return([]*domain.Tag{{
			ID:        99,
			ProjectID: projectID,
			Name:      "v1.0.0",
			CommitID:  commitID,
			Message:   "first release",
			UserID:    7,
			CreatedAt: now,
			UpdatedAt: now,
		}}, nil)

	resp, err := srv.ListTags(context.Background(), &pb.ListTagsRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, resp.Tags, 1)

	tag := resp.Tags[0]
	require.Equal(t, snow.ID(99).Base36(), tag.Id)
	require.Equal(t, "v1.0.0", tag.Name)
	require.Equal(t, commitID.Base36(), tag.CommitId)
	require.Equal(t, "first release", tag.Message)
	require.Equal(t, snow.ID(7).Base36(), tag.UserId)
	require.Equal(t, now, tag.CreatedAt.AsTime())
	require.Equal(t, now, tag.UpdatedAt.AsTime())
}

func TestListTags_Pagination(t *testing.T) {
	srv, perm, repo, _ := newTestTagServer(t)
	projectID := snow.ID(42)
	ts := time.Now().UTC().Truncate(time.Second)
	lastID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	var gotCreated *time.Time
	repo.EXPECT().
		ListTags(gomock.Any(), projectID, 10, gomock.Any(), lastID).
		DoAndReturn(func(_ context.Context, _ snow.ID, _ int, createdBefore *time.Time, _ snow.ID) ([]*domain.Tag, error) {
			gotCreated = createdBefore
			return nil, nil
		})

	resp, err := srv.ListTags(context.Background(), &pb.ListTagsRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		Limit:         10,
		LastCreatedAt: timestamppb.New(ts),
		LastId:        strPtr(lastID.Base36()),
	})
	require.NoError(t, err)
	require.Empty(t, resp.Tags)
	require.NotNil(t, gotCreated)
	require.True(t, gotCreated.Equal(ts))
}

func TestListTags_InvalidLastId(t *testing.T) {
	srv, _, _, _ := newTestTagServer(t)

	_, err := srv.ListTags(context.Background(), &pb.ListTagsRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		LastId:  strPtr("!!!"),
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetTagByName_Success(t *testing.T) {
	srv, perm, repo, _ := newTestTagServer(t)
	projectID := snow.ID(42)
	now := time.Now().Truncate(time.Second)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), projectID, "v1.0.0").
		Return(&domain.Tag{
			ID: 99, ProjectID: projectID, Name: "v1.0.0", CommitID: 11, UserID: 7,
			CreatedAt: now, UpdatedAt: now,
		}, nil)

	resp, err := srv.GetTagByName(context.Background(), &pb.GetTagByNameRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "v1.0.0",
	})
	require.NoError(t, err)
	require.Equal(t, "v1.0.0", resp.Tag.Name)
	require.Equal(t, snow.ID(11).Base36(), resp.Tag.CommitId)
}

func TestGetTagByName_NotFound(t *testing.T) {
	srv, perm, repo, _ := newTestTagServer(t)
	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), projectID, "missing").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetTagByName(context.Background(), &pb.GetTagByNameRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "missing",
	})
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), `tag "missing" not found`)
}

func TestCreateTag_FromBranch(t *testing.T) {
	srv, perm, repo, branchRepo := newTestTagServer(t)
	ctx := tagClaimCtx()
	projectID := snow.ID(42)
	now := time.Now().Truncate(time.Second)
	commitID := snow.ID(11)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), projectID, "v1.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		branchRepo.EXPECT().
			GetBranchByName(gomock.Any(), projectID, "release").
			Return(&domain.Branch{ID: 3, ProjectID: projectID, Name: "release", CommitID: &commitID}, nil),
	)

	var captured domain.Tag
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			captured = tag
			tag.CreatedAt = now
			tag.UpdatedAt = now
			return &tag, nil
		})

	resp, err := srv.CreateTag(ctx, &pb.CreateTagRequest{
		Context:    &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:       "v1.0.0",
		FromBranch: "release",
		Message:    "first release",
	})
	require.NoError(t, err)
	require.Equal(t, "v1.0.0", resp.Tag.Name)
	require.Equal(t, commitID.Base36(), resp.Tag.CommitId)
	require.Equal(t, "first release", resp.Tag.Message)
	require.Equal(t, commitID, captured.CommitID)
	require.Equal(t, "first release", captured.Message)
	require.Equal(t, snow.ID(7), captured.UserID)
}

func TestCreateTag_FromCommitID(t *testing.T) {
	srv, perm, repo, branchRepo := newTestTagServer(t)
	ctx := tagClaimCtx()
	projectID := snow.ID(42)
	commitID := snow.ID(11)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), projectID, "v2.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		branchRepo.EXPECT().
			GetCommit(gomock.Any(), commitID).
			Return(&domain.Commit{ID: commitID, ProjectID: projectID}, nil),
	)

	var captured domain.Tag
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			captured = tag
			return &tag, nil
		})

	resp, err := srv.CreateTag(ctx, &pb.CreateTagRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:         "v2.0.0",
		FromCommitId: commitID.Base36(),
	})
	require.NoError(t, err)
	require.Equal(t, "v2.0.0", resp.Tag.Name)
	require.Equal(t, commitID, captured.CommitID)
}

func TestCreateTag_FromCommitHash(t *testing.T) {
	srv, perm, repo, branchRepo := newTestTagServer(t)
	ctx := tagClaimCtx()
	projectID := snow.ID(42)
	commitID := snow.ID(11)
	hashHex := strings.Repeat("ab", 32)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	hash, err := domain.ParseHashHex(hashHex)
	require.NoError(t, err)

	gomock.InOrder(
		repo.EXPECT().
			GetTagByName(gomock.Any(), projectID, "v3.0.0").
			Return(nil, domain.NewErrorRecordNotFound()),
		branchRepo.EXPECT().
			GetCommitByHash(gomock.Any(), hash).
			Return(&domain.Commit{ID: commitID, ProjectID: projectID}, nil),
	)

	var captured domain.Tag
	repo.EXPECT().
		CreateTag(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, tag domain.Tag) (*domain.Tag, error) {
			captured = tag
			return &tag, nil
		})

	resp, err := srv.CreateTag(ctx, &pb.CreateTagRequest{
		Context:        &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:           "v3.0.0",
		FromCommitHash: hashHex,
	})
	require.NoError(t, err)
	require.Equal(t, "v3.0.0", resp.Tag.Name)
	require.Equal(t, commitID, captured.CommitID)
}

func TestCreateTag_InvalidCommitId(t *testing.T) {
	srv, _, _, _ := newTestTagServer(t)

	_, err := srv.CreateTag(context.Background(), &pb.CreateTagRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:         "v1.0.0",
		FromCommitId: "!!!",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateTag_InvalidCommitHash(t *testing.T) {
	srv, _, _, _ := newTestTagServer(t)

	_, err := srv.CreateTag(context.Background(), &pb.CreateTagRequest{
		Context:        &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:           "v1.0.0",
		FromCommitHash: "not-hex",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestCreateTag_NoPermission(t *testing.T) {
	srv, perm, _, _ := newTestTagServer(t)
	ctx := tagClaimCtx()
	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(false)

	_, err := srv.CreateTag(ctx, &pb.CreateTagRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "v1.0.0",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestCreateTag_AlreadyExists(t *testing.T) {
	srv, perm, repo, _ := newTestTagServer(t)
	ctx := tagClaimCtx()
	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), projectID, "v1.0.0").
		Return(&domain.Tag{ID: 99, ProjectID: projectID, Name: "v1.0.0"}, nil)

	_, err := srv.CreateTag(ctx, &pb.CreateTagRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "v1.0.0",
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestDeleteTag_Success(t *testing.T) {
	srv, perm, repo, _ := newTestTagServer(t)
	ctx := tagClaimCtx()
	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), projectID, "v1.0.0").
		Return(&domain.Tag{ID: 99, ProjectID: projectID, Name: "v1.0.0"}, nil)
	repo.EXPECT().
		DeleteTag(gomock.Any(), projectID, snow.ID(99)).
		Return(nil)

	_, err := srv.DeleteTag(ctx, &pb.DeleteTagRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "v1.0.0",
	})
	require.NoError(t, err)
}

func TestDeleteTag_NotFound(t *testing.T) {
	srv, perm, repo, _ := newTestTagServer(t)
	ctx := tagClaimCtx()
	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionWrite).
		Return(true)

	repo.EXPECT().
		GetTagByName(gomock.Any(), projectID, "missing").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.DeleteTag(ctx, &pb.DeleteTagRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Name:    "missing",
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}
