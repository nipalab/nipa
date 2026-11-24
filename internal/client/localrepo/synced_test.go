package localrepo

import (
	"testing"

	"github.com/stretchr/testify/require"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
	sqlcLocalrepo "github.com/nipalab/nipa/internal/repository/sqlc/localrepo"
)

func newSyncedRepo(t *testing.T) *LocalRepo {
	t.Helper()
	lr := NewLocalRepo()
	require.NoError(t, lr.Init(t.TempDir()))
	t.Cleanup(func() { _ = lr.Close() })
	return lr
}

func syncedTestTree(name string) *domain.TreeNode {
	return &domain.TreeNode{
		Name: "root",
		Hash: domain.Hash{1},
		FileChildren: []*domain.File{{
			Name: name,
			Hash: domain.Hash{2},
			Mode: 2,
		}},
	}
}

func rawSyncedValue(t *testing.T, lr *LocalRepo) string {
	t.Helper()
	value, err := sqlcLocalrepo.New(lr.db).MetaGet(t.Context(), syncedTreeKey)
	require.NoError(t, err)
	return value
}

func TestLoadSyncedTree_AbsentOnFreshRepo(t *testing.T) {
	lr := newSyncedRepo(t)

	got, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Nil(t, got, "a working copy that never recorded a sync reports nothing")
}

func TestSaveSyncedTree_RoundTrip(t *testing.T) {
	lr := newSyncedRepo(t)

	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "abc123", []string{"src", "assets"}))

	got, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, &clientDomain.SyncedTree{CommitID: "abc123", Sparse: []string{"assets", "src"}}, got)
	snapshot, err := lr.Snapshot()
	require.NoError(t, err)
	require.Len(t, snapshot.Files, 1, "the tree is saved with the record")
	require.Equal(t, "a.txt", snapshot.Files[0].Path)
}

func TestSaveSyncedTree_CanonicalEncoding(t *testing.T) {
	lr := newSyncedRepo(t)

	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", []string{"src", "", "assets", "src"}))
	require.Equal(t, `{"commit":"c1","sparse":["assets","src"]}`, rawSyncedValue(t, lr),
		"order, duplicates and empty entries do not change the record")

	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", nil))
	require.Equal(t, `{"commit":"c1","sparse":[]}`, rawSyncedValue(t, lr), "a full checkout has its own encoding")
	full, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, &clientDomain.SyncedTree{CommitID: "c1", Sparse: []string{}}, full)

	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", []string{}))
	require.Equal(t, `{"commit":"c1","sparse":[]}`, rawSyncedValue(t, lr))
}

func TestSaveSyncedTree_ReplacesRecord(t *testing.T) {
	lr := newSyncedRepo(t)

	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", []string{"src"}))
	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("b.txt"), "c2", nil))

	got, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, &clientDomain.SyncedTree{CommitID: "c2", Sparse: []string{}}, got)
}

func TestSaveTree_DropsSyncedRecord(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", []string{"src"}))

	require.NoError(t, lr.SaveTree(syncedTestTree("b.txt")))

	got, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Nil(t, got, "a tree saved without a commit must not keep an older record")
}

func TestSaveSyncedTree_EmptyCommitRecordsNothing(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", nil))

	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("b.txt"), "", nil))

	got, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Nil(t, got)
	snapshot, err := lr.Snapshot()
	require.NoError(t, err)
	require.Equal(t, "b.txt", snapshot.Files[0].Path)
}

func TestLoadSyncedTree_UnreadableRecordIsAbsent(t *testing.T) {
	lr := newSyncedRepo(t)
	q := sqlcLocalrepo.New(lr.db)

	for _, value := range []string{"not json", `{"sparse":["src"]}`, ""} {
		require.NoError(t, q.MetaSet(t.Context(), sqlcLocalrepo.MetaSetParams{Key: syncedTreeKey, Value: value}))
		got, err := lr.LoadSyncedTree()
		require.NoError(t, err)
		require.Nil(t, got, "value %q", value)
	}
}

func TestSyncedTree_NotInitialized(t *testing.T) {
	lr := NewLocalRepo()

	_, err := lr.LoadSyncedTree()
	require.EqualError(t, err, "local repo not initialized")
	require.EqualError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", nil), "local repo not initialized")
	require.EqualError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "", nil), "local repo not initialized")
	require.EqualError(t, lr.ClearSyncedTree(), "local repo not initialized")
}

func TestSyncedTree_ClosedDatabase(t *testing.T) {
	lr := newSyncedRepo(t)
	db := lr.db
	require.NoError(t, db.Close())

	_, err := lr.LoadSyncedTree()
	require.Error(t, err)
	require.Error(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", nil))
	require.Error(t, lr.ClearSyncedTree())
}

func TestClearSyncedTree_DropsRecordKeepsTree(t *testing.T) {
	lr := newSyncedRepo(t)
	require.NoError(t, lr.ClearSyncedTree(), "clearing an absent record is fine")
	require.NoError(t, lr.SaveSyncedTree(syncedTestTree("a.txt"), "c1", []string{"src"}))

	require.NoError(t, lr.ClearSyncedTree())
	got, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Nil(t, got)
	snap, err := lr.Snapshot()
	require.NoError(t, err)
	require.Len(t, snap.Files, 1, "the stored tree rows stay")
}
