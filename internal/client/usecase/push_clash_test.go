package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

var clashTracked = map[string]string{
	"a/b":          "b\n",
	"a/x/one":      "1\n",
	"a/x/sub/deep": "d\n",
	"a/x/two":      "2\n",
	"k":            "k\n",
	"top/f":        "f\n",
}

func stagedOf(t *testing.T, fx *shelfFixture) []string {
	t.Helper()
	staged, err := fx.lr.ListStaged()
	require.NoError(t, err)
	return staged
}

// Adding a file that stands where a tracked folder was stages the deletions
// of the tracked files below it, like adding a deleted folder does; adding
// below a tracked file that is a folder now stages that file's deletion.
func TestAdd_StagesWhatAFileFolderSwapDisplaces(t *testing.T) {
	ctx := context.Background()
	swap := func(t *testing.T) *shelfFixture {
		fx := newShelfFixture(t, clashTracked)
		require.NoError(t, os.RemoveAll(filepath.Join(fx.root, "a", "x")))
		writeRepoFile(t, fx.root, "a/x", "a file now\n")
		require.NoError(t, os.Remove(filepath.Join(fx.root, "a", "b")))
		writeRepoFile(t, fx.root, "a/b/c/d", "below\n")
		require.NoError(t, os.RemoveAll(filepath.Join(fx.root, "top")))
		writeRepoFile(t, fx.root, "top", "root file\n")
		require.NoError(t, os.Remove(filepath.Join(fx.root, "k")))
		writeRepoFile(t, fx.root, "k/new", "root folder\n")
		return fx
	}
	cases := []struct {
		name    string
		targets []string
		want    []string
	}{
		{name: "file where a folder was", targets: []string{"a/x"}, want: []string{"a/x", "a/x/one", "a/x/sub/deep", "a/x/two"}},
		{name: "root file where a folder was", targets: []string{"top"}, want: []string{"top", "top/f"}},
		{name: "file below a tracked file", targets: []string{"a/b/c/d"}, want: []string{"a/b", "a/b/c/d"}},
		{name: "folder that was a tracked file", targets: []string{"a/b"}, want: []string{"a/b", "a/b/c/d"}},
		{name: "folder below a tracked file", targets: []string{"a/b/c"}, want: []string{"a/b", "a/b/c/d"}},
		{name: "root folder that was a tracked file", targets: []string{"k"}, want: []string{"k", "k/new"}},
		{name: "file below a root tracked file", targets: []string{"k/new"}, want: []string{"k", "k/new"}},
		{name: "both swaps", targets: []string{"a/x", "a/b/c/d"}, want: []string{"a/b", "a/b/c/d", "a/x", "a/x/one", "a/x/sub/deep", "a/x/two"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := swap(t)
			require.NoError(t, fx.wc.Add(ctx, tc.targets))
			require.Equal(t, tc.want, stagedOf(t, fx))

			// The full-load path stages the same.
			full := swap(t)
			wc, err := NewWorkingCopy(fullLoadRepo{full.lr}, full.root)
			require.NoError(t, err)
			require.NoError(t, wc.Add(ctx, tc.targets))
			require.Equal(t, tc.want, stagedOf(t, full))

			// What add staged is a push the tree check accepts.
			base, err := fx.lr.SnapshotFiles(tc.want)
			require.NoError(t, err)
			toRead, removed, err := classifyStaged(fx.root, tc.want, base)
			require.NoError(t, err)
			require.NoError(t, newTestPush(t, fx.lr, nil).checkStagedTree(tc.want, base, toRead, removed))
		})
	}

	t.Run("a plain file stages only itself", func(t *testing.T) {
		fx := newShelfFixture(t, clashTracked)
		writeRepoFile(t, fx.root, "a/x/one", "edited\n")
		writeRepoFile(t, fx.root, "a/x/new", "new\n")
		require.NoError(t, fx.wc.Add(ctx, []string{"a/x/one", "a/x/new"}))
		require.Equal(t, []string{"a/x/new", "a/x/one"}, stagedOf(t, fx))
	})
}

func clashScope(t *testing.T, fx *shelfFixture, full bool) *trackedScope {
	t.Helper()
	var repo snapshotLoader = fx.lr
	if full {
		repo = fullLoadRepo{fx.lr}
	}
	scope, err := newTrackedScope(repo)
	require.NoError(t, err)
	return scope
}

func TestStagedTreeClashes(t *testing.T) {
	cases := []struct {
		name    string
		files   []string
		removed []string
		want    []stagedClash
	}{
		{name: "nothing written", removed: []string{"a/b"}},
		{name: "edits and new files", files: []string{"a/b", "a/x/one", "a/x/new", "n/deep/file", "root"}},
		{name: "file over a folder", files: []string{"a/x"},
			want: []stagedClash{{path: "a/x", staged: true, left: []string{"a/x/one", "a/x/sub/deep", "a/x/two"}}}},
		{name: "file over a folder with some deletions", files: []string{"a/x"}, removed: []string{"a/x/one", "a/x/two"},
			want: []stagedClash{{path: "a/x", staged: true, left: []string{"a/x/sub/deep"}}}},
		{name: "file over a root folder", files: []string{"top"},
			want: []stagedClash{{path: "top", staged: true, left: []string{"top/f"}}}},
		{name: "file over a fully deleted folder", files: []string{"a/x", "top"}, removed: []string{"a/x/one", "a/x/two", "a/x/sub/deep", "top/f"}},
		{name: "file below a tracked file", files: []string{"a/b/c/d", "a/b/e"},
			want: []stagedClash{{path: "a/b", below: "a/b/c/d"}}},
		{name: "file below a root tracked file", files: []string{"k/new"},
			want: []stagedClash{{path: "k", below: "k/new"}}},
		{name: "file below a deleted tracked file", files: []string{"a/b/c/d", "k/new"}, removed: []string{"a/b", "k"}},
		{name: "file and a file below it", files: []string{"n/p", "n/p/q"},
			want: []stagedClash{{path: "n/p", below: "n/p/q", staged: true}}},
		{name: "several at once", files: []string{"a/x", "k/new", "a/b/c"},
			want: []stagedClash{
				{path: "a/b", below: "a/b/c"},
				{path: "a/x", staged: true, left: []string{"a/x/one", "a/x/sub/deep", "a/x/two"}},
				{path: "k", below: "k/new"},
			}},
	}
	for _, tc := range cases {
		for _, full := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/full=%v", tc.name, full), func(t *testing.T) {
				fx := newShelfFixture(t, clashTracked)
				got, err := stagedTreeClashes(clashScope(t, fx, full), tc.files, tc.removed)
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			})
		}
	}

	t.Run("an empty repository is not probed", func(t *testing.T) {
		fx := newShelfFixture(t, map[string]string{})
		counting := &readCountingRepo{LocalRepo: fx.lr}
		scope, err := newTrackedScope(counting)
		require.NoError(t, err)
		got, err := stagedTreeClashes(scope, []string{"a/b", "a/c", "d"}, nil)
		require.NoError(t, err)
		require.Empty(t, got)
		require.Zero(t, counting.pathReads)
		require.Zero(t, counting.dirReads)
		require.Zero(t, counting.snapshots)
	})
}

func TestStagedClashError(t *testing.T) {
	err := stagedClashError([]stagedClash{{path: "a/x", staged: true, left: []string{"a/x/one", "a/x/two"}}})
	require.ErrorContains(t, err, `cannot push: a path cannot be both a file and a folder`)
	require.ErrorContains(t, err, `"a/x" is staged as a file, but the tracked files below it are not staged for deletion: a/x/one, a/x/two`)
	var userErr *domain.Error
	require.True(t, errors.As(err, &userErr))
	require.Equal(t, 400, userErr.Code)
	require.Equal(t, "nipa add a/x", userErr.Action)
	require.Contains(t, userErr.Hint, "stage the deletions")

	err = stagedClashError([]stagedClash{{path: "a/b", below: "a/b/c"}})
	require.ErrorContains(t, err, `"a/b/c" is staged below "a/b", a tracked file that is not staged for deletion`)
	require.True(t, errors.As(err, &userErr))
	require.Equal(t, "nipa add a/b", userErr.Action)

	err = stagedClashError([]stagedClash{{path: "n/p", below: "n/p/q", staged: true}})
	require.ErrorContains(t, err, `"n/p" is staged as a file and "n/p/q" is staged below it`)
	require.True(t, errors.As(err, &userErr))
	require.Contains(t, userErr.Hint, "unstage")

	var many []stagedClash
	var left []string
	for i := 0; i < stagedClashLimit+3; i++ {
		many = append(many, stagedClash{path: fmt.Sprintf("d%02d", i), below: fmt.Sprintf("d%02d/f", i)})
		left = append(left, fmt.Sprintf("big/%02d", i))
	}
	err = stagedClashError(many)
	require.ErrorContains(t, err, "(and 3 more)")
	require.NotContains(t, err.Error(), `"d20"`)
	err = stagedClashError([]stagedClash{{path: "big", staged: true, left: left}})
	require.ErrorContains(t, err, "big/19 (and 3 more)")
}

// A push that would leave a path as a file and as a folder is refused before
// anything is read, uploaded or changed, for the plan and the push alike.
func TestPush_RefusesAFileFolderClashBeforeUploading(t *testing.T) {
	ctx := context.Background()
	fx := newShelfFixture(t, clashTracked)
	require.NoError(t, os.RemoveAll(filepath.Join(fx.root, "a", "x")))
	writeRepoFile(t, fx.root, "a/x", "a file now\n")
	// Staged behind add's back: only the new file, not the deletions.
	require.NoError(t, fx.lr.StageAddMany([]string{"a/x"}))
	before, err := fx.lr.Snapshot()
	require.NoError(t, err)

	client := &clashPushClient{}
	for _, repo := range []pushLocalRepo{fx.lr, fullLoadRepo{fx.lr}} {
		push := newTestPush(t, repo, client)
		_, err = push.Plan(ctx, fx.root)
		require.ErrorContains(t, err, `"a/x" is staged as a file`)
		err = push.Run(ctx, fx.root, "swap")
		require.ErrorContains(t, err, `"a/x" is staged as a file`)
		require.ErrorContains(t, err, "a/x/one, a/x/sub/deep, a/x/two")
		require.Zero(t, client.pushes, "nothing reaches the server")
		require.Zero(t, client.uploads, "nothing is uploaded")
	}

	require.Equal(t, []string{"a/x"}, stagedOf(t, fx), "the staged list is kept")
	after, err := fx.lr.Snapshot()
	require.NoError(t, err)
	require.Equal(t, before, after, "the stored tree is untouched")

	// Staging the deletions the hint names makes it a push.
	require.NoError(t, fx.wc.Add(ctx, []string{"a/x"}))
	plan, err := newTestPush(t, fx.lr, client).Plan(ctx, fx.root)
	require.NoError(t, err)
	var changes []string
	for _, c := range plan.Changes {
		changes = append(changes, c.Status+" "+c.Path)
	}
	require.Equal(t, []string{"A a/x", "D a/x/one", "D a/x/sub/deep", "D a/x/two"}, changes)
}

// clashPushClient counts what reaches the server.
type clashPushClient struct {
	pushes, uploads int
}

func (c *clashPushClient) Connect(context.Context, string) error { return nil }

func (c *clashPushClient) Push(context.Context, string, string, string, string, string, []*serverDomain.PushFile, []string, string, string) (*serverDomain.PushResult, error) {
	c.pushes++
	return nil, errors.New("unexpected push")
}

func (c *clashPushClient) UploadChunks(_ context.Context, _ domain.ChunkScope, chunks []*serverDomain.ChunkData, _ ...func(*serverDomain.ChunkData)) (int, int, error) {
	c.uploads++
	return len(chunks), 0, nil
}

func (c *clashPushClient) GetTreeNodeManifest(context.Context, string, string, string, []string) (*serverDomain.TreeNode, error) {
	return nil, errors.New("unexpected manifest fetch")
}

func TestCheckWriteLayout(t *testing.T) {
	require.NoError(t, checkWriteLayout(nil))
	require.NoError(t, checkWriteLayout([]string{"a/x"}))
	require.NoError(t, checkWriteLayout([]string{"a/x", "a/xy", "a/x.d/f", "b/c/d", "b/c/e", "top"}))

	err := checkWriteLayout([]string{"keep", "a/x/one", "a/x", "a/x/sub/deep", "top/f", "top"})
	require.ErrorContains(t, err, `it holds "a/x", "top" as both a file and a folder`)
	require.ErrorContains(t, err, "Nothing was written")
	var userErr *domain.Error
	require.True(t, errors.As(err, &userErr))
	require.Equal(t, 400, userErr.Code)

	var paths []string
	for i := 0; i < stagedClashLimit+2; i++ {
		paths = append(paths, fmt.Sprintf("d%02d", i), fmt.Sprintf("d%02d/f", i))
	}
	err = checkWriteLayout(paths)
	require.ErrorContains(t, err, `"d19" (and 2 more)`)
}

func TestMarkKeptTracked(t *testing.T) {
	fx := newShelfFixture(t, clashTracked)
	blockers := func() []swapBlocker {
		return []swapBlocker{
			{"a/x/one", "untracked or kept file in a folder that becomes a file"},
			{"a/x/stray", "untracked or kept file in a folder that becomes a file"},
			{"a/b", "untracked or kept file where a folder is needed"},
			{"k", "locally changed"},
		}
	}
	got := markKeptTracked(fx.lr, blockers())
	require.Equal(t, damagedTargetReason, got[0].reason)
	require.Equal(t, "untracked or kept file in a folder that becomes a file", got[1].reason)
	require.Equal(t, damagedTargetReason, got[2].reason)
	require.Equal(t, "locally changed", got[3].reason)
	err := swapConflictError(got)
	require.ErrorContains(t, err, "the target commit is damaged")

	require.Equal(t, blockers(), markKeptTracked(struct{}{}, blockers()), "a repository without by-path reads keeps the reasons")
	only := []swapBlocker{{"k", "locally changed"}}
	require.Equal(t, only, markKeptTracked(fx.lr, only))
	require.NotContains(t, swapConflictError(only).Error(), "damaged")

	closed := newShelfFixture(t, clashTracked)
	require.NoError(t, closed.lr.Close())
	require.Equal(t, blockers(), markKeptTracked(closed.lr, blockers()), "a failed read keeps the reasons")
}
