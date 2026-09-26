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

func newTestMergeRequest(t *testing.T) (*MergeRequest, *MockmergeRequestRepository, *MockbranchRepository, *MockpermissionUsecase, *MockbranchMerger) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockmergeRequestRepository(ctrl)
	branchRepo := NewMockbranchRepository(ctrl)
	perm := NewMockpermissionUsecase(ctrl)
	merger := NewMockbranchMerger(ctrl)
	node, err := snow.NewNode(1)
	require.NoError(t, err)
	return NewMergeRequest(repo, branchRepo, perm, merger, node), repo, branchRepo, perm, merger
}

func openMergeRequest() *domain.MergeRequest {
	return &domain.MergeRequest{
		ID: 5, Number: 5, ProjectID: 1, SourceBranchID: 3, TargetBranchID: 2,
		SourceBranch: "feature", TargetBranch: "main", Status: domain.MergeRequestOpen, CreatedBy: 7,
	}
}

func branchWithHead(id snow.ID, head snow.ID) *domain.Branch {
	return &domain.Branch{ID: id, ProjectID: 1, CommitID: &head}
}

func TestMergeRequest_Create(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	ctx := permissionCtx(7)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, created domain.MergeRequest) (*domain.MergeRequest, error) {
			require.NotZero(t, created.ID)
			require.Equal(t, "Feature", created.Title)
			require.Equal(t, "feature", created.SourceBranch)
			require.Equal(t, "main", created.TargetBranch)
			require.Equal(t, snow.ID(7), created.CreatedBy)
			require.Equal(t, domain.MergeRequestOpen, created.Status)
			require.Equal(t, &targetHead, created.MergeBaseCommitID)
			return &created, nil
		},
	)

	created, err := mr.Create(ctx, snow.ID(1), " Feature ", " body ", "feature", "main")
	require.NoError(t, err)
	require.Equal(t, "Feature", created.Title)
}

func TestMergeRequest_Create_Validation(t *testing.T) {
	mr, _, branchRepo, perm, _ := newTestMergeRequest(t)
	ctx := permissionCtx(7)
	head := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &head}, nil).AnyTimes()

	_, err := mr.Create(ctx, snow.ID(1), "  ", "", "feature", "main")
	requireUserError(t, err)

	_, err = mr.Create(ctx, snow.ID(1), "t", "", "main", "main")
	requireUserError(t, err)

	_, err = mr.Create(ctx, snow.ID(1), "t", "", "", "main")
	requireUserError(t, err)

	branchRepo2 := NewMockbranchRepository(gomock.NewController(t))
	branchRepo2.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(&domain.Branch{ID: 3, ProjectID: 1}, nil)
	mr2 := NewMergeRequest(NewMockmergeRequestRepository(gomock.NewController(t)), branchRepo2,
		perm, NewMockbranchMerger(gomock.NewController(t)), newTestBranchNode(t))
	_, err = mr2.Create(permissionCtx(7), snow.ID(1), "t", "", "feature", "main")
	requireUserError(t, err)
}

func TestMergeRequest_Create_MissingBranch(t *testing.T) {
	mr, _, branchRepo, perm, _ := newTestMergeRequest(t)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "ghost").Return(nil, domain.NewErrorRecordNotFound())

	_, err := mr.Create(permissionCtx(7), snow.ID(1), "t", "", "ghost", "main")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestMergeRequest_List(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()

	repo.EXPECT().List(gomock.Any(), snow.ID(1), "", 50).Return([]*domain.MergeRequest{openMergeRequest()}, nil)
	got, err := mr.List(permissionCtx(7), snow.ID(1), "", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)

	repo.EXPECT().List(gomock.Any(), snow.ID(1), domain.MergeRequestClosed, 10).Return(nil, nil)
	_, err = mr.List(permissionCtx(7), snow.ID(1), "closed", 10)
	require.NoError(t, err)

	_, err = mr.List(permissionCtx(7), snow.ID(1), "bogus", 10)
	requireUserError(t, err)
}

func TestMergeRequest_Get_NotFound(t *testing.T) {
	mr, repo, _, perm, _ := newTestMergeRequest(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(nil, domain.NewErrorRecordNotFound())

	_, err := mr.Get(permissionCtx(7), snow.ID(1), 5)
	require.True(t, domain.IsErrorNotFound(err))
	require.Contains(t, err.Error(), "merge request #5 not found")
}

func TestMergeRequest_Check_Mergeable(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
		Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)

	info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, domain.MergeabilityMergeable, info.Status)
	require.Equal(t, &sourceHead, info.SourceCommitID)
	require.Equal(t, &targetHead, info.TargetCommitID)
}

func TestMergeRequest_Check_States(t *testing.T) {
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	t.Run("behind target", func(t *testing.T) {
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		base := snow.ID(9)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&MergeBaseInfo{MergeBaseCommitID: &base}, nil)

		info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, domain.MergeabilityBehind, info.Status)
	})

	t.Run("up to date", func(t *testing.T) {
		mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, sourceHead), nil)

		info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, domain.MergeabilityUpToDate, info.Status)
	})

	t.Run("missing branch is invalid", func(t *testing.T) {
		mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, domain.NewErrorRecordNotFound())

		info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, domain.MergeabilityInvalid, info.Status)
	})

	t.Run("recreated branch is invalid", func(t *testing.T) {
		mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(30, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)

		info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, domain.MergeabilityInvalid, info.Status, "a branch recreated under the same name has a new id and must not be mergeable")
	})

	t.Run("terminal stays terminal", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		merged := openMergeRequest()
		merged.Status = domain.MergeRequestMerged
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(merged, nil)

		info, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, domain.MergeRequestMerged, info.Status)
	})
}

func TestMergeRequest_Merge(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil).Times(2)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
	merger.EXPECT().FastForwardForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature").
		Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}, nil)
	repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestMerged, &sourceHead).Return(nil)

	merged, _, err := mr.Merge(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Equal(t, openMergeRequest().ID, merged.ID)
}

func TestMergeRequest_Merge_BehindTarget(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)
	base := snow.ID(9)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(&MergeBaseInfo{MergeBaseCommitID: &base}, nil)

	_, info, err := mr.Merge(permissionCtx(7), snow.ID(1), 5)
	require.True(t, domain.IsErrorConflict(err))
	require.Equal(t, domain.MergeabilityBehind, info.Status)
}

func TestMergeRequest_Merge_RecreatedSourceBranchRefused(t *testing.T) {
	mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(30, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)

	_, info, err := mr.Merge(permissionCtx(7), snow.ID(1), 5)
	require.True(t, domain.IsErrorConflict(err))
	require.Equal(t, domain.MergeabilityInvalid, info.Status)
}

func TestMergeRequest_CloseAndReopen(t *testing.T) {
	t.Run("author can close and reopen", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).AnyTimes()

		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestClosed, nil).Return(nil)
		closed := openMergeRequest()
		closed.Status = domain.MergeRequestClosed
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)

		got, err := mr.Close(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
		require.Equal(t, domain.MergeRequestClosed, got.Status)

		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)
		repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestOpen, nil).Return(nil)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		_, err = mr.Reopen(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
	})

	t.Run("project admin can close", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		repo.EXPECT().UpdateStatus(gomock.Any(), snow.ID(1), int64(5), domain.MergeRequestClosed, nil).Return(nil)
		closed := openMergeRequest()
		closed.Status = domain.MergeRequestClosed
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)

		_, err := mr.Close(permissionCtx(99), snow.ID(1), 5)
		require.NoError(t, err)
	})

	t.Run("other users are denied", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)

		_, err := mr.Close(permissionCtx(99), snow.ID(1), 5)
		require.True(t, domain.IsErrorNoPermission(err))
	})
}

func TestMergeRequest_Update(t *testing.T) {
	t.Run("author can update title and description", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		repo.EXPECT().Update(gomock.Any(), snow.ID(1), int64(5), "New title", "New body").DoAndReturn(
			func(_ context.Context, _ snow.ID, _ int64, title, description string) (*domain.MergeRequest, error) {
				updated := openMergeRequest()
				updated.Title = title
				updated.Description = description
				return updated, nil
			},
		)

		got, err := mr.Update(permissionCtx(7), snow.ID(1), 5, " New title ", " New body ")
		require.NoError(t, err)
		require.Equal(t, "New title", got.Title)
		require.Equal(t, "New body", got.Description)
	})

	t.Run("description only keeps the existing title", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		existing := openMergeRequest()
		existing.Title = "Original"
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(existing, nil)
		repo.EXPECT().Update(gomock.Any(), snow.ID(1), int64(5), "Original", "Body").Return(existing, nil)

		_, err := mr.Update(permissionCtx(7), snow.ID(1), 5, "", "Body")
		require.NoError(t, err)
	})

	t.Run("project admin can update", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		repo.EXPECT().Update(gomock.Any(), snow.ID(1), int64(5), "T", "D").Return(openMergeRequest(), nil)

		_, err := mr.Update(permissionCtx(99), snow.ID(1), 5, "T", "D")
		require.NoError(t, err)
	})

	t.Run("other users are denied", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().AdminHasProject(gomock.Any(), snow.ID(1)).Return(false)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)

		_, err := mr.Update(permissionCtx(99), snow.ID(1), 5, "T", "")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("closed request is a conflict", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		closed := openMergeRequest()
		closed.Status = domain.MergeRequestClosed
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(closed, nil)

		_, err := mr.Update(permissionCtx(7), snow.ID(1), 5, "T", "")
		require.True(t, domain.IsErrorConflict(err))
	})

	t.Run("nothing to update", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)

		_, err := mr.Update(permissionCtx(7), snow.ID(1), 5, "  ", "")
		requireUserError(t, err)
	})

	t.Run("repository error propagates", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		repo.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, wantErr)

		_, err := mr.Update(permissionCtx(7), snow.ID(1), 5, "T", "")
		require.ErrorIs(t, err, wantErr)
	})
}

func TestMergeRequest_Diff(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)
	base := snow.ID(9)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &base}, nil)
	merger.EXPECT().TreeDiffBetween(gomock.Any(), snow.ID(1), &base, sourceHead).Return(nil, nil)

	files, err := mr.Diff(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestMergeRequest_Commits(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	mid := snow.ID(10)
	base := snow.ID(9)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(&MergeBaseInfo{MergeBaseCommitID: &base}, nil)
	branchRepo.EXPECT().CommitLogUntil(gomock.Any(), snow.ID(1), sourceHead, base, mergeRequestCommitLimit).
		Return([]*domain.CommitLogEntry{
			{Commit: domain.Commit{ID: sourceHead, Message: "second"}, AuthorName: "Alice"},
			{Commit: domain.Commit{ID: mid, Message: "first"}, AuthorName: "Bob"},
		}, nil)

	commits, err := mr.Commits(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Len(t, commits, 2, "the merge base commit is not part of the request")
	require.Equal(t, sourceHead, commits[0].ID, "newest first")
	require.Equal(t, "Alice", commits[0].AuthorName)
	require.Equal(t, mid, commits[1].ID)
}

func TestMergeRequest_Commits_WithoutTarget(t *testing.T) {
	mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
	sourceHead := snow.ID(11)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, domain.NewErrorRecordNotFound())
	branchRepo.EXPECT().CommitLogUntil(gomock.Any(), snow.ID(1), sourceHead, snow.ID(0), mergeRequestCommitLimit).
		Return([]*domain.CommitLogEntry{{Commit: domain.Commit{ID: sourceHead, Message: "only"}}}, nil)

	commits, err := mr.Commits(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Len(t, commits, 1, "a target that no longer exists logs the whole source branch")
	require.Equal(t, sourceHead, commits[0].ID)
}

func TestMergeRequest_Commits_MergeBaseError(t *testing.T) {
	mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
	sourceHead := snow.ID(11)
	targetHead := snow.ID(12)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
	merger.EXPECT().GetMergeBase(gomock.Any(), snow.ID(1), MergeRef{CommitID: &targetHead}, MergeRef{CommitID: &sourceHead}).
		Return(nil, errors.New("boom"))

	_, err := mr.Commits(permissionCtx(7), snow.ID(1), 5)
	require.Error(t, err)
}

func TestMergeRequest_Commits_SourceBranchMissing(t *testing.T) {
	mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, domain.NewErrorRecordNotFound())

	_, err := mr.Commits(permissionCtx(7), snow.ID(1), 5)
	require.True(t, domain.IsErrorNotFound(err))
}

func TestMergeRequest_Commits_EmptySource(t *testing.T) {
	mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)

	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
	branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(&domain.Branch{ID: 3, ProjectID: 1}, nil)

	commits, err := mr.Commits(permissionCtx(7), snow.ID(1), 5)
	require.NoError(t, err)
	require.Empty(t, commits)
}

func TestMergeRequest_ErrorPaths(t *testing.T) {
	t.Run("create needs a claim and read access", func(t *testing.T) {
		mr, _, _, perm, _ := newTestMergeRequest(t)
		_, err := mr.Create(context.Background(), snow.ID(1), "t", "", "feature", "main")
		require.True(t, domain.IsErrorNoPermission(err))

		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err = mr.Create(permissionCtx(7), snow.ID(1), "t", "", "feature", "main")
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("create target branch missing", func(t *testing.T) {
		mr, _, branchRepo, perm, _ := newTestMergeRequest(t)
		head := snow.ID(11)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &head}, nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "ghost").Return(nil, domain.NewErrorRecordNotFound())

		_, err := mr.Create(permissionCtx(7), snow.ID(1), "t", "", "feature", "ghost")
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("create repository error", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").
			Return(&domain.Branch{ID: 3, ProjectID: 1, CommitID: &sourceHead}, nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
			Return(&domain.Branch{ID: 2, ProjectID: 1, CommitID: &targetHead}, nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&MergeBaseInfo{}, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, wantErr)

		_, err := mr.Create(permissionCtx(7), snow.ID(1), "t", "", "feature", "main")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("merge propagates fast-forward errors", func(t *testing.T) {
		wantErr := errors.New("branch has moved")
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
		merger.EXPECT().FastForwardForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature").Return(nil, wantErr)

		_, _, err := mr.Merge(permissionCtx(7), snow.ID(1), 5)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("merge update status error", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
		merger.EXPECT().FastForwardForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature").
			Return(&domain.Branch{ID: 2, ProjectID: 1, Name: "main", CommitID: &sourceHead}, nil)
		repo.EXPECT().UpdateStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(wantErr)

		_, _, err := mr.Merge(permissionCtx(7), snow.ID(1), 5)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("diff missing source branch", func(t *testing.T) {
		mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, domain.NewErrorRecordNotFound())

		_, err := mr.Diff(permissionCtx(7), snow.ID(1), 5)
		require.True(t, domain.IsErrorNotFound(err))
	})

	t.Run("diff without target branch", func(t *testing.T) {
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(nil, domain.NewErrorRecordNotFound())
		merger.EXPECT().TreeDiffBetween(gomock.Any(), snow.ID(1), nil, sourceHead).Return(nil, nil)

		_, err := mr.Diff(permissionCtx(7), snow.ID(1), 5)
		require.NoError(t, err)
	})

	t.Run("list needs read access", func(t *testing.T) {
		mr, _, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := mr.List(permissionCtx(7), snow.ID(1), "", 0)
		require.True(t, domain.IsErrorNoPermission(err))
	})
}

func TestMergeRequest_MoreErrorPaths(t *testing.T) {
	t.Run("get needs read access", func(t *testing.T) {
		mr, _, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		_, err := mr.Get(permissionCtx(7), snow.ID(1), 5)
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("invalid number", func(t *testing.T) {
		mr, _, _, _, _ := newTestMergeRequest(t)
		_, err := mr.Get(permissionCtx(7), snow.ID(1), 0)
		requireUserError(t, err)
	})

	t.Run("create source repository error", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, _, branchRepo, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, wantErr)

		_, err := mr.Create(permissionCtx(7), snow.ID(1), "t", "", "feature", "main")
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("check merge base error", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, wantErr)

		_, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("check source repository error", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, repo, branchRepo, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(nil, wantErr)

		_, err := mr.Check(permissionCtx(7), snow.ID(1), 5)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("merge propagates protected target denial", func(t *testing.T) {
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(&MergeBaseInfo{MergeBaseCommitID: &targetHead}, nil)
		merger.EXPECT().FastForwardForMergeRequest(gomock.Any(), snow.ID(1), "main", "feature").
			Return(nil, domain.NewErrorNoPermission())

		_, _, err := mr.Merge(permissionCtx(7), snow.ID(1), 5)
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("merge a merged request is a conflict", func(t *testing.T) {
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		merged := openMergeRequest()
		merged.Status = domain.MergeRequestMerged
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(merged, nil)

		_, info, err := mr.Merge(permissionCtx(7), snow.ID(1), 5)
		require.True(t, domain.IsErrorConflict(err))
		require.Equal(t, domain.MergeRequestMerged, info.Status)
	})

	t.Run("diff merge base error", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, repo, branchRepo, perm, merger := newTestMergeRequest(t)
		sourceHead := snow.ID(11)
		targetHead := snow.ID(12)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "feature").Return(branchWithHead(3, sourceHead), nil)
		branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchWithHead(2, targetHead), nil)
		merger.EXPECT().GetMergeBase(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, wantErr)

		_, err := mr.Diff(permissionCtx(7), snow.ID(1), 5)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("close update error", func(t *testing.T) {
		wantErr := errors.New("db down")
		mr, repo, _, perm, _ := newTestMergeRequest(t)
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		repo.EXPECT().Get(gomock.Any(), snow.ID(1), int64(5)).Return(openMergeRequest(), nil)
		repo.EXPECT().UpdateStatus(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(wantErr)

		_, err := mr.Close(permissionCtx(7), snow.ID(1), 5)
		require.ErrorIs(t, err, wantErr)
	})

	t.Run("no claims at all", func(t *testing.T) {
		mr, _, _, _, _ := newTestMergeRequest(t)
		_, err := mr.Create(context.Background(), snow.ID(1), "t", "", "feature", "main")
		require.True(t, domain.IsErrorNoPermission(err))
	})
}
