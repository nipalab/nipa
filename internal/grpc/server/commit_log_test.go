package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
)

func TestGetCommitLog_Success(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(99)
	startID := snow.ID(88)
	now := time.Now().UTC().Truncate(time.Microsecond)
	var hash domain.Hash
	hash[0] = 0xaa

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		CommitLog(gomock.Any(), projectID, startID, 10).
		Return([]*domain.CommitLogEntry{
			{
				Commit:      domain.Commit{ID: 99, Hash: hash, Message: "latest", CreatedAt: now, Parent1ID: &startID},
				AuthorName:  "Alice",
				AuthorEmail: "alice@example.com",
			},
		}, nil)

	startStr := startID.Base36()
	resp, err := srv.GetCommitLog(context.Background(), &pb.GetCommitLogRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:        "main",
		StartCommitId: &startStr,
		Limit:         10,
	})
	require.NoError(t, err)
	require.Len(t, resp.Commits, 1)

	c := resp.Commits[0]
	require.Equal(t, commitID.Base36(), c.CommitId)
	require.Equal(t, hash.String(), c.CommitHash)
	require.Equal(t, startID.Base36(), c.GetParent_1Id())
	require.Equal(t, "Alice", c.AuthorName)
	require.Equal(t, "alice@example.com", c.AuthorEmail)
	require.Equal(t, "latest", c.Message)
	require.Equal(t, now, c.CreatedAt.AsTime())
}

func TestGetCommitLog_NoStartCommit(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetDefaultBranch(gomock.Any(), projectID).
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		CommitLog(gomock.Any(), projectID, commitID, 50).
		Return([]*domain.CommitLogEntry{}, nil)

	resp, err := srv.GetCommitLog(context.Background(), &pb.GetCommitLogRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
	})
	require.NoError(t, err)
	require.Empty(t, resp.Commits)
	require.Equal(t, "", resp.Branch)
}

func TestGetCommitLog_ResolveError(t *testing.T) {
	srv := New(newMockUsecaseContainer(t, nil))

	_, err := srv.GetCommitLog(context.Background(), &pb.GetCommitLogRequest{
		Context: &pb.ProjectContext{Org: "unknown", Project: "unknown"},
	})
	require.Error(t, err)
}

func TestGetCommitLog_InvalidStartCommitID(t *testing.T) {
	branch, _, _ := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	invalidID := "!!!invalid!!!"
	_, err := srv.GetCommitLog(context.Background(), &pb.GetCommitLogRequest{
		Context:       &pb.ProjectContext{Org: "org", Project: "proj"},
		StartCommitId: &invalidID,
	})
	require.Error(t, err)
}

func TestGetCommitLog_UsecaseError(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "nope").
		Return(nil, domain.NewErrorRecordNotFound())

	_, err := srv.GetCommitLog(context.Background(), &pb.GetCommitLogRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:  "nope",
	})
	require.Error(t, err)
}

func TestGetCommitLog_EmptyEntry(t *testing.T) {
	branch, perm, repo := newTestBranchUc(t)
	srv := New(newMockUsecaseContainer(t, branch))

	projectID := snow.ID(42)
	commitID := snow.ID(99)

	perm.EXPECT().
		HasProjectAccess(gomock.Any(), projectID, domain.PermissionRead).
		Return(true)
	repo.EXPECT().
		GetBranchByName(gomock.Any(), projectID, "main").
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", CommitID: &commitID}, nil)
	repo.EXPECT().
		CommitLog(gomock.Any(), projectID, commitID, 10).
		Return(nil, nil)

	resp, err := srv.GetCommitLog(context.Background(), &pb.GetCommitLogRequest{
		Context: &pb.ProjectContext{Org: "org", Project: "proj"},
		Branch:  "main",
		Limit:   10,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Commits)
}

func TestCommitLogEntryToPB_RootCommit(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	var hash domain.Hash
	hash[0] = 0xbb
	entry := &domain.CommitLogEntry{
		Commit: domain.Commit{
			ID:        snow.ID(1),
			Hash:      hash,
			Message:   "root",
			CreatedAt: now,
		},
		AuthorName:  "Root",
		AuthorEmail: "",
	}
	c := commitLogEntryToPB(entry)
	require.Equal(t, snow.ID(1).Base36(), c.CommitId)
	require.Equal(t, hash.String(), c.CommitHash)
	require.Nil(t, c.Parent_1Id)
	require.Nil(t, c.Parent_2Id)
	require.Equal(t, timestamppb.New(now), c.CreatedAt)
}
