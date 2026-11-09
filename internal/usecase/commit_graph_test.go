package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type mapCommitRepo struct {
	*MockbranchRepository
	commits  map[snow.ID]*domain.Commit
	getCalls int
}

func (r *mapCommitRepo) GetCommit(_ context.Context, id snow.ID) (*domain.Commit, error) {
	r.getCalls++
	c, ok := r.commits[id]
	if !ok {
		return nil, domain.NewErrorNotFound("commit not found")
	}
	return c, nil
}

type batchCommitRepo struct {
	*mapCommitRepo
	ancestorCalls int
	err           error
}

func (r *batchCommitRepo) CommitAncestors(_ context.Context, projectID, startID snow.ID, limit int) ([]*domain.Commit, error) {
	r.ancestorCalls++
	if r.err != nil {
		return nil, r.err
	}
	var out []*domain.Commit
	seen := map[snow.ID]bool{startID: true}
	queue := []snow.ID{startID}
	for len(queue) > 0 && len(out) < limit {
		id := queue[0]
		queue = queue[1:]
		c, ok := r.commits[id]
		if !ok || c.ProjectID != projectID {
			continue
		}
		out = append(out, c)
		for _, p := range commitParents(c) {
			if !seen[p] {
				seen[p] = true
				queue = append(queue, p)
			}
		}
	}
	return out, nil
}

type graphSpec map[snow.ID][]snow.ID

func buildCommits(project snow.ID, spec graphSpec) map[snow.ID]*domain.Commit {
	out := make(map[snow.ID]*domain.Commit, len(spec))
	for id, parents := range spec {
		c := &domain.Commit{ID: id, ProjectID: project}
		if len(parents) > 0 {
			c.Parent1ID = idPtr(parents[0])
		}
		if len(parents) > 1 {
			c.Parent2ID = idPtr(parents[1])
		}
		out[id] = c
	}
	return out
}

func chainSpec(spec graphSpec, from, n int) graphSpec {
	for i := from; i < from+n; i++ {
		if i == from {
			spec[snow.ID(i)] = nil
			continue
		}
		spec[snow.ID(i)] = []snow.ID{snow.ID(i - 1)}
	}
	return spec
}

func testGraph() map[snow.ID]*domain.Commit {
	spec := graphSpec{
		100: nil, 101: {100}, 102: {100}, 103: {101, 102}, 104: {103}, 105: {102},
		200: nil, 201: {200},
		300: {999},
	}
	chainSpec(spec, 1, 40)
	commits := buildCommits(1, spec)
	commits[400] = &domain.Commit{ID: 400, ProjectID: 2}
	return commits
}

type graphRepos struct {
	plain *mapCommitRepo
	batch *batchCommitRepo
}

func newGraphRepos(t *testing.T, commits map[snow.ID]*domain.Commit) graphRepos {
	ctrl := gomock.NewController(t)
	return graphRepos{
		plain: &mapCommitRepo{MockbranchRepository: NewMockbranchRepository(ctrl), commits: commits},
		batch: &batchCommitRepo{mapCommitRepo: &mapCommitRepo{MockbranchRepository: NewMockbranchRepository(ctrl), commits: commits}},
	}
}

func (g graphRepos) branches(t *testing.T) (*Branch, *Branch) {
	ctrl := gomock.NewController(t)
	perm := newAllowAllPerm(ctrl)
	perm.EXPECT().HasProjectAccess(gomock.Any(), gomock.Any(), gomock.Any()).Return(true).AnyTimes()
	return NewBranch(perm, g.plain, newTestBranchNode(t)), NewBranch(perm, g.batch, newTestBranchNode(t))
}

func errCode(err error) int {
	var domErr *domain.Error
	if errors.As(err, &domErr) {
		return domErr.Code
	}
	if err != nil {
		return -1
	}
	return 0
}

func TestCommitGraph_FindMergeBaseSameOnBothPaths(t *testing.T) {
	repos := newGraphRepos(t, testGraph())
	plain, batch := repos.branches(t)
	cases := [][2]snow.ID{
		{40, 1}, {1, 40}, {40, 20}, {104, 105}, {103, 105}, {104, 101}, {101, 102},
		{201, 104}, {201, 200}, {40, 104}, {300, 104}, {104, 300},
	}
	for _, c := range cases {
		a, b := c[0], c[1]
		want, wantErr := plain.findMergeBase(context.Background(), 1, &a, &b)
		got, gotErr := batch.findMergeBase(context.Background(), 1, &a, &b)
		require.Equal(t, errCode(wantErr), errCode(gotErr), "%v", c)
		require.Equal(t, want, got, "%v", c)
	}
	got, err := batch.findMergeBase(context.Background(), 1, nil, idPtr(1))
	require.NoError(t, err)
	require.Nil(t, got)
	require.Zero(t, repos.batch.getCalls)
}

func TestCommitGraph_IsAncestorSameOnBothPaths(t *testing.T) {
	repos := newGraphRepos(t, testGraph())
	plain, batch := repos.branches(t)
	cases := [][2]snow.ID{
		{1, 40}, {40, 1}, {20, 20}, {100, 104}, {102, 104}, {105, 104}, {200, 104},
		{1, 104}, {999, 300}, {1, 300},
	}
	for _, c := range cases {
		want, wantErr := plain.isAncestor(context.Background(), 1, c[0], c[1])
		got, gotErr := batch.isAncestor(context.Background(), 1, c[0], c[1])
		require.Equal(t, errCode(wantErr), errCode(gotErr), "%v", c)
		require.Equal(t, want, got, "%v", c)
	}
	require.Zero(t, repos.batch.getCalls)
}

func TestCommitGraph_WalkCommitsSameOnBothPaths(t *testing.T) {
	repos := newGraphRepos(t, testGraph())
	plain, batch := repos.branches(t)
	type walk struct {
		start snow.ID
		stop  *snow.ID
		limit int
	}
	cases := []walk{
		{40, nil, 0}, {40, nil, 100}, {40, idPtr(30), 100}, {40, idPtr(40), 5},
		{104, nil, 100}, {104, idPtr(102), 100}, {104, idPtr(101), 2}, {201, idPtr(104), 10},
		{300, nil, 10}, {400, nil, 10}, {777, nil, 10},
	}
	for _, c := range cases {
		want, wantErr := plain.WalkCommits(context.Background(), 1, c.start, c.stop, c.limit)
		got, gotErr := batch.WalkCommits(context.Background(), 1, c.start, c.stop, c.limit)
		require.Equal(t, wantErr, gotErr, "%+v", c)
		require.Equal(t, walkIDs(want), walkIDs(got), "%+v", c)
	}
	require.Zero(t, repos.batch.getCalls)
}

func TestCommitGraph_WalkCapSameOnBothPaths(t *testing.T) {
	old := maxCommitWalkNodes
	maxCommitWalkNodes = 10
	t.Cleanup(func() { maxCommitWalkNodes = old })
	repos := newGraphRepos(t, testGraph())
	plain, batch := repos.branches(t)
	_, wantErr := plain.WalkCommits(context.Background(), 1, 40, nil, 50)
	_, gotErr := batch.WalkCommits(context.Background(), 1, 40, nil, 50)
	require.Error(t, wantErr)
	require.Equal(t, wantErr, gotErr)
}

func TestCommitGraph_ContainsCapSameOnBothPaths(t *testing.T) {
	repos := newGraphRepos(t, buildCommits(1, chainSpec(graphSpec{}, 1, maxContainsWalk+100)))
	plain, batch := repos.branches(t)
	head := snow.ID(maxContainsWalk + 99)
	_, wantErr := plain.isAncestor(context.Background(), 1, 1, head)
	_, gotErr := batch.isAncestor(context.Background(), 1, 1, head)
	require.Error(t, wantErr)
	require.Equal(t, wantErr, gotErr)
}

func TestCommitGraph_LongChainIsBatched(t *testing.T) {
	repos := newGraphRepos(t, buildCommits(1, chainSpec(graphSpec{}, 1, 2000)))
	_, batch := repos.branches(t)

	ok, err := batch.isAncestor(context.Background(), 1, 1, 2000)
	require.NoError(t, err)
	require.True(t, ok)
	require.LessOrEqual(t, repos.batch.ancestorCalls, 4)

	repos.batch.ancestorCalls = 0
	got, err := batch.WalkCommits(context.Background(), 1, 2000, nil, 5000)
	require.NoError(t, err)
	require.Len(t, got, 2000)
	require.LessOrEqual(t, repos.batch.ancestorCalls, 4)

	repos.batch.ancestorCalls = 0
	base, err := batch.findMergeBase(context.Background(), 1, idPtr(2000), idPtr(1500))
	require.NoError(t, err)
	require.Equal(t, snow.ID(1500), *base)
	require.LessOrEqual(t, repos.batch.ancestorCalls, 2)
	require.Zero(t, repos.batch.getCalls)
}

func TestCommitGraph_Get(t *testing.T) {
	commits := testGraph()
	repos := newGraphRepos(t, commits)

	g := newCommitGraph(repos.batch, 1, false)
	_, err := g.get(context.Background(), 400)
	require.EqualError(t, err, fmt.Sprintf("commit %s not found", snow.ID(400).Base36()))
	require.True(t, domain.IsErrorNotFound(err))
	c, err := g.get(context.Background(), 104)
	require.NoError(t, err)
	require.Same(t, commits[104], c)
	calls := repos.batch.ancestorCalls
	_, err = g.get(context.Background(), 100)
	require.NoError(t, err)
	require.Equal(t, calls, repos.batch.ancestorCalls)

	boom := errors.New("boom")
	repos.batch.err = boom
	_, err = newCommitGraph(repos.batch, 1, true).get(context.Background(), 1)
	require.ErrorIs(t, err, boom)

	_, err = newCommitGraph(repos.plain, 1, true).get(context.Background(), 400)
	require.EqualError(t, err, fmt.Sprintf("commit %s not found", snow.ID(400).Base36()))
	c, err = newCommitGraph(repos.plain, 1, false).get(context.Background(), 400)
	require.NoError(t, err)
	require.Equal(t, snow.ID(2), c.ProjectID)
}
