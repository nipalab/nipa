package usecase

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestVisibleChunkCache_ReusesUntilIdleTTL(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c := newVisibleChunkCache(time.Minute, 100, clock.now)
	calls := 0
	compute := func() ([]domain.Hash, error) {
		calls++
		return []domain.Hash{{byte(calls)}}, nil
	}

	got, err := c.get("k", compute)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{{1}}, got)

	clock.t = clock.t.Add(50 * time.Second)
	got, _ = c.get("k", compute)
	require.Equal(t, []domain.Hash{{1}}, got)
	clock.t = clock.t.Add(50 * time.Second)
	got, _ = c.get("k", compute)
	require.Equal(t, []domain.Hash{{1}}, got, "a hit renews the entry")
	require.Equal(t, 1, calls)

	clock.t = clock.t.Add(time.Minute)
	got, _ = c.get("k", compute)
	require.Equal(t, []domain.Hash{{2}}, got)
	require.Equal(t, 1, len(c.entries))
	require.Equal(t, 1, c.size)
}

func TestVisibleChunkCache_DoesNotKeepErrorsOrOversizedAnswers(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c := newVisibleChunkCache(time.Minute, 2, clock.now)
	boom := errors.New("boom")

	_, err := c.get("k", func() ([]domain.Hash, error) { return nil, boom })
	require.ErrorIs(t, err, boom)
	require.Empty(t, c.entries)

	big := []domain.Hash{{1}, {2}, {3}}
	got, err := c.get("big", func() ([]domain.Hash, error) { return big, nil })
	require.NoError(t, err)
	require.Equal(t, big, got)
	require.Empty(t, c.entries)
	require.Zero(t, c.size)
}

func TestVisibleChunkCache_EvictsLeastRecentlyUsed(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c := newVisibleChunkCache(time.Minute, 3, clock.now)
	two := func() ([]domain.Hash, error) { return []domain.Hash{{1}, {2}}, nil }
	one := func() ([]domain.Hash, error) { return []domain.Hash{{3}}, nil }

	_, _ = c.get("a", two)
	_, _ = c.get("b", one)
	_, _ = c.get("a", two)
	_, _ = c.get("c", one)
	require.Contains(t, c.entries, "a")
	require.Contains(t, c.entries, "c")
	require.NotContains(t, c.entries, "b")
	require.Equal(t, 3, c.size)

	clock.t = clock.t.Add(2 * time.Minute)
	_, _ = c.get("d", one)
	require.Equal(t, []string{"d"}, keysOf(c.entries), "expired entries are dropped on insert")
	require.Equal(t, 1, c.size)
}

func keysOf(m map[string]*visibleEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestVisibleChunkCache_ConcurrentMissComputesOnce(t *testing.T) {
	c := newVisibleChunkCache(time.Minute, 100, time.Now)
	var calls atomic.Int32
	release := make(chan struct{})
	compute := func() ([]domain.Hash, error) {
		calls.Add(1)
		<-release
		return []domain.Hash{{9}}, nil
	}

	var wg sync.WaitGroup
	results := make([][]domain.Hash, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], _ = c.get("k", compute)
		}()
	}
	require.Eventually(t, func() bool { return calls.Load() == 1 }, time.Second, time.Millisecond)
	close(release)
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	for _, r := range results {
		require.Equal(t, []domain.Hash{{9}}, r)
	}
}

func TestVisibleChunksKey(t *testing.T) {
	user := domain.Claims{UserID: 7}
	base := visibleChunksKey(user, 1, []string{"b", "a", "a"}, []string{"y", "x"})
	require.Equal(t, base, visibleChunksKey(user, 1, []string{"a", "b"}, []string{"x", "y"}))
	require.NotEqual(t, base, visibleChunksKey(domain.Claims{UserID: 8}, 1, []string{"a", "b"}, []string{"x", "y"}))
	require.NotEqual(t, base, visibleChunksKey(domain.Claims{UserID: 7, IsAdmin: true}, 1, []string{"a", "b"}, []string{"x", "y"}))
	require.NotEqual(t, base, visibleChunksKey(user, 2, []string{"a", "b"}, []string{"x", "y"}))
	require.NotEqual(t, base, visibleChunksKey(user, 1, []string{"a"}, []string{"x", "y"}))
	require.NotEqual(t, base, visibleChunksKey(user, 1, []string{"a", "b"}, []string{"x"}))
}

func TestBranch_VisibleChunks_CachesPerCaller(t *testing.T) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).Times(3)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil).Times(2)
	uc := NewBranch(perm, repo, newTestBranchNode(t))

	alice := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1})
	bob := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 2})
	commits := []string{snow.ID(1).Base36()}

	first, err := uc.VisibleChunks(alice, 1, commits, nil)
	require.NoError(t, err)
	walked := repo.loadedDirs()
	again, err := uc.VisibleChunks(alice, 1, commits, nil)
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.Equal(t, walked, repo.loadedDirs(), "the second window is served from the cache")

	repo.filesCall = map[int64]int{}
	_, err = uc.VisibleChunks(bob, 1, commits, nil)
	require.NoError(t, err)
	require.Equal(t, walked, repo.loadedDirs(), "another caller walks the tree again")
}

func TestBranch_VisibleChunks_ChecksAccessBeforeCache(t *testing.T) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	gomock.InOrder(
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true),
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(AllowAllFilter(), nil),
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false),
	)
	uc := NewBranch(perm, repo, newTestBranchNode(t))
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1})

	_, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36()}, nil)
	require.NoError(t, err)
	_, err = uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36()}, nil)
	require.True(t, domain.IsErrorNoPermission(err))
}
