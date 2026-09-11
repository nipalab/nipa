package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treehash"
	"github.com/nipalab/nipa/internal/usecase"
)

func newPushUsecase(t *testing.T) (*usecase.Push, *MockpermissionUsecase, *MockbranchRepository, *MockpushRepository) {
	t.Helper()
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	repo := NewMockbranchRepository(ctrl)
	pushRepo := NewMockpushRepository(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return usecase.NewPush(perm, repo, pushRepo, node), perm, repo, pushRepo
}

func TestPushHandler_EmptyMessage(t *testing.T) {
	_, _, _, _ = newPushUsecase(t)
	srv := New(&mockUsecaseContainer{common: newTestCommon()})
	_, err := srv.Push(context.Background(), &pb.PushRequest{})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestPushHandler_InvalidHash(t *testing.T) {
	push, _, _, _ := newPushUsecase(t)
	srv := New(&mockUsecaseContainer{common: newTestCommon(), push: push})
	_, err := srv.Push(context.Background(), &pb.PushRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Message: "msg",
		Files:   []*pb.PushFile{{Path: "a.txt", FileHash: "zz-not-hex"}},
	})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestPushHandler_ConflictIsFailedPrecondition(t *testing.T) {
	push, perm, repo, _ := newPushUsecase(t)
	pushCtx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
	srv := New(&mockUsecaseContainer{common: newTestCommon(), push: push})

	commitID := snow.ID(9)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 42, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(9)).
		Return(&domain.Commit{ID: 9, TreeID: 100}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Hash: domain.Hash{1}, Name: "root"}, nil)

	_, err := srv.Push(pushCtx, &pb.PushRequest{
		Context:      &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:       "main",
		BaseTreeHash: "stale",
		Message:      "msg",
	})
	require.Error(t, err)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestPushHandler_Success(t *testing.T) {
	push, perm, repo, pushRepo := newPushUsecase(t)
	pushCtx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: snow.ID(7)})
	srv := New(&mockUsecaseContainer{common: newTestCommon(), push: push})

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(42), domain.PermissionWrite).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(42), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 42, Name: "main"}, nil)

	ch := chunker.Sum([]byte("hello push"))
	fh := treehash.FileHash([]domain.Hash{ch})
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil)

	resp, err := srv.Push(pushCtx, &pb.PushRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:  "main",
		Message: "add hello",
		Files: []*pb.PushFile{
			{
				Path:        "hello.txt",
				Mode:        pb.FileMode_FILE_MODE_READ_WRITE,
				SizeBytes:   10,
				FileHash:    fh.String(),
				ChunkHashes: []string{ch.String()},
			},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.CommitId)
	require.NotEmpty(t, resp.CommitHash)
	require.NotEmpty(t, resp.TreeHash)
}
