package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func commitTreeTestRepo(t *testing.T, repo *MockbranchRepository, projectID snow.ID, commit *domain.Commit, root *domain.TreeNode) {
	t.Helper()
	repo.EXPECT().
		GetTreeNode(gomock.Any(), commit.TreeID).
		Return(root, nil)
	repo.EXPECT().
		ListFilesByTree(gomock.Any(), commit.TreeID).
		Return([]*domain.File{{ID: 1, Name: "a.txt", Mode: 2}}, nil)
	repo.EXPECT().
		ListTreeChildren(gomock.Any(), commit.TreeID).
		Return(nil, nil)
}

func TestGetCommitTree_ByID(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commit := &domain.Commit{ID: snow.ID(99), Hash: domain.Hash{0x01}, ProjectID: projectID, TreeID: 7}
	root := &domain.TreeNode{ID: 7, Hash: domain.Hash{0x02}, Name: "root"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commit.ID).
		Return(commit, nil)
	commitTreeTestRepo(t, repo, projectID, commit, root)

	resp, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitId: commit.ID.Base36(),
	})
	require.NoError(t, err)
	require.Equal(t, commit.ID.Base36(), resp.CommitId)
	require.Equal(t, root.Hash.String(), resp.RootTree.TreeHash)
	require.Len(t, resp.RootTree.Files, 1)
	require.Equal(t, "a.txt", resp.RootTree.Files[0].Path)
}

func TestGetCommitTree_ByHash(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commit := &domain.Commit{ID: snow.ID(99), Hash: domain.Hash{0x01}, ProjectID: projectID, TreeID: 7}
	root := &domain.TreeNode{ID: 7, Hash: domain.Hash{0x02}, Name: "root"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommitByHash(gomock.Any(), commit.Hash).
		Return(commit, nil)
	commitTreeTestRepo(t, repo, projectID, commit, root)

	resp, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context:    &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitHash: commit.Hash.String(),
	})
	require.NoError(t, err)
	require.Equal(t, commit.ID.Base36(), resp.CommitId)
	require.Len(t, resp.RootTree.Files, 1)
}

func TestGetCommitTree_IDTakesPrecedence(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commit := &domain.Commit{ID: snow.ID(99), Hash: domain.Hash{0x01}, ProjectID: projectID, TreeID: 7}
	root := &domain.TreeNode{ID: 7, Hash: domain.Hash{0x02}, Name: "root"}

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commit.ID).
		Return(commit, nil)
	commitTreeTestRepo(t, repo, projectID, commit, root)

	// GetCommitByHash is not expected: the ID wins when both are set.
	resp, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context:    &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitId:   commit.ID.Base36(),
		CommitHash: domain.Hash{0x09}.String(),
	})
	require.NoError(t, err)
	require.Equal(t, commit.ID.Base36(), resp.CommitId)
}

func TestGetCommitTree_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context:  &pb.ProjectContext{Org: "unknown", Project: "unknown"},
		CommitId: snow.ID(99).Base36(),
	})
	require.Error(t, err)
}

func TestGetCommitTree_InvalidCommitID(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	_, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitId: "!!!invalid!!!",
	})
	require.Error(t, err)
}

func TestGetCommitTree_InvalidCommitHash(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	_, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context:    &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitHash: "zz",
	})
	require.Error(t, err)
}

func TestGetCommitTree_EmptyRef(t *testing.T) {
	branch, perm, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)

	_, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
	})
	require.Error(t, err)
}

func TestGetCommitTree_UsecaseError(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(99)
	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetCommit(gomock.Any(), commitID).
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetCommitTree(context.Background(), &pb.GetCommitTreeRequest{
		Context:  &pb.ProjectContext{Org: "org", Project: "proj"},
		CommitId: commitID.Base36(),
	})
	require.Error(t, err)
}
