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

type containsFixture struct {
	uc         *Branch
	perm       *MockpermissionUsecase
	tags       *MocktagRepository
	branchRepo *MockbranchRepository
}

func newContainsFixture(t *testing.T) containsFixture {
	ctrl := gomock.NewController(t)
	f := containsFixture{
		perm:       newAllowAllPerm(ctrl),
		tags:       NewMocktagRepository(ctrl),
		branchRepo: NewMockbranchRepository(ctrl),
	}
	f.uc = NewBranch(f.perm, f.branchRepo, newTestBranchNode(t)).WithTags(f.tags)
	return f
}

// history: 10 <- 20 <- 30 (main), 20 <- 25 (feature), 40 merges 30 and 25.
func (f containsFixture) history() {
	commits := map[snow.ID]*domain.Commit{
		10: {ID: 10, ProjectID: 1},
		20: {ID: 20, ProjectID: 1, Parent1ID: idPtr(10)},
		25: {ID: 25, ProjectID: 1, Parent1ID: idPtr(20)},
		30: {ID: 30, ProjectID: 1, Parent1ID: idPtr(20)},
		40: {ID: 40, ProjectID: 1, Parent1ID: idPtr(30), Parent2ID: idPtr(25)},
		99: {ID: 99, ProjectID: 2},
	}
	f.branchRepo.EXPECT().GetCommit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id snow.ID) (*domain.Commit, error) {
			if c, ok := commits[id]; ok {
				return c, nil
			}
			return nil, domain.NewErrorRecordNotFound()
		}).AnyTimes()
}

func (f containsFixture) read(allowed bool) {
	f.perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(allowed)
}

func TestBranch_Contains_Tag(t *testing.T) {
	f := newContainsFixture(t)
	f.history()
	f.read(true)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v1").Return(&domain.Tag{CommitID: 40}, nil)

	ok, head, err := f.uc.Contains(context.Background(), 1, 25, "v1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, snow.ID(40), head)
}

func TestBranch_Contains_EqualAndNotContained(t *testing.T) {
	f := newContainsFixture(t)
	f.history()
	f.read(true)
	f.read(true)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "main").Return(nil, domain.NewErrorRecordNotFound()).Times(2)
	f.branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(&domain.Branch{CommitID: idPtr(30)}, nil).Times(2)

	ok, _, err := f.uc.Contains(context.Background(), 1, 30, "main")
	require.NoError(t, err)
	require.True(t, ok)

	ok, _, err = f.uc.Contains(context.Background(), 1, 25, "main")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestBranch_Contains_CommitIDAndHashRefs(t *testing.T) {
	f := newContainsFixture(t)
	f.history()
	f.read(true)
	f.read(true)
	f.read(true)
	ref40 := snow.ID(40).Base36()
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound()).Times(3)
	f.branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), gomock.Any()).Return(nil, domain.NewErrorRecordNotFound()).Times(3)

	ok, head, err := f.uc.Contains(context.Background(), 1, 10, ref40)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, snow.ID(40), head)

	var hash domain.Hash
	hash[0] = 7
	f.branchRepo.EXPECT().GetCommitByHash(gomock.Any(), snow.ID(1), hash).Return(&domain.Commit{ID: 20, ProjectID: 1}, nil)
	ok, head, err = f.uc.Contains(context.Background(), 1, 30, hash.String())
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, snow.ID(20), head)

	_, _, err = f.uc.Contains(context.Background(), 1, 10, "nope")
	require.True(t, domain.IsErrorNotFound(err))
}

func TestBranch_Contains_Errors(t *testing.T) {
	f := newContainsFixture(t)
	f.history()

	f.read(false)
	_, _, err := f.uc.Contains(context.Background(), 1, 10, "v1")
	require.True(t, domain.IsErrorNoPermission(err))

	f.read(true)
	_, _, err = f.uc.Contains(context.Background(), 1, 99, "v1")
	require.True(t, domain.IsErrorNotFound(err), "a commit of another project is not found")

	f.read(true)
	_, _, err = f.uc.Contains(context.Background(), 1, 777, "v1")
	require.True(t, domain.IsErrorNotFound(err))

	f.read(true)
	_, _, err = f.uc.Contains(context.Background(), 1, 10, "  ")
	require.Error(t, err)

	boom := errors.New("boom")
	f.read(true)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "x").Return(nil, boom)
	_, _, err = f.uc.Contains(context.Background(), 1, 10, "x")
	require.ErrorIs(t, err, boom)

	f.read(true)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "y").Return(nil, domain.NewErrorRecordNotFound())
	f.branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "y").Return(nil, boom)
	_, _, err = f.uc.Contains(context.Background(), 1, 10, "y")
	require.ErrorIs(t, err, boom)

	f.read(true)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "empty").Return(nil, domain.NewErrorRecordNotFound())
	f.branchRepo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "empty").Return(&domain.Branch{}, nil)
	ok, _, err := f.uc.Contains(context.Background(), 1, 10, "empty")
	require.NoError(t, err)
	require.False(t, ok, "an empty branch contains nothing")
}

func TestBranch_Contains_WalkError(t *testing.T) {
	f := newContainsFixture(t)
	boom := errors.New("boom")
	f.read(true)
	f.branchRepo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, ProjectID: 1}, nil)
	f.branchRepo.EXPECT().GetCommit(gomock.Any(), snow.ID(50)).Return(nil, boom)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v").Return(&domain.Tag{CommitID: 50}, nil)
	_, _, err := f.uc.Contains(context.Background(), 1, 10, "v")
	require.ErrorIs(t, err, boom)

	f.read(true)
	f.branchRepo.EXPECT().GetCommit(gomock.Any(), snow.ID(11)).Return(nil, boom)
	_, _, err = f.uc.Contains(context.Background(), 1, 11, "v")
	require.ErrorIs(t, err, boom)
}

func TestBranch_Contains_IgnoresIDOrder(t *testing.T) {
	f := newContainsFixture(t)
	commits := map[snow.ID]*domain.Commit{
		50: {ID: 50, ProjectID: 1},
		45: {ID: 45, ProjectID: 1, Parent1ID: idPtr(50)},
		60: {ID: 60, ProjectID: 1, Parent1ID: idPtr(45)},
	}
	f.branchRepo.EXPECT().GetCommit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id snow.ID) (*domain.Commit, error) { return commits[id], nil }).AnyTimes()
	f.read(true)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v").Return(&domain.Tag{CommitID: 60}, nil)

	ok, _, err := f.uc.Contains(context.Background(), 1, 50, "v")
	require.NoError(t, err)
	require.True(t, ok, "a child stored with a lower id than its parent is still walked")
}

func TestBranch_Contains_WalkCap(t *testing.T) {
	f := newContainsFixture(t)
	f.branchRepo.EXPECT().GetCommit(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, id snow.ID) (*domain.Commit, error) {
			if id == 1 {
				return &domain.Commit{ID: 1, ProjectID: 1}, nil
			}
			return &domain.Commit{ID: id, ProjectID: 1, Parent1ID: idPtr(id - 1)}, nil
		}).AnyTimes()
	f.read(true)
	f.tags.EXPECT().GetTagByName(gomock.Any(), snow.ID(1), "v").Return(&domain.Tag{CommitID: maxContainsWalk + 100}, nil)

	_, _, err := f.uc.Contains(context.Background(), 1, 1, "v")
	var domErr *domain.Error
	require.ErrorAs(t, err, &domErr)
	require.Equal(t, 400, domErr.Code)
}

func TestBranch_Contains_WithoutTags(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	repo := NewMockbranchRepository(ctrl)
	uc := NewBranch(perm, repo, newTestBranchNode(t))
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(10)).Return(&domain.Commit{ID: 10, ProjectID: 1}, nil)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(&domain.Branch{CommitID: idPtr(10)}, nil)

	ok, _, err := uc.Contains(context.Background(), 1, 10, "main")
	require.NoError(t, err)
	require.True(t, ok)
}
