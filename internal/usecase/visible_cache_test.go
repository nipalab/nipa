package usecase

import (
	"context"
	"errors"
	"strconv"
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
	compute := func(context.Context) ([]domain.Hash, error) {
		calls++
		return []domain.Hash{{byte(calls)}}, nil
	}

	got, err := c.get(context.Background(), "k", visibleRequest{}, compute)
	require.NoError(t, err)
	require.Equal(t, []domain.Hash{{1}}, got)

	clock.t = clock.t.Add(50 * time.Second)
	got, _ = c.get(context.Background(), "k", visibleRequest{}, compute)
	require.Equal(t, []domain.Hash{{1}}, got)
	clock.t = clock.t.Add(50 * time.Second)
	got, _ = c.get(context.Background(), "k", visibleRequest{}, compute)
	require.Equal(t, []domain.Hash{{1}}, got, "a hit renews the entry")
	require.Equal(t, 1, calls)

	clock.t = clock.t.Add(time.Minute)
	got, _ = c.get(context.Background(), "k", visibleRequest{}, compute)
	require.Equal(t, []domain.Hash{{2}}, got)
	require.Equal(t, 1, len(c.entries))
	require.Equal(t, 2, c.size)
}

func TestVisibleChunkCache_DoesNotKeepErrorsOrOversizedAnswers(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c := newVisibleChunkCache(time.Minute, 2, clock.now)
	boom := errors.New("boom")

	_, err := c.get(context.Background(), "k", visibleRequest{}, func(context.Context) ([]domain.Hash, error) { return nil, boom })
	require.ErrorIs(t, err, boom)
	require.Empty(t, c.entries)

	big := []domain.Hash{{1}, {2}, {3}}
	got, err := c.get(context.Background(), "big", visibleRequest{}, func(context.Context) ([]domain.Hash, error) { return big, nil })
	require.NoError(t, err)
	require.Equal(t, big, got)
	require.Empty(t, c.entries)
	require.Zero(t, c.size)
}

func TestVisibleChunkCache_EvictsLeastRecentlyUsed(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c := newVisibleChunkCache(time.Minute, 5, clock.now)
	two := func(context.Context) ([]domain.Hash, error) { return []domain.Hash{{1}, {2}}, nil }
	one := func(context.Context) ([]domain.Hash, error) { return []domain.Hash{{3}}, nil }

	_, _ = c.get(context.Background(), "a", visibleRequest{}, two)
	_, _ = c.get(context.Background(), "b", visibleRequest{}, one)
	_, _ = c.get(context.Background(), "a", visibleRequest{}, two)
	_, _ = c.get(context.Background(), "c", visibleRequest{}, one)
	require.Contains(t, c.entries, "a")
	require.Contains(t, c.entries, "c")
	require.NotContains(t, c.entries, "b")
	require.Equal(t, 5, c.size)

	clock.t = clock.t.Add(2 * time.Minute)
	_, _ = c.get(context.Background(), "d", visibleRequest{}, one)
	require.Equal(t, []string{"d"}, keysOf(c.entries), "expired entries are dropped on insert")
	require.Equal(t, 2, c.size)
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
	compute := func(context.Context) ([]domain.Hash, error) {
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
			results[i], _ = c.get(context.Background(), "k", visibleRequest{}, compute)
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
	key := func(f *PathFilter, project snow.ID, commits, paths []string) string {
		t.Helper()
		k, ok := visibleChunksKey(f, project, commits, paths)
		require.True(t, ok)
		return k
	}
	reader := restrictedReadFilter("a", "b")
	base := key(reader, 1, []string{"b", "a", "a"}, []string{"y", "x"})
	require.Equal(t, base, key(reader, 1, []string{"a", "b"}, []string{"x", "y"}), "order and duplicates do not matter")
	require.Equal(t, base, key(restrictedReadFilter("b", "a", "a"), 1, []string{"a", "b"}, []string{"x", "y"}), "the same rules in another order")
	require.NotEqual(t, base, key(reader, 2, []string{"a", "b"}, []string{"x", "y"}))
	require.NotEqual(t, base, key(reader, 1, []string{"a"}, []string{"x", "y"}))
	require.NotEqual(t, base, key(reader, 1, []string{"a", "b"}, []string{"x"}))

	// Every change of what the filter lets through changes the key.
	require.NotEqual(t, base, key(restrictedReadFilter("a"), 1, []string{"a", "b"}, []string{"x", "y"}), "a rule revoked")
	require.NotEqual(t, base, key(restrictedReadFilter("a", "b", "c"), 1, []string{"a", "b"}, []string{"x", "y"}), "a rule granted")
	require.NotEqual(t, base, key(AllowAllFilter(), 1, []string{"a", "b"}, []string{"x", "y"}), "an admin")
	denied := &PathFilter{permission: domain.PermissionRead, set: &permissionSet{
		rules:    reader.set.rules,
		defaults: []*domain.ProjectPathPermission{{PathPrefix: "", Permission: domain.PermissionRead}},
	}}
	require.NotEqual(t, base, key(denied, 1, []string{"a", "b"}, []string{"x", "y"}), "a path default added")
	owner := &PathFilter{permission: domain.PermissionRead, set: &permissionSet{orgOwner: true}}
	require.Equal(t, key(AllowAllFilter(), 1, []string{"a"}, nil), key(owner, 1, []string{"a"}, nil), "everyone who reads everything shares a key")

	// Rules that grant no read never matter.
	writeOnly := &PathFilter{permission: domain.PermissionRead, set: &permissionSet{
		rules: append([]*domain.PBACRule{{PathPrefix: "z", Permission: domain.PermissionWrite}}, reader.set.rules...),
	}}
	require.Equal(t, base, key(writeOnly, 1, []string{"a", "b"}, []string{"x", "y"}))

	_, ok := visibleChunksKey(nil, 1, []string{"a"}, nil)
	require.False(t, ok, "a nil filter has no key")
	_, ok = visibleChunksKey(&PathFilter{}, 1, []string{"a"}, nil)
	require.False(t, ok, "a filter without a rule set has no key")
}

// rulePerm is a permission usecase whose read rules per user can be changed
// between calls, like an admin granting and revoking.
type rulePerm struct {
	prefixes map[snow.ID][]string
	compiled int
}

func (p *rulePerm) filter(ctx context.Context) *PathFilter {
	claim, _ := domain.ClaimFromContext(ctx)
	return restrictedReadFilter(p.prefixes[claim.UserID]...)
}

func (p *rulePerm) HasProjectAccess(ctx context.Context, _ snow.ID, _ domain.Permission) bool {
	claim, _ := domain.ClaimFromContext(ctx)
	return len(p.prefixes[claim.UserID]) > 0
}

func (p *rulePerm) HasPathAccess(ctx context.Context, _ snow.ID, path string, _ domain.Permission) bool {
	return p.filter(ctx).Allow(path)
}

func (p *rulePerm) CompileFilter(ctx context.Context, _ snow.ID, _ domain.Permission) (*PathFilter, error) {
	p.compiled++
	return p.filter(ctx), nil
}

func (p *rulePerm) AdminHasProject(context.Context, snow.ID) bool { return false }

func TestBranch_VisibleChunks_CachesPerRuleSet(t *testing.T) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	const alice, bob, carol = snow.ID(1), snow.ID(2), snow.ID(3)
	perm := &rulePerm{prefixes: map[snow.ID][]string{
		alice: {"assets"},
		bob:   {"assets", "code"},
		carol: {"assets"},
	}}
	uc := NewBranch(perm, repo, newTestBranchNode(t))
	ctxOf := func(id snow.ID) context.Context {
		return domain.ContextWithClaim(context.Background(), domain.Claims{UserID: id})
	}
	commits := []string{snow.ID(1).Base36()}
	// ask returns the answer and the directories it had to load.
	ask := func(id snow.ID) ([]domain.Hash, int) {
		t.Helper()
		repo.filesCall = map[int64]int{}
		got, err := uc.VisibleChunks(ctxOf(id), 1, commits, nil)
		require.NoError(t, err)
		return got, repo.loadedDirs()
	}
	want := func(id snow.ID) []domain.Hash {
		t.Helper()
		return visibleReference(t, uc, repo, []snow.ID{1}, restrictedReadFilter(perm.prefixes[id]...), nil)
	}

	aliceFirst, walked := ask(alice)
	require.Equal(t, want(alice), aliceFirst)
	require.Positive(t, walked)
	again, walked := ask(alice)
	require.Equal(t, aliceFirst, again)
	require.Zero(t, walked, "the second window is served from the cache")

	// Two callers with different rules on the same commit never share.
	bobFirst, walked := ask(bob)
	require.Positive(t, walked, "other rules walk the tree again")
	require.Equal(t, want(bob), bobFirst)
	require.Greater(t, len(bobFirst), len(aliceFirst))
	again, _ = ask(alice)
	require.Equal(t, aliceFirst, again, "and leave the first caller's answer alone")

	// A caller with the same rules as another reuses the walk.
	carolFirst, walked := ask(carol)
	require.Equal(t, aliceFirst, carolFirst)
	require.Zero(t, walked, "equal rules share an entry")

	// Revoke: the cached answer of the wider rules is not served any more.
	perm.prefixes[bob] = []string{"assets"}
	revoked, _ := ask(bob)
	require.Equal(t, aliceFirst, revoked, "a revoke shows on the very next call")
	require.Less(t, len(revoked), len(bobFirst))

	// Grant: the cached answer of the narrower rules is not served either.
	perm.prefixes[alice] = []string{"assets", "code", "docs"}
	granted, walked := ask(alice)
	require.Equal(t, want(alice), granted, "a grant shows on the very next call")
	require.Positive(t, walked)
	require.Greater(t, len(granted), len(bobFirst))
	stillNarrow, _ := ask(carol)
	require.Equal(t, aliceFirst, stillNarrow, "a caller whose rules did not change keeps its answer")

	// Granting back what was revoked finds the old entry again.
	perm.prefixes[bob] = []string{"assets", "code"}
	back, walked := ask(bob)
	require.Equal(t, bobFirst, back)
	require.Zero(t, walked)

	require.Equal(t, 9, perm.compiled, "the filter is compiled on every call, hit or miss")
}

func TestBranch_VisibleChunks_FilterWithoutFingerprintIsNotCached(t *testing.T) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true).Times(2)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, nil).Times(2)
	uc := NewBranch(perm, repo, newTestBranchNode(t))
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1})

	for range 2 {
		got, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36()}, nil)
		require.NoError(t, err)
		require.Empty(t, got, "a nil filter allows nothing")
	}
	require.Empty(t, uc.visible.entries)
}

func TestBranch_VisibleChunks_CompileFilterError(t *testing.T) {
	ctrl := gomock.NewController(t)
	perm := NewMockpermissionUsecase(ctrl)
	boom := errors.New("rules unavailable")
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, boom)
	uc := NewBranch(perm, newMemTreeRepo(), newTestBranchNode(t))
	_, err := uc.VisibleChunks(context.Background(), 1, []string{snow.ID(1).Base36()}, nil)
	require.ErrorIs(t, err, boom)
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

func TestBranch_WithVisibleChunksCacheHashes(t *testing.T) {
	uc := NewBranch(nil, nil, nil)
	require.Equal(t, DefaultVisibleChunksCacheHashes, uc.visible.maxHashes)
	require.Equal(t, DefaultVisibleChunksCacheHashes, uc.WithVisibleChunksCacheHashes(0).visible.maxHashes)
	require.Equal(t, 10, uc.WithVisibleChunksCacheHashes(10).visible.maxHashes)
}

func TestVisibleChunkCache_EmptyEntriesAreBounded(t *testing.T) {
	c := newVisibleChunkCache(time.Minute, 10, time.Now)
	empty := func(context.Context) ([]domain.Hash, error) { return nil, nil }
	for i := 0; i < 100; i++ {
		_, err := c.get(context.Background(), string(rune('a'+i%26))+string(rune('A'+i/26)), visibleRequest{}, empty)
		require.NoError(t, err)
	}
	require.LessOrEqual(t, len(c.entries), 10)
	require.LessOrEqual(t, c.size, 10)
}

func TestVisibleChunkCache_LeaderCancelDoesNotFailWaiters(t *testing.T) {
	c := newVisibleChunkCache(time.Minute, 100, time.Now)
	started := make(chan struct{})
	release := make(chan struct{})
	compute := func(ctx context.Context) ([]domain.Hash, error) {
		close(started)
		<-release
		return []domain.Hash{{5}}, ctx.Err()
	}
	leaderCtx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = c.get(leaderCtx, "k", visibleRequest{}, compute) }()
	<-started
	cancel()

	done := make(chan []domain.Hash, 1)
	go func() {
		got, _ := c.get(context.Background(), "k", visibleRequest{}, compute)
		done <- got
	}()
	close(release)
	require.Equal(t, []domain.Hash{{5}}, <-done)
}

func TestVisibleChunkCache_WaiterHonoursOwnContext(t *testing.T) {
	c := newVisibleChunkCache(time.Minute, 100, time.Now)
	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = c.get(context.Background(), "k", visibleRequest{}, func(context.Context) ([]domain.Hash, error) {
			close(started)
			<-release
			return nil, nil
		})
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.get(ctx, "k", visibleRequest{}, nil)
	require.ErrorIs(t, err, context.Canceled)
	close(release)
}

func TestVisibleChunkCache_ComputeIsBoundedAndDetached(t *testing.T) {
	c := newVisibleChunkCache(time.Minute, 100, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var deadline time.Time
	var hasDeadline bool
	var ctxErr error
	_, _ = c.get(ctx, "k", visibleRequest{}, func(cctx context.Context) ([]domain.Hash, error) {
		deadline, hasDeadline = cctx.Deadline()
		ctxErr = cctx.Err()
		return nil, nil
	})
	require.True(t, hasDeadline)
	require.WithinDuration(t, time.Now().Add(DefaultVisibleChunksComputeTimeout), deadline, 5*time.Second)
	require.NoError(t, ctxErr)
}

// benchFilter is a restricted reader with rules read rules and defaults path
// defaults, more than a project normally has.
func benchFilter(rules, defaults int) *PathFilter {
	set := &permissionSet{}
	for i := range rules {
		set.rules = append(set.rules, &domain.PBACRule{PathPrefix: "assets/team" + strconv.Itoa(i) + "/textures", Permission: domain.PermissionRead})
	}
	for i := range defaults {
		set.defaults = append(set.defaults, &domain.ProjectPathPermission{PathPrefix: "public/area" + strconv.Itoa(i), Permission: domain.PermissionRead})
	}
	return &PathFilter{set: set, permission: domain.PermissionRead}
}

// BenchmarkVisibleChunksKey is what the rule fingerprint adds to every
// GetChunkDownloadUrls call, hit or miss.
func BenchmarkVisibleChunksKey(b *testing.B) {
	commits := []string{snow.ID(1).Base36(), snow.ID(2).Base36()}
	for _, size := range []struct{ rules, defaults int }{{0, 1}, {5, 5}, {50, 20}, {500, 100}} {
		filter := benchFilter(size.rules, size.defaults)
		b.Run(strconv.Itoa(size.rules)+"rules_"+strconv.Itoa(size.defaults)+"defaults", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := visibleChunksKey(filter, 1, commits, nil); !ok {
					b.Fatal("no key")
				}
			}
		})
	}
}

// BenchmarkBranch_VisibleChunks_Hit is a whole warm call: access check,
// filter compile, key and cache lookup.
func BenchmarkBranch_VisibleChunks_Hit(b *testing.B) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	ctrl := gomock.NewController(b)
	perm := NewMockpermissionUsecase(ctrl)
	filter := benchFilter(50, 20)
	perm.EXPECT().HasProjectAccess(gomock.Any(), gomock.Any(), gomock.Any()).Return(true).AnyTimes()
	perm.EXPECT().CompileFilter(gomock.Any(), gomock.Any(), gomock.Any()).Return(filter, nil).AnyTimes()
	node, err := snow.NewNode(1)
	if err != nil {
		b.Fatal(err)
	}
	uc := NewBranch(perm, repo, node)
	ctx := domain.ContextWithClaim(context.Background(), domain.Claims{UserID: 1})
	commits := []string{snow.ID(1).Base36()}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := uc.VisibleChunks(ctx, 1, commits, nil); err != nil {
			b.Fatal(err)
		}
	}
}
