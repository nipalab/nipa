package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/merge"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func newTestRevertWithAuth(t *testing.T, client *stubRevertClient, local *stubLocalRepo, auth *Auth) (*Revert, *stubPushClient) {
	t.Helper()
	pushClient := &stubPushClient{manifest: &serverDomain.TreeNode{Name: "root"}}
	push := NewPush(auth, pushClient, local)
	return NewRevert(auth, client, local, push), pushClient
}

func failingAuth(t *testing.T) *Auth {
	t.Helper()
	storage := &stubSecureStorage{loadErr: errors.New("not found")}
	return NewAuth(&stubLoginExecutor{}, storage, &stubUserInput{err: errors.New("login cancelled")})
}

type cleanRevertFixture struct {
	root       string
	client     *stubRevertClient
	local      *stubLocalRepo
	revert     *Revert
	pushClient *stubPushClient
	head       *serverDomain.TreeNode
}

func newCleanRevertFixture(t *testing.T) *cleanRevertFixture {
	t.Helper()
	root := t.TempDir()
	writeRepoFile(t, root, "a.txt", "v2")
	v1 := testFile(t, "a.txt", "v1")
	v2 := testFile(t, "a.txt", "v2")
	head := testTree(0xaa, v2.file)
	client := &stubRevertClient{
		details: map[string]*domain.CommitDetail{
			"2": {ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v2.file)},
			"1": {ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)},
		},
		headTree: head,
	}
	local := &stubLocalRepo{loadConfig: revertConfig(), snapshot: snapshotOf(head)}
	storeBlobs(local, v1)
	revert, pushClient := newTestRevert(t, client, local)
	return &cleanRevertFixture{
		root:       root,
		client:     client,
		local:      local,
		revert:     revert,
		pushClient: pushClient,
		head:       head,
	}
}

func TestRevert_Run_ErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("init error", func(t *testing.T) {
		local := &stubLocalRepo{initErr: errors.New("init failed")}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.Run(ctx, t.TempDir(), "2", RevertOptions{})
		require.ErrorContains(t, err, "init failed")
	})

	t.Run("config error", func(t *testing.T) {
		local := &stubLocalRepo{configLoadErr: errors.New("config failed")}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.Run(ctx, t.TempDir(), "2", RevertOptions{})
		require.ErrorContains(t, err, "config failed")
	})

	t.Run("invalid url", func(t *testing.T) {
		local := &stubLocalRepo{loadConfig: &domain.Config{Url: "not a url", Branch: "main"}}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.Run(ctx, t.TempDir(), "2", RevertOptions{})
		require.Error(t, err)
	})

	t.Run("invalid mainline", func(t *testing.T) {
		local := &stubLocalRepo{loadConfig: revertConfig()}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.Run(ctx, t.TempDir(), "2", RevertOptions{Mainline: 3})
		require.ErrorContains(t, err, "--mainline must be 1 or 2")
	})

	t.Run("connect error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.connectErr = errors.New("connect failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "connect failed")
	})

	t.Run("login error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		revert, _ := newTestRevertWithAuth(t, f.client, f.local, failingAuth(t))
		_, err := revert.Run(ctx, f.root, "2", RevertOptions{})
		require.Error(t, err)
	})

	t.Run("target commit error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.detailErr = errors.New("commit lookup failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "commit lookup failed")
	})

	t.Run("target commit missing", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.details = map[string]*domain.CommitDetail{"2": nil}
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "not found")
	})

	t.Run("manifest error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.headErr = errors.New("manifest failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "manifest failed")
	})

	t.Run("empty head tree", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.headTree = nil
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "no commits to revert onto")
	})

	t.Run("snapshot error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.snapshotErr = errors.New("snapshot failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "snapshot failed")
	})

	t.Run("pinned commit error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.loadCommitErr = errors.New("pin failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "pin failed")
	})

	t.Run("save state error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.saveRevertErr = errors.New("save state failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "save state failed")
	})

	t.Run("pending merge state error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.mergeStateErr = errors.New("merge state failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "merge state failed")
	})

	t.Run("pending revert state error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.revertStateErr = errors.New("revert state failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "revert state failed")
	})

	t.Run("staged list error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.stagedErr = errors.New("staged failed")
		_, err := f.revert.Run(ctx, f.root, "2", RevertOptions{})
		require.ErrorContains(t, err, "staged failed")
	})
}

func TestRevert_Run_RangeErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("missing range side", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		_, err := f.revert.Run(ctx, f.root, "2..", RevertOptions{})
		require.ErrorContains(t, err, "<from>..<to>")
	})

	t.Run("walk error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.walkErr = errors.New("walk failed")
		_, err := f.revert.Run(ctx, f.root, "1..2", RevertOptions{})
		require.ErrorContains(t, err, "walk failed")
	})

	t.Run("empty range", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		_, err := f.revert.Run(ctx, f.root, "1..2", RevertOptions{})
		require.ErrorContains(t, err, "no commits in the revert range")
	})

	t.Run("range too large", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		entries := make([]*domain.CommitWalkEntry, revertMaxTargets+1)
		for i := range entries {
			entries[i] = &domain.CommitWalkEntry{ID: "x"}
		}
		f.client.walkEntries = entries
		_, err := f.revert.Run(ctx, f.root, "1..2", RevertOptions{})
		require.ErrorContains(t, err, "too large")
	})

	t.Run("merge commit without mainline", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.walkEntries = []*domain.CommitWalkEntry{{ID: "2", Hash: "h2", Parent2ID: "9", Message: "merge"}}
		_, err := f.revert.Run(ctx, f.root, "1..2", RevertOptions{})
		require.ErrorContains(t, err, "--mainline")
	})

	t.Run("nil walk entry skipped", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.walkEntries = []*domain.CommitWalkEntry{
			nil,
			{ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a"},
		}
		outcome, err := f.revert.Run(ctx, f.root, "1..3", RevertOptions{})
		require.NoError(t, err)
		require.True(t, outcome.Committed)
	})
}

func TestRevert_ResolveTargets_RangeSizeLimit(t *testing.T) {
	ctx := context.Background()
	url := &domain.NipaUrl{Host: "example.com", Org: "org", Project: "project"}

	entries := func(n int) []*domain.CommitWalkEntry {
		out := make([]*domain.CommitWalkEntry, n)
		for i := range out {
			out[i] = &domain.CommitWalkEntry{ID: "c", Hash: "h", Message: "subject"}
		}
		return out
	}

	t.Run("range of exactly the max is allowed", func(t *testing.T) {
		client := &stubRevertClient{walkEntries: entries(revertMaxTargets)}
		revert, _ := newTestRevert(t, client, &stubLocalRepo{loadConfig: revertConfig()})
		refs, err := revert.resolveTargets(ctx, url, "1..2", 0)
		require.NoError(t, err)
		require.Len(t, refs, revertMaxTargets)
		require.Equal(t, revertMaxTargets+1, client.walkLimit, "walk one past the cap to detect oversized ranges")
	})

	t.Run("range over the max is rejected up front", func(t *testing.T) {
		client := &stubRevertClient{walkEntries: entries(revertMaxTargets + 1)}
		revert, _ := newTestRevert(t, client, &stubLocalRepo{loadConfig: revertConfig()})
		_, err := revert.resolveTargets(ctx, url, "1..2", 0)
		require.ErrorContains(t, err, "too large")
		require.Equal(t, revertMaxTargets+1, client.walkLimit)
	})
}

func TestRevert_Process_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	url := &domain.NipaUrl{Host: "example.com", Org: "org", Project: "project"}

	stateFor := func(id, base string) *domain.RevertState {
		return &domain.RevertState{
			Targets:         []domain.CommitRef{{ID: id, Hash: "h" + id, Subject: "subject"}},
			CurrentTreeHash: base,
		}
	}

	t.Run("target commit error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("9", f.head.Hash.String()), merge.Flatten(f.head))
		require.Error(t, err)
	})

	t.Run("parent commit error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.client.details["2"] = &domain.CommitDetail{ID: "2", Hash: "c2hash", Parent1ID: "99", Message: "change a", Tree: testTree(0x02)}
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), merge.Flatten(f.head))
		require.Error(t, err)
	})

	t.Run("snapshot error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.snapshotErr = errors.New("snapshot failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), merge.Flatten(f.head))
		require.ErrorContains(t, err, "snapshot failed")
	})

	t.Run("download error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.missingChunks = []serverDomain.Hash{{0x99}}
		f.client.downloadErr = errors.New("download failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), merge.Flatten(f.head))
		require.ErrorContains(t, err, "download failed")
	})

	t.Run("push error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.pushClient.pushErr = errors.New("push failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), merge.Flatten(f.head))
		require.ErrorContains(t, err, "push failed")
	})

	t.Run("save state after push error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.saveRevertErr = errors.New("save failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), merge.Flatten(f.head))
		require.ErrorContains(t, err, "save failed")
	})

	t.Run("clear state error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.clearRevertErr = errors.New("clear failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), merge.Flatten(f.head))
		require.ErrorContains(t, err, "clear failed")
	})

	t.Run("no change save state error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		v1 := testFile(t, "a.txt", "v1")
		f.client.details["2"] = &domain.CommitDetail{ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v1.file)}
		f.local.saveRevertErr = errors.New("save failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), merge.Flatten(f.head))
		require.ErrorContains(t, err, "save failed")
	})

	t.Run("no-commit save tree error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.treeErr = errors.New("save tree failed")
		state := stateFor("2", f.head.Hash.String())
		state.NoCommit = true
		_, err := f.revert.process(ctx, f.root, url, "main", state, merge.Flatten(f.head))
		require.ErrorContains(t, err, "save tree failed")
	})

	t.Run("no-commit save state error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		f.local.saveRevertErr = errors.New("save failed")
		state := stateFor("2", f.head.Hash.String())
		state.NoCommit = true
		_, err := f.revert.process(ctx, f.root, url, "main", state, merge.Flatten(f.head))
		require.ErrorContains(t, err, "save failed")
	})

	t.Run("conflict save tree error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		ours := conflictFixture(t, f)
		f.local.treeErr = errors.New("save tree failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), ours)
		require.ErrorContains(t, err, "save tree failed")
	})

	t.Run("conflict save state error", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		ours := conflictFixture(t, f)
		f.local.saveRevertErr = errors.New("save failed")
		_, err := f.revert.process(ctx, f.root, url, "main", stateFor("2", f.head.Hash.String()), ours)
		require.ErrorContains(t, err, "save failed")
	})

	t.Run("merge commit mainline one", func(t *testing.T) {
		f := newCleanRevertFixture(t)
		ours := testFile(t, "a.txt", "merge")
		p1 := testFile(t, "a.txt", "one")
		p2 := testFile(t, "a.txt", "two")
		head := testTree(0xaa, ours.file)
		f.client.details["5"] = &domain.CommitDetail{ID: "5", Hash: "m5", Parent1ID: "3", Parent2ID: "4", Message: "merge", Tree: testTree(0x05, ours.file)}
		f.client.details["3"] = &domain.CommitDetail{ID: "3", Hash: "c3", Message: "on main", Tree: testTree(0x03, p1.file)}
		f.client.details["4"] = &domain.CommitDetail{ID: "4", Hash: "c4", Message: "side", Tree: testTree(0x04, p2.file)}
		f.local.snapshot = snapshotOf(head)
		storeBlobs(f.local, ours, p1, p2)
		state := stateFor("5", head.Hash.String())
		state.Mainline = 1

		outcome, err := f.revert.process(ctx, f.root, url, "main", state, merge.Flatten(head))
		require.NoError(t, err)
		require.True(t, outcome.Committed)
		require.Equal(t, "one", string(readRepoFile(t, f.root, "a.txt")))
	})
}

func conflictFixture(t *testing.T, f *cleanRevertFixture) map[string]merge.File {
	t.Helper()
	v1 := testFile(t, "a.txt", "top\nv1\nbottom")
	v2 := testFile(t, "a.txt", "top\nv2\nbottom")
	v3 := testFile(t, "a.txt", "top\nv3\nbottom")
	f.client.details["2"] = &domain.CommitDetail{ID: "2", Hash: "c2hash", Parent1ID: "1", Message: "change a", Tree: testTree(0x02, v2.file)}
	f.client.details["1"] = &domain.CommitDetail{ID: "1", Hash: "c1hash", Message: "add a", Tree: testTree(0x01, v1.file)}
	storeBlobs(f.local, v1, v2, v3)
	return merge.Flatten(testTree(0x03, v3.file))
}

func TestRevert_Abort_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	state := &domain.RevertState{
		Targets:          []domain.CommitRef{{ID: "2"}},
		CurrentTreeHash:  "head",
		OriginalTreeHash: "head",
	}
	abortLocal := func() *stubLocalRepo {
		return &stubLocalRepo{loadConfig: revertConfig(), revertState: state, snapshot: &domain.Snapshot{}}
	}

	t.Run("load state error", func(t *testing.T) {
		local := abortLocal()
		local.revertStateErr = errors.New("load state failed")
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "load state failed")
	})

	t.Run("connect error", func(t *testing.T) {
		client := &stubRevertClient{connectErr: errors.New("connect failed")}
		revert, _ := newTestRevert(t, client, abortLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "connect failed")
	})

	t.Run("login error", func(t *testing.T) {
		revert, _ := newTestRevertWithAuth(t, &stubRevertClient{}, abortLocal(), failingAuth(t))
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.Error(t, err)
	})

	t.Run("manifest error", func(t *testing.T) {
		client := &stubRevertClient{headErr: errors.New("manifest failed")}
		revert, _ := newTestRevert(t, client, abortLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "manifest failed")
	})

	t.Run("sync error", func(t *testing.T) {
		hash := serverDomain.Hash{0x99}
		client := &stubRevertClient{
			headTree:    testTree(0xaa, merge.File{Path: "a.txt", Mode: 0o644, ChunkHashes: []serverDomain.Hash{hash}}),
			downloadErr: errors.New("download failed"),
		}
		local := abortLocal()
		local.missingChunks = []serverDomain.Hash{hash}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "download failed")
	})

	t.Run("save tree error", func(t *testing.T) {
		local := abortLocal()
		local.treeErr = errors.New("save tree failed")
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "save tree failed")
	})

	t.Run("save commit error", func(t *testing.T) {
		local := abortLocal()
		local.saveCommitErr = errors.New("save commit failed")
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "save commit failed")
	})

	t.Run("clear staged error", func(t *testing.T) {
		local := abortLocal()
		local.clearStagedErr = errors.New("clear staged failed")
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "clear staged failed")
	})

	t.Run("clear state error", func(t *testing.T) {
		local := abortLocal()
		local.clearRevertErr = errors.New("clear state failed")
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Abort: true})
		require.ErrorContains(t, err, "clear state failed")
	})
}

func TestRevert_Skip_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	newSkipState := func() *domain.RevertState {
		return &domain.RevertState{
			Targets:          []domain.CommitRef{{ID: "2"}},
			CurrentTreeHash:  "head",
			OriginalTreeHash: "head",
		}
	}
	skipLocal := func() *stubLocalRepo {
		return &stubLocalRepo{loadConfig: revertConfig(), revertState: newSkipState(), snapshot: &domain.Snapshot{}}
	}
	skipClient := func() *stubRevertClient {
		return &stubRevertClient{headTree: testTree(0xaa)}
	}

	t.Run("load state error", func(t *testing.T) {
		local := skipLocal()
		local.revertStateErr = errors.New("load state failed")
		revert, _ := newTestRevert(t, skipClient(), local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "load state failed")
	})

	t.Run("connect error", func(t *testing.T) {
		client := skipClient()
		client.connectErr = errors.New("connect failed")
		revert, _ := newTestRevert(t, client, skipLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "connect failed")
	})

	t.Run("login error", func(t *testing.T) {
		revert, _ := newTestRevertWithAuth(t, skipClient(), skipLocal(), failingAuth(t))
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.Error(t, err)
	})

	t.Run("manifest error", func(t *testing.T) {
		client := skipClient()
		client.headErr = errors.New("manifest failed")
		revert, _ := newTestRevert(t, client, skipLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "manifest failed")
	})

	t.Run("nil head tree", func(t *testing.T) {
		revert, _ := newTestRevert(t, &stubRevertClient{}, skipLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "no commits")
	})

	t.Run("sync error", func(t *testing.T) {
		hash := serverDomain.Hash{0x99}
		client := &stubRevertClient{
			headTree:    testTree(0xaa, merge.File{Path: "a.txt", Mode: 0o644, ChunkHashes: []serverDomain.Hash{hash}}),
			downloadErr: errors.New("download failed"),
		}
		local := skipLocal()
		local.missingChunks = []serverDomain.Hash{hash}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "download failed")
	})

	t.Run("save tree error", func(t *testing.T) {
		local := skipLocal()
		local.treeErr = errors.New("save tree failed")
		revert, _ := newTestRevert(t, skipClient(), local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "save tree failed")
	})

	t.Run("clear staged error", func(t *testing.T) {
		local := skipLocal()
		local.clearStagedErr = errors.New("clear staged failed")
		revert, _ := newTestRevert(t, skipClient(), local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "clear staged failed")
	})

	t.Run("save state error", func(t *testing.T) {
		local := skipLocal()
		local.saveRevertErr = errors.New("save state failed")
		revert, _ := newTestRevert(t, skipClient(), local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "save state failed")
	})

	t.Run("clear state error", func(t *testing.T) {
		local := skipLocal()
		local.clearRevertErr = errors.New("clear state failed")
		revert, _ := newTestRevert(t, skipClient(), local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.ErrorContains(t, err, "clear state failed")
	})

	t.Run("continues remaining targets", func(t *testing.T) {
		head := testTree(0xaa)
		local := skipLocal()
		local.revertState = &domain.RevertState{
			Targets: []domain.CommitRef{
				{ID: "2", Hash: "h2", Subject: "second"},
				{ID: "1", Hash: "h1", Subject: "first"},
			},
			CurrentTreeHash:  "head",
			OriginalTreeHash: "head",
		}
		client := &stubRevertClient{
			headTree: head,
			details: map[string]*domain.CommitDetail{
				"1": {ID: "1", Hash: "h1", Parent1ID: "0", Message: "first", Tree: head},
				"0": {ID: "0", Hash: "h0", Message: "root", Tree: head},
			},
		}
		revert, pushClient := newTestRevert(t, client, local)
		outcome, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Skip: true})
		require.NoError(t, err)
		require.True(t, outcome.NoChange)
		require.Empty(t, pushClient.pushes)
		require.True(t, local.clearedRevert)
	})
}

func TestRevert_Resume_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	newResumeState := func() *domain.RevertState {
		return &domain.RevertState{
			Targets:         []domain.CommitRef{{ID: "2", Hash: "c2hash", Subject: "change a"}},
			CurrentTreeHash: "head",
		}
	}
	resumeLocal := func() *stubLocalRepo {
		return &stubLocalRepo{loadConfig: revertConfig(), revertState: newResumeState(), snapshot: &domain.Snapshot{}}
	}

	t.Run("load state error", func(t *testing.T) {
		local := resumeLocal()
		local.revertStateErr = errors.New("load state failed")
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "load state failed")
	})

	t.Run("connect error", func(t *testing.T) {
		client := &stubRevertClient{connectErr: errors.New("connect failed")}
		revert, _ := newTestRevert(t, client, resumeLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "connect failed")
	})

	t.Run("login error", func(t *testing.T) {
		revert, _ := newTestRevertWithAuth(t, &stubRevertClient{}, resumeLocal(), failingAuth(t))
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.Error(t, err)
	})

	t.Run("no-commit working tree error", func(t *testing.T) {
		local := resumeLocal()
		local.snapshotErr = errors.New("snapshot failed")
		local.revertState.NoCommit = true
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "snapshot failed")
	})

	t.Run("manifest error", func(t *testing.T) {
		client := &stubRevertClient{headErr: errors.New("manifest failed")}
		revert, _ := newTestRevert(t, client, resumeLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "manifest failed")
	})

	t.Run("nil head tree", func(t *testing.T) {
		revert, _ := newTestRevert(t, &stubRevertClient{}, resumeLocal())
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "no commits")
	})

	t.Run("staged list error", func(t *testing.T) {
		local := resumeLocal()
		local.stagedErr = errors.New("staged failed")
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "staged failed")
	})

	t.Run("push error", func(t *testing.T) {
		root := t.TempDir()
		writeRepoFile(t, root, "a.txt", "resolved")
		local := resumeLocal()
		local.staged = []string{"a.txt"}
		revert, pushClient := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		pushClient.pushErr = errors.New("push failed")
		_, err := revert.Run(ctx, root, "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "push failed")
	})

	t.Run("reload manifest error", func(t *testing.T) {
		root := t.TempDir()
		writeRepoFile(t, root, "a.txt", "resolved")
		local := resumeLocal()
		local.staged = []string{"a.txt"}
		client := &stubRevertClient{headTree: testTree(0xaa), headErr: errors.New("reload failed"), headErrOn: 2}
		revert, _ := newTestRevert(t, client, local)
		_, err := revert.Run(ctx, root, "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "reload failed")
	})

	t.Run("save state error", func(t *testing.T) {
		local := resumeLocal()
		local.saveRevertErr = errors.New("save state failed")
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "save state failed")
	})

	t.Run("clear state error", func(t *testing.T) {
		local := resumeLocal()
		local.clearRevertErr = errors.New("clear state failed")
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.ErrorContains(t, err, "clear state failed")
	})

	t.Run("process error", func(t *testing.T) {
		local := resumeLocal()
		local.revertState.Targets = []domain.CommitRef{{ID: "2", Hash: "h2"}, {ID: "9", Hash: "h9"}}
		revert, _ := newTestRevert(t, &stubRevertClient{headTree: testTree(0xaa)}, local)
		_, err := revert.Run(ctx, t.TempDir(), "", RevertOptions{Continue: true})
		require.Error(t, err)
	})

	t.Run("success with remaining targets", func(t *testing.T) {
		root := t.TempDir()
		writeRepoFile(t, root, "a.txt", "v2")
		v1 := testFile(t, "a.txt", "v1")
		v2 := testFile(t, "a.txt", "v2")
		local := resumeLocal()
		local.snapshot = snapshotOf(testTree(0xaa, v2.file))
		local.revertState.Targets = []domain.CommitRef{
			{ID: "3", Hash: "h3", Subject: "third"},
			{ID: "2", Hash: "h2", Subject: "second"},
		}
		client := &stubRevertClient{
			headTree: testTree(0xaa, v2.file),
			details: map[string]*domain.CommitDetail{
				"2": {ID: "2", Hash: "h2", Parent1ID: "1", Message: "second", Tree: testTree(0x02, v2.file)},
				"1": {ID: "1", Hash: "h1", Message: "first", Tree: testTree(0x01, v1.file)},
			},
		}
		storeBlobs(local, v1)
		revert, pushClient := newTestRevert(t, client, local)

		outcome, err := revert.Run(ctx, root, "", RevertOptions{Continue: true})
		require.NoError(t, err)
		require.True(t, outcome.Committed)
		require.Len(t, pushClient.pushes, 1)
		require.True(t, local.clearedRevert)
	})
}

func TestRevert_WorkingTreeFiles(t *testing.T) {
	t.Run("snapshot error", func(t *testing.T) {
		local := &stubLocalRepo{snapshotErr: errors.New("snapshot failed")}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.workingTreeFiles(t.TempDir())
		require.ErrorContains(t, err, "snapshot failed")
	})

	t.Run("missing file skipped", func(t *testing.T) {
		local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "gone.txt"}}}}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		files, err := revert.workingTreeFiles(t.TempDir())
		require.NoError(t, err)
		require.Empty(t, files)
	})

	t.Run("unreadable path error", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "adir"), 0o755))
		local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "adir"}}}}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		_, err := revert.workingTreeFiles(root)
		require.Error(t, err)
	})

	t.Run("reads working files", func(t *testing.T) {
		root := t.TempDir()
		writeRepoFile(t, root, "a.txt", "content")
		local := &stubLocalRepo{snapshot: &domain.Snapshot{Files: []domain.SnapshotFile{{Path: "a.txt", Mode: 0o644}}}}
		revert, _ := newTestRevert(t, &stubRevertClient{}, local)
		files, err := revert.workingTreeFiles(root)
		require.NoError(t, err)
		require.Len(t, files, 1)
		require.Equal(t, int64(len("content")), files["a.txt"].SizeBytes)
	})
}

func TestFirstLine(t *testing.T) {
	require.Equal(t, "subject", firstLine("subject\nbody"))
	require.Equal(t, "subject", firstLine("subject"))
}
