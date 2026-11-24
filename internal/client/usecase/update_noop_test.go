package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const noopURL = "http://example.com/org/project"

var noopHead = snow.ID(42)

// upToDateStubs is a working copy that holds the branch head with the
// configured sparse set, and a server whose branch points at that head.
func upToDateStubs(sparse ...string) (*stubLocalRepo, *stubUpdateClient) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: noopURL, Branch: "main", Sparse: sparse},
		snapshot:   &domain.Snapshot{},
		loadCommit: &domain.LocalCommit{CommitID: noopHead.Base36()},
		syncedTree: &domain.SyncedTree{CommitID: noopHead.Base36(), Sparse: sparse},
	}
	client := &stubUpdateClient{
		manifest:   &serverDomain.TreeNode{Name: "root"},
		branchInfo: &serverDomain.Branch{Name: "main", CommitID: &noopHead},
	}
	return local, client
}

func requireNoSync(t *testing.T, local *stubLocalRepo, client *stubUpdateClient) {
	t.Helper()
	require.Zero(t, client.manifestCalls, "no branch manifest may be fetched")
	require.Zero(t, client.commitManifestCalls, "no commit manifest may be fetched")
	require.Zero(t, local.snapshotCalls, "the working copy must not be scanned")
	require.Zero(t, local.treeSaves, "the stored tree must not be rewritten")
	require.Empty(t, local.savedCommitID, "the commit must not be rewritten")
}

func TestUpdate_Run_UpToDateSkipsEverything(t *testing.T) {
	local, client := upToDateStubs()
	updater := newTestUpdate(t, local, client)

	result, err := updater.RunResult(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.True(t, result.UpToDate)
	require.Equal(t, "main", client.branchLookup, "the branch head is still resolved")
	requireNoSync(t, local, client)

	require.NoError(t, updater.Run(context.Background(), t.TempDir()))
	requireNoSync(t, local, client)
}

func TestUpdate_Run_UpToDateIgnoresSparseOrder(t *testing.T) {
	local, client := upToDateStubs("src", "assets")
	local.syncedTree.Sparse = []string{"assets", "src"}

	result, err := newTestUpdate(t, local, client).RunResult(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.True(t, result.UpToDate)
	requireNoSync(t, local, client)
}

func TestUpdate_Run_FullPathWhenAnyConditionFails(t *testing.T) {
	other := snow.ID(7)
	cases := map[string]func(local *stubLocalRepo, client *stubUpdateClient, u *Update) *Update{
		"head differs from the local commit": func(_ *stubLocalRepo, client *stubUpdateClient, u *Update) *Update {
			client.branchInfo = &serverDomain.Branch{Name: "main", CommitID: &other}
			return u
		},
		"no local commit": func(local *stubLocalRepo, _ *stubUpdateClient, u *Update) *Update {
			local.loadCommit = &domain.LocalCommit{}
			return u
		},
		"sparse set changed": func(local *stubLocalRepo, _ *stubUpdateClient, u *Update) *Update {
			local.loadConfig.Sparse = []string{"src"}
			return u
		},
		"sparse set disabled": func(local *stubLocalRepo, _ *stubUpdateClient, u *Update) *Update {
			local.syncedTree.Sparse = []string{"src"}
			return u
		},
		"no synced record": func(local *stubLocalRepo, _ *stubUpdateClient, u *Update) *Update {
			local.syncedTree = nil
			return u
		},
		"stored tree belongs to another commit": func(local *stubLocalRepo, _ *stubUpdateClient, u *Update) *Update {
			local.syncedTree.CommitID = other.Base36()
			return u
		},
		"forced": func(_ *stubLocalRepo, _ *stubUpdateClient, u *Update) *Update {
			return u.WithForce(true)
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			local, client := upToDateStubs()
			updater := breakIt(local, client, newTestUpdate(t, local, client))
			wantHead := client.branchInfo.CommitID.Base36()
			wantSparse := local.loadConfig.Sparse

			result, err := updater.RunResult(context.Background(), t.TempDir())
			require.NoError(t, err)
			require.False(t, result.UpToDate)
			require.Equal(t, 1, client.manifestCalls, "the manifest must be fetched")
			require.Equal(t, 1, local.snapshotCalls, "the working copy must be synced")
			require.Equal(t, 1, local.treeSaves, "the tree must be saved")
			require.Equal(t, wantHead, local.savedCommitID)
			require.Equal(t, &domain.SyncedTree{CommitID: wantHead, Sparse: append([]string{}, wantSparse...)}, local.syncedTree,
				"the full path records what it synced")
		})
	}
}

func TestUpdate_Run_EmptyHeadTakesFullPath(t *testing.T) {
	for name, branch := range map[string]*serverDomain.Branch{"branch without commits": {Name: "main"}, "no branch info": nil} {
		t.Run(name, func(t *testing.T) {
			local, client := upToDateStubs()
			client.branchInfo = branch
			local.loadCommit = &domain.LocalCommit{}
			local.syncedTree = &domain.SyncedTree{}

			result, err := newTestUpdate(t, local, client).RunResult(context.Background(), t.TempDir())
			require.NoError(t, err)
			require.False(t, result.UpToDate)
			require.Equal(t, 1, client.manifestCalls)
			require.Equal(t, 1, local.treeSaves)
			require.Nil(t, local.syncedTree, "a tree without a commit records no sync")
			require.Empty(t, local.savedCommitID)
		})
	}
}

func TestUpdate_Run_UpToDateCheckErrors(t *testing.T) {
	wantErr := errors.New("db broken")

	local, client := upToDateStubs()
	local.loadCommitErr = wantErr
	_, err := newTestUpdate(t, local, client).RunResult(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
	requireNoSync(t, local, client)

	local, client = upToDateStubs()
	local.syncedTreeErr = wantErr
	_, err = newTestUpdate(t, local, client).RunResult(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
	requireNoSync(t, local, client)
}

func detachedUpToDateStubs(sparse ...string) (*stubLocalRepo, *stubUpdateClient) {
	local := &stubLocalRepo{
		loadConfig: &domain.Config{
			Url: noopURL, Branch: "main", Sparse: sparse,
			Head: &domain.HeadRef{Kind: domain.HeadKindTag, Name: "v1.0.0"},
		},
		snapshot:   &domain.Snapshot{},
		loadCommit: &domain.LocalCommit{CommitID: "abc123", CommitHash: "deadbeef"},
		syncedTree: &domain.SyncedTree{CommitID: "abc123", Sparse: sparse},
	}
	return local, &stubUpdateClient{commitManifest: &serverDomain.TreeNode{Name: "root"}}
}

func TestUpdate_Run_DetachedUpToDateSkipsEverything(t *testing.T) {
	local, client := detachedUpToDateStubs("src")

	result, err := newTestUpdate(t, local, client).RunResult(context.Background(), t.TempDir())
	require.NoError(t, err)
	require.True(t, result.UpToDate)
	require.Equal(t, "http://example.com", client.connectHost, "the detached no-op still connects, like every update")
	require.Empty(t, client.branchLookup, "a detached update never asks for the branch head")
	requireNoSync(t, local, client)
}

func TestUpdate_Run_DetachedFullPathWhenAnyConditionFails(t *testing.T) {
	cases := map[string]func(local *stubLocalRepo, u *Update) *Update{
		"sparse set changed": func(local *stubLocalRepo, u *Update) *Update {
			local.loadConfig.Sparse = []string{"docs"}
			return u
		},
		"no synced record": func(local *stubLocalRepo, u *Update) *Update {
			local.syncedTree = nil
			return u
		},
		"stored tree belongs to another commit": func(local *stubLocalRepo, u *Update) *Update {
			local.syncedTree.CommitID = "zzz999"
			return u
		},
		"forced": func(_ *stubLocalRepo, u *Update) *Update {
			return u.WithForce(true)
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			local, client := detachedUpToDateStubs("src")
			updater := breakIt(local, newTestUpdate(t, local, client))
			wantSparse := local.loadConfig.Sparse

			result, err := updater.RunResult(context.Background(), t.TempDir())
			require.NoError(t, err)
			require.False(t, result.UpToDate)
			require.Equal(t, 1, client.commitManifestCalls)
			require.Equal(t, "abc123", client.commitTreeLookup)
			require.Equal(t, 1, local.snapshotCalls)
			require.Equal(t, 1, local.treeSaves)
			require.Equal(t, "abc123", local.savedCommitID)
			require.Equal(t, "deadbeef", local.savedCommitHash)
			require.Equal(t, &domain.SyncedTree{CommitID: "abc123", Sparse: wantSparse}, local.syncedTree)
		})
	}
}

func TestUpdate_Run_DetachedUpToDateCheckError(t *testing.T) {
	wantErr := errors.New("db broken")
	local, client := detachedUpToDateStubs()
	local.syncedTreeErr = wantErr

	_, err := newTestUpdate(t, local, client).RunResult(context.Background(), t.TempDir())
	require.ErrorIs(t, err, wantErr)
	requireNoSync(t, local, client)
}

func TestUpdate_Run_DetachedWithoutCommitStillRefuses(t *testing.T) {
	local, client := detachedUpToDateStubs()
	local.loadCommit = &domain.LocalCommit{}
	local.syncedTree = &domain.SyncedTree{}

	_, err := newTestUpdate(t, local, client).RunResult(context.Background(), t.TempDir())
	require.ErrorContains(t, err, "no commit checked out")
}

// The sparse set is only saved by SetSparse; the update after it is what
// fetches or drops paths, so it must never be taken for a no-op.
func TestUpdate_SetSparseThenRunStillFetches(t *testing.T) {
	root := t.TempDir()
	local := &stubLocalRepo{
		config:     domain.Config{Url: noopURL, Branch: "main"},
		snapshot:   &domain.Snapshot{},
		loadCommit: &domain.LocalCommit{CommitID: noopHead.Base36()},
	}
	client := &stubUpdateClient{
		manifest:   &serverDomain.TreeNode{Name: "root"},
		branchInfo: &serverDomain.Branch{Name: "main", CommitID: &noopHead},
	}
	updater := newTestUpdate(t, local, client)
	run := func() UpdateResult {
		t.Helper()
		result, err := updater.RunResult(context.Background(), root)
		require.NoError(t, err)
		return result
	}

	require.False(t, run().UpToDate, "a working copy without a record takes the full path")
	require.True(t, run().UpToDate)
	require.Equal(t, 1, client.manifestCalls)

	require.NoError(t, updater.SetSparse(root, []string{"/src/", "assets"}))
	require.False(t, run().UpToDate, "a changed sparse set must be fetched")
	require.Equal(t, 2, client.manifestCalls)
	require.Equal(t, "src", client.treePath)
	require.True(t, run().UpToDate, "once synced, the new sparse set is up to date")

	require.NoError(t, updater.SetSparse(root, nil))
	require.False(t, run().UpToDate, "disabling sparse checkout must fetch the full tree")
	require.Equal(t, 3, client.manifestCalls)
	require.Empty(t, client.treePath)
}

func TestUpdate_SwitchRecordsSyncedTree(t *testing.T) {
	local, client := upToDateStubs("src")
	local.syncedTree = nil

	require.NoError(t, newTestUpdate(t, local, client).Switch(context.Background(), t.TempDir(), "dev"))
	require.Equal(t, &domain.SyncedTree{CommitID: noopHead.Base36(), Sparse: []string{"src"}}, local.syncedTree)
}

func TestUpdate_SwitchTagRecordsSyncedTree(t *testing.T) {
	local, client := upToDateStubs("src")
	local.syncedTree = nil
	client.tagInfo = &domain.Tag{ID: "9", Name: "v1.0.0", CommitID: "abc123"}
	client.commitManifest = &serverDomain.TreeNode{Name: "root"}

	require.NoError(t, newTestUpdate(t, local, client).SwitchTag(context.Background(), t.TempDir(), "v1.0.0"))
	require.Equal(t, &domain.SyncedTree{CommitID: "abc123", Sparse: []string{"src"}}, local.syncedTree)
}

func TestSameSparseSet(t *testing.T) {
	require.True(t, sameSparseSet(nil, nil))
	require.True(t, sameSparseSet(nil, []string{}))
	require.True(t, sameSparseSet([]string{"a", "b"}, []string{"b", "a", "a", ""}))
	require.False(t, sameSparseSet(nil, []string{"a"}))
	require.False(t, sameSparseSet([]string{"a"}, []string{"a", "b"}))
	require.False(t, sameSparseSet([]string{"a"}, []string{"a/b"}))
}

// Against a real local repository: the no-op leaves the working copy and the
// object cache alone, and a forced update is what restores a deleted file.
func TestUpdate_Run_UpToDateOnRealRepo(t *testing.T) {
	root := t.TempDir()
	lr := &pruneCountingRepo{LocalRepo: localrepo.NewLocalRepo()}
	t.Cleanup(func() { _ = lr.Close() })
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveConfig(domain.Config{Url: noopURL, Branch: "main"}))

	fileHash, chunks, stored, encoding := testEncodedFile(t, "a.txt", "tracked content")
	client := &stubUpdateClient{
		branchInfo: &serverDomain.Branch{Name: "main", CommitID: &noopHead},
		manifest: &serverDomain.TreeNode{
			Name: "root",
			Hash: serverDomain.Hash{1},
			FileChildren: []*serverDomain.File{{
				Name:      "a.txt",
				Mode:      2,
				SizeBytes: int64(len("tracked content")),
				Encoding:  encoding,
				Hash:      fileHash,
				Chunks:    chunks,
			}},
		},
		downloadData: stored,
	}
	updater := newTestUpdate(t, lr, client)

	result, err := updater.RunResult(context.Background(), root)
	require.NoError(t, err)
	require.False(t, result.UpToDate)
	require.Equal(t, 1, lr.prunes, "a real update prunes the cache")
	synced, err := lr.LoadSyncedTree()
	require.NoError(t, err)
	require.Equal(t, &domain.SyncedTree{CommitID: noopHead.Base36(), Sparse: []string{}}, synced)

	require.NoError(t, os.Remove(filepath.Join(root, "a.txt")))
	result, err = updater.RunResult(context.Background(), root)
	require.NoError(t, err)
	require.True(t, result.UpToDate)
	require.Equal(t, 1, client.manifestCalls)
	require.Equal(t, 1, lr.prunes, "the no-op must not sweep the cache")
	require.NoFileExists(t, filepath.Join(root, "a.txt"), "the no-op does not restore a deleted file")

	result, err = updater.WithForce(true).RunResult(context.Background(), root)
	require.NoError(t, err)
	require.False(t, result.UpToDate)
	require.Equal(t, 2, client.manifestCalls)
	require.Equal(t, 2, lr.prunes)
	require.Equal(t, "tracked content", string(readRepoFile(t, root, "a.txt")))

	// A tree saved outside a sync (merge, revert) drops the record, so the
	// next update cannot trust the commit id alone.
	require.NoError(t, lr.SaveTree(client.manifest))
	result, err = updater.RunResult(context.Background(), root)
	require.NoError(t, err)
	require.False(t, result.UpToDate)
	require.Equal(t, 3, client.manifestCalls)
}

func TestPush_Run_RecordsSyncedTree(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "src/a.txt", "hello world")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: noopURL, Branch: "main", Sparse: []string{"src"}},
		snapshot:   &domain.Snapshot{TreeHash: "abc123", Files: []domain.SnapshotFile{{Path: "src/a.txt", Hash: serverDomain.Hash{0x01}}}},
		staged:     []string{"src/a.txt"},
	}
	client := &stubPushClient{
		manifest:   &serverDomain.TreeNode{Name: "root"},
		pushResult: &serverDomain.PushResult{CommitID: noopHead},
	}

	require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "update"))
	require.Equal(t, noopHead.Base36(), local.savedCommitID)
	require.Equal(t, &domain.SyncedTree{CommitID: noopHead.Base36(), Sparse: []string{"src"}}, local.syncedTree,
		"the tree saved after a push is the pushed commit's, so the next update is a no-op")
}

func TestRepo_Clone_RecordsSyncedTree(t *testing.T) {
	auth := NewAuth(nil, &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "secret")}}, nil)
	local := &stubLocalRepo{}
	repo := NewRepo(auth, &stubRepoInterface{
		branch:   &serverDomain.Branch{Name: "main", CommitID: &noopHead},
		manifest: &serverDomain.TreeNode{},
	}, local)

	err := repo.Clone(context.Background(), noopURL, "example.com", "org", "project", "main", []string{"/src"}, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, &domain.SyncedTree{CommitID: noopHead.Base36(), Sparse: []string{"src"}}, local.syncedTree)
}
