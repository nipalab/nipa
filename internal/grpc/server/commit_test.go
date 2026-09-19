package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func TestGetCommit_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(7)
	parentID := snow.ID(3)
	now := time.Now().Truncate(time.Second)
	commitHash := domain.Hash{0x01, 0x02}
	rootHash := domain.Hash{0x0a, 0x0b}
	fileHash := domain.Hash{0x0c}

	perm.EXPECT().HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(&domain.Commit{
		ID:        commitID,
		ProjectID: projectID,
		TreeID:    5,
		Hash:      commitHash,
		Parent1ID: &parentID,
		Message:   "hello",
		CreatedAt: now,
	}, nil)
	gomock.InOrder(
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(5)).
			Return(&domain.TreeNode{ID: 5, Hash: rootHash}, nil),
		repo.EXPECT().ListFilesByTree(gomock.Any(), int64(5)).
			Return([]*domain.File{{ID: 1, Name: "a.txt", TreeID: 5, Hash: fileHash, SizeBytes: 3}}, nil),
		repo.EXPECT().ListTreeChildren(gomock.Any(), int64(5)).Return(nil, nil),
	)

	resp, err := srv.GetCommit(context.Background(), &pb.GetCommitRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitId: commitID.Base36(),
	})
	require.NoError(t, err)
	require.Equal(t, "7", resp.Commit.CommitId)
	require.Equal(t, commitHash.String(), resp.Commit.CommitHash)
	require.Equal(t, rootHash.String(), resp.Commit.TreeHash)
	require.Equal(t, "3", *resp.Commit.Parent_1Id)
	require.Nil(t, resp.Commit.Parent_2Id)
	require.Equal(t, "hello", resp.Commit.Message)
	require.True(t, now.Equal(resp.Commit.CreatedAt.AsTime()))
	require.Equal(t, rootHash.String(), resp.RootTree.TreeHash)
	require.Len(t, resp.RootTree.Files, 1)
	require.Equal(t, "a.txt", resp.RootTree.Files[0].Path)
}

func TestGetCommit_InvalidID(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	_, err := srv.GetCommit(context.Background(), &pb.GetCommitRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitId: "!!",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetCommit_NotFound(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(7)

	perm.EXPECT().HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), commitID).Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetCommit(context.Background(), &pb.GetCommitRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitId: commitID.Base36(),
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestWalkCommits_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	p1, p2, p3 := snow.ID(1), snow.ID(2), snow.ID(3)
	hash1 := domain.Hash{0x01}
	hash2 := domain.Hash{0x02}
	hash3 := domain.Hash{0x03}

	perm.EXPECT().HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), p1).Return(&domain.Commit{ID: p1, ProjectID: projectID, TreeID: 1, Hash: hash1, Message: "one"}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), p2).Return(&domain.Commit{ID: p2, ProjectID: projectID, TreeID: 2, Hash: hash2, Parent1ID: &p1, Message: "two"}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), p3).Return(&domain.Commit{ID: p3, ProjectID: projectID, TreeID: 3, Hash: hash3, Parent1ID: &p2, Message: "three"}, nil)

	resp, err := srv.WalkCommits(context.Background(), &pb.WalkCommitsRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		StartCommitId: p3.Base36(),
		Limit:         10,
	})
	require.NoError(t, err)
	require.Len(t, resp.Commits, 3)
	require.Equal(t, []string{"3", "2", "1"}, []string{
		resp.Commits[0].CommitId,
		resp.Commits[1].CommitId,
		resp.Commits[2].CommitId,
	})
	require.Equal(t, hash3.String(), resp.Commits[0].CommitHash)
	require.Equal(t, "2", *resp.Commits[0].Parent_1Id)
	require.Nil(t, resp.Commits[0].Parent_2Id)
	require.Equal(t, "three", resp.Commits[0].Message)
	require.Nil(t, resp.Commits[2].Parent_1Id)
}

func TestWalkCommits_WithStop(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	p3 := snow.ID(3)
	stop := snow.ID(2).Base36()

	perm.EXPECT().HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), p3).
		Return(&domain.Commit{ID: p3, ProjectID: projectID, TreeID: 3, Message: "three"}, nil)

	resp, err := srv.WalkCommits(context.Background(), &pb.WalkCommitsRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		StartCommitId: p3.Base36(),
		StopCommitId:  &stop,
		Limit:         10,
	})
	require.NoError(t, err)
	require.Len(t, resp.Commits, 1)
	require.Equal(t, "3", resp.Commits[0].CommitId)
}

func TestWalkCommits_InvalidStart(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	_, err := srv.WalkCommits(context.Background(), &pb.WalkCommitsRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		StartCommitId: "",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestWalkCommits_InvalidStop(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	bad := "!!"
	_, err := srv.WalkCommits(context.Background(), &pb.WalkCommitsRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		StartCommitId: "3",
		StopCommitId:  &bad,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestWalkCommits_NoPermission(t *testing.T) {
	branch, perm, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	perm.EXPECT().HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).Return(false)

	_, err := srv.WalkCommits(context.Background(), &pb.WalkCommitsRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		StartCommitId: "3",
	})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
