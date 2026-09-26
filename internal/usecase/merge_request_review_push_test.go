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

// emptyHeadTree wires the branch reads a single-file push into an empty head tree.
func emptyHeadTree(repo *MockbranchRepository) {
	head := snow.ID(9)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").
		Return(&domain.Branch{ID: 5, ProjectID: 1, Name: "main", CommitID: &head}, nil)
	repo.EXPECT().GetCommit(gomock.Any(), head).
		Return(&domain.Commit{ID: 9, ProjectID: 1, TreeID: 100, Hash: domain.Hash{5}}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(100)).
		Return(&domain.TreeNode{ID: 100, Name: "root"}, nil).AnyTimes()
	repo.EXPECT().ListFilesByTree(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	repo.EXPECT().ListTreeChildren(gomock.Any(), int64(100)).Return(nil, nil).AnyTimes()
	repo.EXPECT().GetCommitByHash(gomock.Any(), gomock.Any()).
		Return(nil, domain.NewErrorNotFound("no parent")).AnyTimes()
}

func newReviewPushFixture(t *testing.T) (*Push, *MockbranchRepository, *MockpushRepository, context.Context) {
	t.Helper()

	uc, perm, repo, pushRepo, ctx := newPushFixture(t)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionWrite).Return(true)
	emptyHeadTree(repo)
	return uc, repo, pushRepo, ctx
}

func TestPush_NotifiesReviewsAboutTheNewHead(t *testing.T) {
	uc, _, pushRepo, ctx := newReviewPushFixture(t)

	var applied ApplyPushRequest
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req ApplyPushRequest) error {
			applied = req
			return nil
		})
	reviews := NewMockreviewPushGate(gomock.NewController(t))
	reviews.EXPECT().NoteBranchPush(gomock.Any(), snow.ID(1), snow.ID(5), gomock.Any(), snow.ID(7), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, newHead, _ snow.ID, commitHash string) error {
			require.Equal(t, applied.CommitID, newHead, "reviews must be dismissed against the commit that landed")
			require.Equal(t, applied.CommitHash.String(), commitHash)
			return nil
		})

	ch, fh := chunkAndFileHash(t, "hello")
	_, err := uc.WithReviews(reviews).Push(ctx, snow.ID(1), "main", "", "msg",
		[]*domain.PushFile{{Path: "a.txt", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}},
		nil, "", snow.ID(9).Base36())
	require.NoError(t, err)
}

// a review bookkeeping failure must not turn a stored commit into a failed push
func TestPush_ReviewHookFailureIsNotFatal(t *testing.T) {
	uc, _, pushRepo, ctx := newReviewPushFixture(t)
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(nil)
	reviews := NewMockreviewPushGate(gomock.NewController(t))
	reviews.EXPECT().NoteBranchPush(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("database is locked"))

	ch, fh := chunkAndFileHash(t, "hello")
	result, err := uc.WithReviews(reviews).Push(ctx, snow.ID(1), "main", "", "msg",
		[]*domain.PushFile{{Path: "a.txt", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}},
		nil, "", snow.ID(9).Base36())
	require.NoError(t, err)
	require.NotZero(t, result.CommitID)
}

// a failed push must not dismiss anything
func TestPush_ReviewHookIsSkippedWhenThePushFails(t *testing.T) {
	uc, _, pushRepo, ctx := newReviewPushFixture(t)
	pushRepo.EXPECT().ApplyPush(gomock.Any(), gomock.Any()).Return(errors.New("storage down"))
	// no review expectation: gomock fails the test if NoteBranchPush is called

	ch, fh := chunkAndFileHash(t, "hello")
	_, err := uc.WithReviews(NewMockreviewPushGate(gomock.NewController(t))).Push(ctx, snow.ID(1), "main", "", "msg",
		[]*domain.PushFile{{Path: "a.txt", Mode: 0o644, SizeBytes: 5, FileHash: fh, ChunkHashes: []domain.Hash{ch}}},
		nil, "", snow.ID(9).Base36())
	require.Error(t, err)
}
