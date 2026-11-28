package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

// fakeChunkRows holds chunk rows per project, like the chunks table with its
// UNIQUE (project_id, hash), and records every lookup.
type fakeChunkRows struct {
	rows  map[snow.ID]map[domain.Hash]struct{}
	calls int
	err   error
}

func (f *fakeChunkRows) add(projectID snow.ID, hashes ...domain.Hash) {
	if f.rows == nil {
		f.rows = map[snow.ID]map[domain.Hash]struct{}{}
	}
	if f.rows[projectID] == nil {
		f.rows[projectID] = map[domain.Hash]struct{}{}
	}
	for _, hash := range hashes {
		f.rows[projectID][hash] = struct{}{}
	}
}

func (f *fakeChunkRows) ProjectChunkRecords(_ context.Context, projectID snow.ID, hashes []domain.Hash) (map[domain.Hash]ChunkRecord, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := map[domain.Hash]ChunkRecord{}
	for _, hash := range hashes {
		if _, ok := f.rows[projectID][hash]; ok {
			out[hash] = ChunkRecord{SizeBytes: 1}
		}
	}
	return out, nil
}

// countingTreeRepo counts every batched tree statement and remembers which
// files had their chunks read.
type countingTreeRepo struct {
	*memTreeRepo
	statements int
	chunkFiles []int64
}

func (r *countingTreeRepo) ListFileMetaByTrees(ctx context.Context, ids []int64) (map[int64][]*domain.File, error) {
	r.statements++
	return r.memTreeRepo.ListFileMetaByTrees(ctx, ids)
}

func (r *countingTreeRepo) ListChunksByFiles(ctx context.Context, ids []int64) (map[int64][]domain.Chunk, error) {
	r.statements++
	r.chunkFiles = append(r.chunkFiles, ids...)
	return r.memTreeRepo.ListChunksByFiles(ctx, ids)
}

func (r *countingTreeRepo) ListTreeChildrenByTrees(ctx context.Context, ids []int64) (map[int64][]*domain.TreeNode, error) {
	r.statements++
	return r.memTreeRepo.ListTreeChildrenByTrees(ctx, ids)
}

func downloadPerm(t *testing.T, filter *PathFilter) *MockpermissionUsecase {
	t.Helper()
	perm := NewMockpermissionUsecase(gomock.NewController(t))
	perm.EXPECT().HasProjectAccess(gomock.Any(), gomock.Any(), domain.PermissionRead).Return(true).AnyTimes()
	perm.EXPECT().CompileFilter(gomock.Any(), gomock.Any(), domain.PermissionRead).Return(filter, nil).AnyTimes()
	return perm
}

func contentHash(content string) domain.Hash { return chunker.Sum([]byte(content)) }

func TestBranch_DownloadableChunks_UnrestrictedUsesChunkRows(t *testing.T) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	repo.addCommit(2, 2, map[string]string{"other.txt": "other project"})

	inProject := contentHash("code content 1")
	uncommitted := domain.Hash{0xaa}
	foreign := contentHash("other project")
	unknown := domain.Hash{0xbb}
	rows := &fakeChunkRows{}
	rows.add(1, inProject, uncommitted)
	rows.add(2, foreign)

	uc := NewBranch(downloadPerm(t, AllowAllFilter()), repo, newTestBranchNode(t)).WithChunkRows(rows)
	got, scope, err := uc.DownloadableChunks(context.Background(), 1, []string{snow.ID(1).Base36()}, []string{"code"},
		[]domain.Hash{unknown, inProject, foreign, uncommitted, inProject})
	require.NoError(t, err)
	require.Equal(t, ChunkReadProject, scope)
	require.Equal(t, []domain.Hash{inProject, uncommitted}, got, "request order, no duplicates, only this project's rows")
	require.Equal(t, 1, rows.calls)
	require.Zero(t, repo.loadedDirs(), "the fast path walks no tree")
	require.Zero(t, repo.batches)
}

func TestBranch_DownloadableChunks_ValidatesEveryCaller(t *testing.T) {
	ctx := context.Background()
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, map[string]string{"a.txt": "a"})
	repo.addCommit(2, 2, map[string]string{"b.txt": "b"})
	hashes := []domain.Hash{contentHash("a")}

	for name, filter := range map[string]*PathFilter{"unrestricted": AllowAllFilter(), "restricted": restrictedReadFilter("a.txt")} {
		t.Run(name, func(t *testing.T) {
			rows := &fakeChunkRows{}
			uc := NewBranch(downloadPerm(t, filter), repo, newTestBranchNode(t)).WithChunkRows(rows)

			_, _, err := uc.DownloadableChunks(ctx, 1, nil, nil, hashes)
			requireUserError(t, err)
			_, _, err = uc.DownloadableChunks(ctx, 1, []string{"!!"}, nil, hashes)
			requireUserError(t, err)
			_, _, err = uc.DownloadableChunks(ctx, 1, []string{snow.ID(1).Base36()}, []string{"../x"}, hashes)
			requireUserError(t, err)
			_, _, err = uc.DownloadableChunks(ctx, 1, []string{snow.ID(2).Base36()}, nil, hashes)
			require.True(t, domain.IsErrorNotFound(err), "a commit of another project: %v", err)
			require.Zero(t, rows.calls, "invalid requests never reach the chunk rows")
		})
	}
}

func TestBranch_DownloadableChunks_Errors(t *testing.T) {
	ctx := context.Background()
	commits := []string{snow.ID(1).Base36()}

	t.Run("no read access", func(t *testing.T) {
		perm := NewMockpermissionUsecase(gomock.NewController(t))
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(false)
		uc := NewBranch(perm, newMemTreeRepo(), newTestBranchNode(t))
		_, _, err := uc.DownloadableChunks(ctx, 1, commits, nil, nil)
		require.True(t, domain.IsErrorNoPermission(err))
	})

	t.Run("rules unavailable", func(t *testing.T) {
		boom := errors.New("rules unavailable")
		perm := NewMockpermissionUsecase(gomock.NewController(t))
		perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
		perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(nil, boom)
		uc := NewBranch(perm, newMemTreeRepo(), newTestBranchNode(t))
		_, _, err := uc.DownloadableChunks(ctx, 1, commits, nil, nil)
		require.ErrorIs(t, err, boom)
	})

	t.Run("chunk rows unavailable", func(t *testing.T) {
		boom := errors.New("db down")
		repo := newMemTreeRepo()
		repo.addCommit(1, 1, map[string]string{"a.txt": "a"})
		uc := NewBranch(downloadPerm(t, AllowAllFilter()), repo, newTestBranchNode(t)).WithChunkRows(&fakeChunkRows{err: boom})
		_, _, err := uc.DownloadableChunks(ctx, 1, commits, nil, []domain.Hash{{1}})
		require.ErrorIs(t, err, boom)
	})

	t.Run("walk fails", func(t *testing.T) {
		boom := errors.New("db down")
		repo := newMemTreeRepo()
		repo.addCommit(1, 1, map[string]string{"a/x.txt": "1"})
		repo.listErr = boom
		uc := NewBranch(downloadPerm(t, restrictedReadFilter("a")), repo, newTestBranchNode(t))
		_, scope, err := uc.DownloadableChunks(ctx, 1, commits, nil, []domain.Hash{{1}})
		require.ErrorIs(t, err, boom)
		require.Equal(t, ChunkReadPaths, scope)
	})
}

// A path-restricted caller is the security boundary: only hashes of files
// under its rules come back, whatever the chunk rows hold.
func TestBranch_DownloadableChunks_RestrictedWalksVisibleSet(t *testing.T) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	public := contentHash("public content 0")
	nested := contentHash("public nested 2")
	hidden := contentHash("code content 0")
	rows := &fakeChunkRows{}
	rows.add(1, public, nested, hidden)

	uc := NewBranch(downloadPerm(t, restrictedReadFilter("public")), repo, newTestBranchNode(t)).WithChunkRows(rows)
	got, scope, err := uc.DownloadableChunks(context.Background(), 1, []string{snow.ID(1).Base36()}, nil,
		[]domain.Hash{hidden, nested, public, nested})
	require.NoError(t, err)
	require.Equal(t, ChunkReadPaths, scope)
	require.Equal(t, []domain.Hash{nested, public}, got)
	require.Zero(t, rows.calls, "restricted callers never use the chunk rows")
}

func TestBranch_DownloadableChunks_UnrestrictedWithoutRowsWalks(t *testing.T) {
	repo := newMemTreeRepo()
	repo.addCommit(1, 1, wideRepo(nil))
	readme := contentHash("readme")

	uc := NewBranch(downloadPerm(t, AllowAllFilter()), repo, newTestBranchNode(t))
	got, scope, err := uc.DownloadableChunks(context.Background(), 1, []string{snow.ID(1).Base36()}, nil, []domain.Hash{readme, {0xcc}})
	require.NoError(t, err)
	require.Equal(t, ChunkReadProject, scope)
	require.Equal(t, []domain.Hash{readme}, got)
	require.NotZero(t, repo.loadedDirs())
}

// The walk reads a level per statement batch: a wide tree costs a handful of
// statements per level, not some per directory, and the chunks of hidden
// files are never read.
func TestBranch_VisibleChunks_BatchesPerLevel(t *testing.T) {
	paths := map[string]string{}
	for i := 0; i < 60; i++ {
		paths[fmt.Sprintf("public/d%02d/f.txt", i)] = fmt.Sprintf("public %d", i)
		paths[fmt.Sprintf("secret/d%02d/f.txt", i)] = fmt.Sprintf("secret %d", i)
	}
	paths["secret.txt"] = "root secret"
	repo := &countingTreeRepo{memTreeRepo: newMemTreeRepo()}
	repo.addCommit(1, 1, paths)

	uc := NewBranch(downloadPerm(t, restrictedReadFilter("public")), repo, newTestBranchNode(t))
	got, err := uc.VisibleChunks(context.Background(), 1, []string{snow.ID(1).Base36()}, nil)
	require.NoError(t, err)
	require.Len(t, got, 60)
	// Levels: root, public, public/dNN. Root and public hold no readable
	// file, so only the last level reads chunks: 3 metadata + 3 children + 1
	// chunk batch.
	require.Equal(t, 7, repo.statements)

	for _, id := range repo.chunkFiles {
		var name string
		for _, files := range repo.files {
			for _, f := range files {
				if f.ID == id {
					name = f.Name
				}
			}
		}
		require.Equal(t, "f.txt", name)
	}
	require.Len(t, repo.chunkFiles, 60, "only the readable files load chunks")
}

func TestBranch_VisibleChunks_SkipsEmptyTrees(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := NewMockbranchRepository(ctrl)
	repo.EXPECT().GetCommit(gomock.Any(), snow.ID(1)).Return(&domain.Commit{ID: 1, ProjectID: 1, TreeID: 5}, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(5)).Return(&domain.TreeNode{ID: 5, Hash: emptyTreeHash}, nil)

	uc := NewBranch(downloadPerm(t, AllowAllFilter()), repo, newTestBranchNode(t))
	got, err := uc.VisibleChunks(context.Background(), 1, []string{snow.ID(1).Base36()}, nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestBranch_VisibleChunks_ChunkAndTreeErrors(t *testing.T) {
	boom := errors.New("db down")
	ctx := context.Background()

	t.Run("chunks", func(t *testing.T) {
		repo := newMemTreeRepo()
		repo.addCommit(1, 1, map[string]string{"a.txt": "a"})
		repo.chunkErr = boom
		uc := NewBranch(downloadPerm(t, AllowAllFilter()), repo, newTestBranchNode(t))
		_, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36()}, nil)
		require.ErrorIs(t, err, boom)
	})

	t.Run("root tree", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		repo := NewMockbranchRepository(ctrl)
		repo.EXPECT().GetCommit(gomock.Any(), snow.ID(1)).Return(&domain.Commit{ID: 1, ProjectID: 1, TreeID: 5}, nil)
		repo.EXPECT().GetTreeNode(gomock.Any(), int64(5)).Return(nil, boom)
		uc := NewBranch(downloadPerm(t, AllowAllFilter()), repo, newTestBranchNode(t))
		_, err := uc.VisibleChunks(ctx, 1, []string{snow.ID(1).Base36()}, nil)
		require.ErrorIs(t, err, boom)
	})
}

func scrapeVisibleMetrics(t *testing.T, m *obs.Metrics) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	return rr.Body.String()
}

// An answer larger than the whole cache is recomputed per call, but it is
// counted every time and logged once per key and ttl.
func TestVisibleChunkCache_ReportsOversize(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c := newVisibleChunkCache(time.Minute, 2, clock.now)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewTextHandler(&logs, nil))
	c.metrics = obs.NewMetrics()

	calls := 0
	big := func(context.Context) ([]domain.Hash, error) {
		calls++
		return []domain.Hash{{1}, {2}, {3}}, nil
	}
	req := visibleRequest{projectID: 9, commits: 1}
	for range 3 {
		got, err := c.get(context.Background(), "big", req, big)
		require.NoError(t, err)
		require.Len(t, got, 3)
	}
	require.Equal(t, 3, calls, "an oversize answer is not kept")
	require.Equal(t, 1, bytes.Count(logs.Bytes(), []byte("larger than the whole visibility cache")))
	require.Contains(t, logs.String(), "hashes=3")
	require.Contains(t, logs.String(), "cache_hashes=2")
	require.Contains(t, logs.String(), "CHUNK_VISIBILITY_CACHE_HASHES")

	clock.t = clock.t.Add(2 * time.Minute)
	_, err := c.get(context.Background(), "big", req, big)
	require.NoError(t, err)
	require.Equal(t, 2, bytes.Count(logs.Bytes(), []byte("larger than the whole visibility cache")), "warned again after the ttl")

	small := func(context.Context) ([]domain.Hash, error) { return []domain.Hash{{1}}, nil }
	_, err = c.get(context.Background(), "small", req, small)
	require.NoError(t, err)
	_, err = c.get(context.Background(), "small", req, small)
	require.NoError(t, err)

	body := scrapeVisibleMetrics(t, c.metrics)
	require.Contains(t, body, `nipa_visible_chunks_cache_total{result="oversize"} 4`)
	require.Contains(t, body, `nipa_visible_chunks_cache_total{result="miss"} 1`)
	require.Contains(t, body, `nipa_visible_chunks_cache_total{result="hit"} 1`)
	require.Contains(t, body, `nipa_visible_chunks_compute_seconds_count{result="ok"} 5`)
}

func TestVisibleChunkCache_ReportsTimeout(t *testing.T) {
	c := newVisibleChunkCache(time.Minute, 100, time.Now)
	c.timeout = 10 * time.Millisecond
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewTextHandler(&logs, nil))
	c.metrics = obs.NewMetrics()

	_, err := c.get(context.Background(), "slow", visibleRequest{projectID: 3, commits: 2}, func(ctx context.Context) ([]domain.Hash, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, logs.String(), "timed out")
	require.Contains(t, logs.String(), "commits=2")
	require.Contains(t, logs.String(), "CHUNK_VISIBILITY_TIMEOUT_SECONDS")

	_, err = c.get(context.Background(), "broken", visibleRequest{}, func(context.Context) ([]domain.Hash, error) {
		return nil, errors.New("db down")
	})
	require.Error(t, err)
	body := scrapeVisibleMetrics(t, c.metrics)
	require.Contains(t, body, `nipa_visible_chunks_compute_seconds_count{result="timeout"} 1`)
	require.Contains(t, body, `nipa_visible_chunks_compute_seconds_count{result="error"} 1`)
	require.Contains(t, body, `nipa_visible_chunks_cache_total{result="error"} 2`)
}

func TestBranch_VisibleChunksOptions(t *testing.T) {
	uc := NewBranch(downloadPerm(t, AllowAllFilter()), newMemTreeRepo(), newTestBranchNode(t))
	require.Equal(t, DefaultVisibleChunksComputeTimeout, uc.visible.timeout)

	uc.WithVisibleChunksTimeout(0).WithVisibleChunksCacheHashes(0)
	require.Equal(t, DefaultVisibleChunksComputeTimeout, uc.visible.timeout)
	require.Equal(t, DefaultVisibleChunksCacheHashes, uc.visible.maxHashes)

	m := obs.NewMetrics()
	uc.WithVisibleChunksTimeout(5 * time.Second).WithMetrics(m)
	uc.WithVisibleChunksCacheHashes(10)
	require.Equal(t, 5*time.Second, uc.visible.timeout, "resizing keeps the timeout")
	require.Same(t, m, uc.visible.metrics, "resizing keeps the metrics")
	require.Equal(t, 10, uc.visible.maxHashes)

	var deadline time.Time
	_, _ = uc.visible.get(context.Background(), "k", visibleRequest{}, func(ctx context.Context) ([]domain.Hash, error) {
		deadline, _ = ctx.Deadline()
		return nil, nil
	})
	require.WithinDuration(t, time.Now().Add(5*time.Second), deadline, time.Second)
}

func TestVisibleChunkCache_SetMaxHashesEvicts(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c := newVisibleChunkCache(time.Minute, 100, clock.now)
	three := func(context.Context) ([]domain.Hash, error) { return []domain.Hash{{1}, {2}, {3}}, nil }
	_, _ = c.get(context.Background(), "a", visibleRequest{}, three)
	_, _ = c.get(context.Background(), "b", visibleRequest{}, three)
	require.Len(t, c.entries, 2)

	c.setMaxHashes(5)
	require.Len(t, c.entries, 1)
	require.LessOrEqual(t, c.size, 5)
}
