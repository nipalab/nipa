package sqlite

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

type allowAllPermission struct{}

func (allowAllPermission) HasProjectAccess(context.Context, snow.ID, domain.Permission) bool {
	return true
}

func (allowAllPermission) HasPathAccess(context.Context, snow.ID, string, domain.Permission) bool {
	return true
}

func (allowAllPermission) CompileFilter(context.Context, snow.ID, domain.Permission) (*usecase.PathFilter, error) {
	return usecase.AllowAllFilter(), nil
}

func (allowAllPermission) AdminHasProject(context.Context, snow.ID) bool { return true }

type countingBranchRepository struct {
	*BranchRepository
	getCalls int
}

func (r *countingBranchRepository) GetCommit(ctx context.Context, id snow.ID) (*domain.Commit, error) {
	r.getCalls++
	return r.BranchRepository.GetCommit(ctx, id)
}

func TestCommitWalkersUseBatchedAncestors(t *testing.T) {
	ctx := context.Background()
	db, q := newSQLiteTestDB(t)
	projectID := seedProject(t, q, 1, "test-project")
	otherID := seedProject(t, q, 1, "other-project")
	parents := linearGraph(3000)
	parents = append(parents, [2]snow.ID{3000}, [2]snow.ID{3000}, [2]snow.ID{3001, 3002})
	ids := seedGraph(t, db, projectID, parents...)
	foreign := seedGraph(t, db, otherID, linearGraph(2)...)

	repo := &countingBranchRepository{BranchRepository: NewBranchRepository(db)}
	uc := usecase.NewBranch(allowAllPermission{}, repo, newTestNode(t))
	head := ids[len(ids)-1]

	got, err := uc.WalkCommits(ctx, projectID, head, nil, 10000)
	require.NoError(t, err)
	require.Len(t, got, len(ids))
	require.Equal(t, head, got[0].ID)
	require.Equal(t, ids[0], got[len(got)-1].ID)
	require.Zero(t, repo.getCalls)

	got, err = uc.WalkCommits(ctx, projectID, head, &ids[2999], 10)
	require.NoError(t, err)
	require.Len(t, got, 3)

	_, err = uc.WalkCommits(ctx, projectID, foreign[1], nil, 10)
	require.True(t, domain.IsErrorNotFound(err))

	ok, _, err := uc.Contains(ctx, projectID, ids[0], head.Base36())
	require.NoError(t, err)
	require.True(t, ok)
	ok, _, err = uc.Contains(ctx, projectID, ids[3000], ids[3001].Base36())
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 4, repo.getCalls)
}
