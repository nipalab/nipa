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

// expectIndexedLog sets up PathCommitLog on main (head 20) up to the
// unindexed lookup, which returns pending.
func expectIndexedLog(perm *MockpermissionUsecase, repo *MockbranchRepository, filter *PathFilter, pending []UnindexedCommit, pendingErr error) snow.ID {
	headID := snow.ID(20)
	perm.EXPECT().HasProjectAccess(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(true)
	repo.EXPECT().GetBranchByName(gomock.Any(), snow.ID(1), "main").Return(branchAt(headID), nil)
	perm.EXPECT().CompileFilter(gomock.Any(), snow.ID(1), domain.PermissionRead).Return(filter, nil)
	repo.EXPECT().UnindexedPathChangeCommits(gomock.Any(), snow.ID(1), headID, maxPathHistoryScan).Return(pending, pendingErr)
	return headID
}

func TestBranch_PathCommitLog_Unrestricted(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	headID := expectIndexedLog(perm, repo, AllowAllFilter(), nil, nil)
	want := []*domain.CommitLogEntry{{Commit: domain.Commit{ID: headID}}}
	repo.EXPECT().PathChangeLog(gomock.Any(), PathChangeQuery{ProjectID: 1, StartID: headID, ScanLimit: maxPathHistoryScan, Path: "src/a.txt", Limit: 7}).
		Return(want, nil)

	got, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "/src/a.txt/", nil, 7)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestBranch_PathCommitLog_NoMatchIsEmptyNotNil(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	expectIndexedLog(perm, repo, AllowAllFilter(), nil, nil)
	repo.EXPECT().PathChangeLog(gomock.Any(), gomock.Any()).Return(nil, nil)

	got, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestBranch_PathCommitLog_IndexesOldCommits(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	pending := []UnindexedCommit{{ID: 20, TreeID: 102, ParentTreeID: 101}}
	expectIndexedLog(perm, repo, AllowAllFilter(), pending, nil)
	older := &domain.TreeNode{ID: 101, Hash: domain.Hash{1}}
	newer := &domain.TreeNode{ID: 102, Hash: domain.Hash{2}}
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(older, nil)
	repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).Return(newer, nil)
	repo.EXPECT().ListFileMetaByTrees(gomock.Any(), []int64{101, 102}).Return(map[int64][]*domain.File{
		101: {{ID: 1, Name: "a.txt", Hash: domain.Hash{7}, Mode: 0o644}, {ID: 2, Name: "run", Hash: domain.Hash{8}, Mode: 0o644}},
		102: {{ID: 3, Name: "a.txt", Hash: domain.Hash{9}, Mode: 0o644}, {ID: 4, Name: "run", Hash: domain.Hash{8}, Mode: 0o755}},
	}, nil)
	repo.EXPECT().ListTreeChildrenByTrees(gomock.Any(), []int64{101, 102}).Return(map[int64][]*domain.TreeNode{
		102: {{ID: 103, Name: "new", Hash: domain.Hash{3}}},
	}, nil)
	repo.EXPECT().ListFileMetaByTrees(gomock.Any(), []int64{103}).Return(map[int64][]*domain.File{
		103: {{ID: 5, Name: "x", Hash: domain.Hash{4}}},
	}, nil)
	repo.EXPECT().ListTreeChildrenByTrees(gomock.Any(), []int64{103}).Return(nil, nil)
	repo.EXPECT().RecordPathChanges(gomock.Any(), []CommitPathChanges{{CommitID: 20, Changes: []PathChange{
		{Path: "a.txt", Kind: PathChangeFile},
		{Path: "new", Kind: PathChangeDir},
		{Path: "new/x", Kind: PathChangeFile},
		{Path: "run", Kind: PathChangeMode},
	}}}).Return(nil)
	repo.EXPECT().PathChangeLog(gomock.Any(), gomock.Any()).Return(nil, nil)

	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "", nil, 0)
	require.NoError(t, err)
}

func TestBranch_PathCommitLog_IndexErrors(t *testing.T) {
	wantErr := errors.New("db down")
	pending := []UnindexedCommit{{ID: 20, TreeID: 102, ParentTreeID: 101}}
	cases := map[string]func(repo *MockbranchRepository) []UnindexedCommit{
		"lookup": func(repo *MockbranchRepository) []UnindexedCommit { return nil },
		"parent tree": func(repo *MockbranchRepository) []UnindexedCommit {
			repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(nil, wantErr)
			return pending
		},
		"tree": func(repo *MockbranchRepository) []UnindexedCommit {
			repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Hash: domain.Hash{1}}, nil)
			repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).Return(nil, wantErr)
			return pending
		},
		"compare": func(repo *MockbranchRepository) []UnindexedCommit {
			repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(&domain.TreeNode{ID: 101, Hash: domain.Hash{1}}, nil)
			repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).Return(&domain.TreeNode{ID: 102, Hash: domain.Hash{2}}, nil)
			repo.EXPECT().ListFileMetaByTrees(gomock.Any(), gomock.Any()).Return(nil, wantErr)
			return pending
		},
		"record": func(repo *MockbranchRepository) []UnindexedCommit {
			same := &domain.TreeNode{ID: 101, Hash: domain.Hash{1}}
			repo.EXPECT().GetTreeNode(gomock.Any(), int64(101)).Return(same, nil)
			repo.EXPECT().GetTreeNode(gomock.Any(), int64(102)).Return(same, nil)
			repo.EXPECT().RecordPathChanges(gomock.Any(), []CommitPathChanges{{CommitID: 20}}).Return(wantErr)
			return pending
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			perm, repo, uc := newHistoryTestBranch(t)
			pending := setup(repo)
			var lookupErr error
			if name == "lookup" {
				lookupErr = wantErr
			}
			expectIndexedLog(perm, repo, AllowAllFilter(), pending, lookupErr)
			_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 0)
			require.ErrorIs(t, err, wantErr)
		})
	}
}

// unchangedPending returns n unindexed commits at depths from..from+n-1
// whose trees are identical, so indexing them compares nothing and records
// empty change sets.
func unchangedPending(repo *MockbranchRepository, from, n int) []UnindexedCommit {
	same := &domain.TreeNode{ID: 101, Hash: domain.Hash{1}}
	repo.EXPECT().GetTreeNode(gomock.Any(), gomock.Any()).Return(same, nil).AnyTimes()
	out := make([]UnindexedCommit, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, UnindexedCommit{ID: snow.ID(5000 - from - i), TreeID: 101, ParentTreeID: 101, Depth: from + i})
	}
	return out
}

func logEntries(n int) []*domain.CommitLogEntry {
	parent := snow.ID(1)
	out := make([]*domain.CommitLogEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &domain.CommitLogEntry{Commit: domain.Commit{ID: snow.ID(9000 - i), Parent1ID: &parent}})
	}
	return out
}

// TestBranch_PathCommitLog_IndexesInGrowingBatches checks that a walk whose
// answer needs every commit is indexed newest first in batches of 100, 200,
// 400... (one write each), with the complete part of the walk asked before
// each batch, and the whole walk asked last.
func TestBranch_PathCommitLog_IndexesInGrowingBatches(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	pending := unchangedPending(repo, 0, 350)
	headID := expectIndexedLog(perm, repo, AllowAllFilter(), pending, nil)
	var recorded [][]snow.ID
	repo.EXPECT().RecordPathChanges(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, commits []CommitPathChanges) error {
		ids := make([]snow.ID, 0, len(commits))
		for _, c := range commits {
			require.Empty(t, c.Changes)
			ids = append(ids, c.CommitID)
		}
		recorded = append(recorded, ids)
		return nil
	}).Times(3)
	query := PathChangeQuery{ProjectID: 1, StartID: headID, Path: "a.txt", Limit: 5}
	var scans []int
	want := logEntries(2)
	repo.EXPECT().PathChangeLog(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, q PathChangeQuery) ([]*domain.CommitLogEntry, error) {
		got := q
		got.ScanLimit = 0
		require.Equal(t, query, got)
		scans = append(scans, q.ScanLimit)
		return want[:1], nil
	}).Times(2)
	repo.EXPECT().PathChangeLog(gomock.Any(), PathChangeQuery{ProjectID: 1, StartID: headID, ScanLimit: maxPathHistoryScan, Path: "a.txt", Limit: 5}).Return(want, nil)

	got, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 5)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, []int{100, 300}, scans, "the walk cut at each next unindexed commit")
	require.Len(t, recorded, 3)
	require.Len(t, recorded[0], 100)
	require.Len(t, recorded[1], 200)
	require.Len(t, recorded[2], 50)
	require.Equal(t, pending[0].ID, recorded[0][0], "newest first")
	require.Equal(t, pending[349].ID, recorded[2][49])
}

// TestBranch_PathCommitLog_BatchCapsAtMax checks the batch size stops
// doubling at pathIndexMaxBatch.
func TestBranch_PathCommitLog_BatchCapsAtMax(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	pending := unchangedPending(repo, 0, 100+200+400+800+1600+1600+5)
	expectIndexedLog(perm, repo, AllowAllFilter(), pending, nil)
	var sizes []int
	repo.EXPECT().RecordPathChanges(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, commits []CommitPathChanges) error {
		sizes = append(sizes, len(commits))
		return nil
	}).AnyTimes()
	repo.EXPECT().PathChangeLog(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 5)
	require.NoError(t, err)
	require.Equal(t, []int{100, 200, 400, 800, 1600, 1600, 5}, sizes)
}

// TestBranch_PathCommitLog_StopsWhenPageIsComplete checks that indexing stops
// as soon as the complete part of the walk holds the page: before any batch
// when the unindexed commits start deep enough, and after the first batch.
func TestBranch_PathCommitLog_StopsWhenPageIsComplete(t *testing.T) {
	t.Run("before indexing", func(t *testing.T) {
		perm, repo, uc := newHistoryTestBranch(t)
		pending := unchangedPending(repo, 40, 500)
		headID := expectIndexedLog(perm, repo, AllowAllFilter(), pending, nil)
		want := logEntries(3)
		repo.EXPECT().PathChangeLog(gomock.Any(), PathChangeQuery{ProjectID: 1, StartID: headID, ScanLimit: 40, Path: "a.txt", Limit: 3}).Return(want, nil)

		got, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 3)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})
	t.Run("after a batch", func(t *testing.T) {
		perm, repo, uc := newHistoryTestBranch(t)
		pending := unchangedPending(repo, 0, 500)
		headID := expectIndexedLog(perm, repo, AllowAllFilter(), pending, nil)
		repo.EXPECT().RecordPathChanges(gomock.Any(), gomock.Len(100)).Return(nil)
		want := logEntries(3)
		repo.EXPECT().PathChangeLog(gomock.Any(), PathChangeQuery{ProjectID: 1, StartID: headID, ScanLimit: 100, Path: "a.txt", Limit: 3}).Return(want, nil)

		got, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 3)
		require.NoError(t, err)
		require.Equal(t, want, got)
	})
	t.Run("restricted reader", func(t *testing.T) {
		perm, repo, uc := newHistoryTestBranch(t)
		restricted := NewTestPathFilter([]*domain.PBACRule{{PathPrefix: "pub", Permission: domain.PermissionRead}}, nil)
		pending := unchangedPending(repo, 0, 500)
		headID := expectIndexedLog(perm, repo, restricted, pending, nil)
		repo.EXPECT().RecordPathChanges(gomock.Any(), gomock.Len(100)).Return(nil)
		candidates := logEntries(3)
		// The cut walk's candidates are filtered: only two are visible, so
		// the next batch is indexed and the next cut holds the page.
		repo.EXPECT().PathChangeLog(gomock.Any(), PathChangeQuery{ProjectID: 1, StartID: headID, ScanLimit: 100, Path: "pub", Limit: 100}).Return(candidates, nil)
		repo.EXPECT().PathChangeRows(gomock.Any(), gomock.Any(), []snow.ID{9000, 8999, 8998}).Return([]PathChangeRow{
			{CommitID: 9000, Path: "pub/a", Kind: PathChangeFile},
			{CommitID: 8999, Path: "secret/b", Kind: PathChangeFile},
			{CommitID: 8998, Path: "pub/c", Kind: PathChangeFile},
		}, nil)
		repo.EXPECT().RecordPathChanges(gomock.Any(), gomock.Len(200)).Return(nil)
		deeper := logEntries(4)
		repo.EXPECT().PathChangeLog(gomock.Any(), PathChangeQuery{ProjectID: 1, StartID: headID, ScanLimit: 300, Path: "pub", Limit: 300}).Return(deeper, nil)
		repo.EXPECT().PathChangeRows(gomock.Any(), gomock.Any(), []snow.ID{9000, 8999, 8998, 8997}).Return([]PathChangeRow{
			{CommitID: 9000, Path: "pub/a", Kind: PathChangeFile},
			{CommitID: 8999, Path: "secret/b", Kind: PathChangeFile},
			{CommitID: 8998, Path: "pub/c", Kind: PathChangeFile},
			{CommitID: 8997, Path: "pub/d", Kind: PathChangeFile},
		}, nil)

		got, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "pub", nil, 3)
		require.NoError(t, err)
		require.Equal(t, []snow.ID{9000, 8998, 8997}, entryIDsOf(got))
	})
}

func TestBranch_PathCommitLog_CutWalkError(t *testing.T) {
	wantErr := errors.New("db down")
	perm, repo, uc := newHistoryTestBranch(t)
	pending := unchangedPending(repo, 10, 5)
	expectIndexedLog(perm, repo, AllowAllFilter(), pending, nil)
	repo.EXPECT().PathChangeLog(gomock.Any(), gomock.Any()).Return(nil, wantErr)
	_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "a.txt", nil, 3)
	require.ErrorIs(t, err, wantErr)
}

func TestBranch_PathCommitLog_LogErrors(t *testing.T) {
	wantErr := errors.New("db down")
	restricted := NewTestPathFilter([]*domain.PBACRule{{PathPrefix: "pub", Permission: domain.PermissionRead}}, nil)
	for name, filter := range map[string]*PathFilter{"unrestricted": AllowAllFilter(), "restricted": restricted} {
		t.Run(name, func(t *testing.T) {
			perm, repo, uc := newHistoryTestBranch(t)
			expectIndexedLog(perm, repo, filter, nil, nil)
			repo.EXPECT().PathChangeLog(gomock.Any(), gomock.Any()).Return(nil, wantErr)
			_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "pub", nil, 0)
			require.ErrorIs(t, err, wantErr)
		})
	}
	t.Run("rows", func(t *testing.T) {
		perm, repo, uc := newHistoryTestBranch(t)
		expectIndexedLog(perm, repo, restricted, nil, nil)
		parent := snow.ID(10)
		repo.EXPECT().PathChangeLog(gomock.Any(), gomock.Any()).
			Return([]*domain.CommitLogEntry{{Commit: domain.Commit{ID: 20, Parent1ID: &parent}}}, nil)
		repo.EXPECT().PathChangeRows(gomock.Any(), gomock.Any(), []snow.ID{20}).Return(nil, wantErr)
		_, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "pub", nil, 0)
		require.ErrorIs(t, err, wantErr)
	})
}

// TestBranch_PathCommitLog_RestrictedReader checks that a reader whose rules
// hide part of a folder only sees the commits that changed what it can read,
// that the root commit is always listed, and that candidates are checked in
// batches until the limit is met.
func TestBranch_PathCommitLog_RestrictedReader(t *testing.T) {
	perm, repo, uc := newHistoryTestBranch(t)
	restricted := NewTestPathFilter([]*domain.PBACRule{
		{PathPrefix: "art/pub", Permission: domain.PermissionRead},
		{PathPrefix: "art/x.txt", Permission: domain.PermissionRead},
	}, nil)
	headID := expectIndexedLog(perm, repo, restricted, nil, nil)
	var candidates []*domain.CommitLogEntry
	rows := map[snow.ID][]PathChangeRow{}
	parent := snow.ID(1)
	for i := 0; i < 120; i++ {
		id := snow.ID(1000 - i)
		candidates = append(candidates, &domain.CommitLogEntry{Commit: domain.Commit{ID: id, Parent1ID: &parent}})
		switch i {
		case 3:
			rows[id] = []PathChangeRow{{CommitID: id, Path: "art/pub/a.png", Kind: PathChangeFile}}
		case 70:
			rows[id] = []PathChangeRow{{CommitID: id, Path: "art/pub/b.png", Kind: PathChangeMode}}
		case 80:
			rows[id] = []PathChangeRow{{CommitID: id, Path: "art", Kind: PathChangeDir}}
		case 90:
			rows[id] = []PathChangeRow{{CommitID: id, Path: "art/secret/c.png", Kind: PathChangeFile}, {CommitID: id, Path: "art/secret", Kind: PathChangeDir}}
		case 100:
			rows[id] = []PathChangeRow{{CommitID: id, Path: "art/hidden.txt", Kind: PathChangeMode}}
		default:
			rows[id] = []PathChangeRow{{CommitID: id, Path: "art/secret/d.png", Kind: PathChangeFile}}
		}
	}
	candidates = append(candidates, &domain.CommitLogEntry{Commit: domain.Commit{ID: 1}})
	repo.EXPECT().PathChangeLog(gomock.Any(), PathChangeQuery{ProjectID: 1, StartID: headID, ScanLimit: maxPathHistoryScan, Path: "art", Limit: maxPathHistoryScan}).
		Return(candidates, nil)
	repo.EXPECT().PathChangeRows(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ PathChangeQuery, ids []snow.ID) ([]PathChangeRow, error) {
			require.LessOrEqual(t, len(ids), 50)
			var out []PathChangeRow
			for _, id := range ids {
				out = append(out, rows[id]...)
			}
			return out, nil
		}).Times(3)

	got, err := uc.PathCommitLog(context.Background(), snow.ID(1), "main", "art", nil, 4)
	require.NoError(t, err)
	require.Equal(t, []snow.ID{997, 930, 920, 1}, entryIDsOf(got))
}

func entryIDsOf(entries []*domain.CommitLogEntry) []snow.ID {
	out := make([]snow.ID, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func TestVisiblePathChange(t *testing.T) {
	filter := NewTestPathFilter([]*domain.PBACRule{
		{PathPrefix: "a/pub", Permission: domain.PermissionRead},
		{PathPrefix: "top.txt", Permission: domain.PermissionRead},
	}, nil)
	row := func(path string, kind PathChangeKind) []PathChangeRow {
		return []PathChangeRow{{Path: path, Kind: kind}}
	}
	require.True(t, visiblePathChange("top.txt", row("top.txt", PathChangeFile), filter))
	require.False(t, visiblePathChange("top.txt", row("top.txt", PathChangeMode), filter), "a file's history ignores mode-only changes")
	require.False(t, visiblePathChange("hidden.txt", row("hidden.txt", PathChangeFile), filter))
	require.True(t, visiblePathChange("a", row("a", PathChangeDir), filter), "a folder appearing shows even when its content is hidden")
	require.True(t, visiblePathChange("a", row("a/pub/x", PathChangeMode), filter), "a mode class change changes the folder")
	require.False(t, visiblePathChange("a", row("a/pub", PathChangeDir), filter), "a nested folder counts through its files only")
	require.False(t, visiblePathChange("a", row("a/secret/x", PathChangeFile), filter))
	require.True(t, visiblePathChange("", row("a/pub/deep/x", PathChangeFile), filter))
	require.False(t, visiblePathChange("", nil, filter))
}

func TestTreeChangesSince(t *testing.T) {
	v1 := fileState{present: true, hash: domain.Hash{1}, class: domain.FileModeClassReadWrite}
	v2 := fileState{present: true, hash: domain.Hash{2}, class: domain.FileModeClassReadWrite}
	exec := fileState{present: true, hash: domain.Hash{1}, class: domain.FileModeClassExecutable}
	absent := fileState{}

	// Two builds on the same base: prev changed a and b and created folder
	// d; cur changes a again, leaves b alone (back to the base), flips c's
	// mode and leaves d alone (absent in the base).
	prev := newTreeChanges()
	prev.files["a"] = [2]fileState{v1, v2}
	prev.files["b"] = [2]fileState{absent, v1}
	prev.files["same"] = [2]fileState{v1, v1}
	prev.dirs["d"] = [2]bool{false, true}
	prev.dirs["kept"] = [2]bool{true, true}
	cur := newTreeChanges()
	cur.files["a"] = [2]fileState{v1, v1}
	cur.files["c"] = [2]fileState{v1, exec}
	cur.dirs["e"] = [2]bool{true, false}
	cur.dirs["kept"] = [2]bool{true, true}

	require.Equal(t, []PathChange{
		{Path: "a", Kind: PathChangeFile},
		{Path: "b", Kind: PathChangeFile},
		{Path: "c", Kind: PathChangeMode},
		{Path: "d", Kind: PathChangeDir},
		{Path: "e", Kind: PathChangeDir},
	}, cur.since(prev))
	require.Equal(t, []PathChange{
		{Path: "c", Kind: PathChangeMode},
		{Path: "e", Kind: PathChangeDir},
	}, cur.since(nil))
}

func TestPathChangeQuery_PrefixRange(t *testing.T) {
	from, to := PathChangeQuery{Path: "a/b"}.PrefixRange()
	require.Equal(t, "a/b/", from)
	require.Equal(t, "a/b0", to)
	require.Less(t, "a/b/zzz", to)
	require.Greater(t, "a/b.txt", "a/b", "a sibling sorts outside [a/b/, a/b0) only because '.' < '/'")
	require.Less(t, "a/b.txt", from)
	from, to = PathChangeQuery{}.PrefixRange()
	require.Empty(t, from)
	require.Empty(t, to)
	require.True(t, PathChangeQuery{}.AnyPath())
}
