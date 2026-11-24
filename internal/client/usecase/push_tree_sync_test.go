package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const treeSyncURL = "http://example.com/org/project"

var (
	treeSyncCommit = snow.ID(77)
	treeSyncHash   = serverDomain.Hash{0xab, 0xcd}
)

// treeSyncFixture is a working copy with one modified and one deleted staged
// file, both pinned, and a server that accepts the push.
func treeSyncFixture(t *testing.T, sparse []string) (string, *stubLocalRepo, *stubPushClient) {
	t.Helper()
	root := t.TempDir()
	writeRepoFile(t, root, "src/a.txt", "hello world")
	local := &stubLocalRepo{
		loadConfig: &domain.Config{Url: treeSyncURL, Branch: "main", Sparse: sparse},
		snapshot: &domain.Snapshot{TreeHash: "base-tree", Files: []domain.SnapshotFile{
			{Path: "src/a.txt", Hash: serverDomain.Hash{0x01}},
			{Path: "src/gone.txt", Hash: serverDomain.Hash{0x02}},
		}},
		staged:     []string{"src/a.txt", "src/gone.txt"},
		loadCommit: &domain.LocalCommit{CommitID: "basecommit"},
		pins:       []string{"src/a.txt", "src/gone.txt", "other"},
	}
	client := &stubPushClient{
		manifest:   &serverDomain.TreeNode{Name: "root", Hash: treeSyncHash},
		pushResult: &serverDomain.PushResult{CommitID: treeSyncCommit, TreeHash: treeSyncHash},
	}
	return root, local, client
}

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	return recorder
}

// treeSyncAttrs returns the attributes of the push.tree span.
func treeSyncAttrs(t *testing.T, recorder *tracetest.SpanRecorder) map[string]string {
	t.Helper()
	for _, span := range recorder.Ended() {
		if span.Name() != "push.tree" {
			continue
		}
		attrs := map[string]string{}
		for _, kv := range span.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		return attrs
	}
	t.Fatal("no push.tree span recorded")
	return nil
}

func requireLanded(t *testing.T, local *stubLocalRepo) {
	t.Helper()
	require.True(t, local.clearedStaged, "staged entries are cleared whichever way the tree was saved")
	require.ElementsMatch(t, []string{"src/a.txt", "src/gone.txt"}, local.unpinned, "landed exact pins are released, directory pins stay")
	require.Equal(t, treeSyncCommit.Base36(), local.savedCommitID)
}

func TestPush_FullCheckoutAppliesTreeLocally(t *testing.T) {
	recorder := recordSpans(t)
	root, local, client := treeSyncFixture(t, nil)

	require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "update"))

	require.Zero(t, client.manifestCalls, "the manifest is not fetched when the push applies locally")
	require.Zero(t, local.treeSaves)
	require.Len(t, local.appliedPushes, 1)
	applied := local.appliedPushes[0]
	require.Equal(t, client.pushFiles, applied.Files)
	require.Equal(t, []string{"src/gone.txt"}, applied.Removed)
	require.Equal(t, "basecommit", applied.BaseCommitID, "the base is the commit the server checked the push against")
	require.Equal(t, client.baseCommitID, applied.BaseCommitID)
	require.Equal(t, treeSyncCommit.Base36(), applied.CommitID)
	require.Equal(t, treeSyncHash, applied.TreeHash)
	require.Empty(t, applied.Sparse)
	requireLanded(t, local)
	require.Equal(t, map[string]string{"nipa.push.tree_sync": "local"}, treeSyncAttrs(t, recorder))
}

func TestPush_FallsBackToManifestWhenApplyFails(t *testing.T) {
	cases := map[string]struct {
		err    error
		reason string
	}{
		"root hash mismatch": {
			err:    &domain.PushApplyMismatchError{Expected: treeSyncHash, Computed: serverDomain.Hash{0x99}},
			reason: "mismatch",
		},
		"stored tree is not the base": {err: domain.ErrPushApplyBase, reason: "base"},
		"local database error":        {err: errors.New("database is locked"), reason: "error"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := recordSpans(t)
			root, local, client := treeSyncFixture(t, nil)
			local.applyPushErr = tc.err

			require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "update"),
				"the commit landed: a failed local apply must not fail the push")

			require.Len(t, local.appliedPushes, 1)
			require.Equal(t, 1, client.manifestCalls)
			require.Empty(t, client.manifestPaths[0], "a full checkout fetches the whole manifest")
			require.Equal(t, 1, local.treeSaves)
			require.Same(t, client.manifest, local.tree)
			require.Equal(t, &domain.SyncedTree{CommitID: treeSyncCommit.Base36(), Sparse: []string{}}, local.syncedTree)
			requireLanded(t, local)
			require.Equal(t, map[string]string{
				"nipa.push.tree_sync":        "refetch",
				"nipa.push.tree_sync_reason": tc.reason,
			}, treeSyncAttrs(t, recorder))
		})
	}
}

func TestPush_FallbackFailureFailsThePush(t *testing.T) {
	root, local, client := treeSyncFixture(t, nil)
	local.applyPushErr = &domain.PushApplyMismatchError{}
	client.manifestErr = errors.New("manifest failed")

	err := newTestPush(t, local, client).Run(context.Background(), root, "update")
	require.ErrorIs(t, err, client.manifestErr, "without a tree the push reports the manifest error, as before")
	require.False(t, local.clearedStaged)
	require.Empty(t, local.unpinned)
}

func TestPush_SparseCheckoutAlwaysFetchesManifest(t *testing.T) {
	recorder := recordSpans(t)
	root, local, client := treeSyncFixture(t, []string{"src"})

	require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "update"))

	require.Empty(t, local.appliedPushes, "a sparse checkout never applies locally")
	require.Equal(t, 1, client.manifestCalls)
	require.Equal(t, []string{"src"}, client.manifestPaths[0])
	require.Equal(t, &domain.SyncedTree{CommitID: treeSyncCommit.Base36(), Sparse: []string{"src"}}, local.syncedTree)
	requireLanded(t, local)
	require.Equal(t, map[string]string{
		"nipa.push.tree_sync":        "refetch",
		"nipa.push.tree_sync_reason": "sparse",
	}, treeSyncAttrs(t, recorder))
}

func TestPush_UnreadableConfigFetchesManifest(t *testing.T) {
	recorder := recordSpans(t)
	root, local, client := treeSyncFixture(t, nil)
	local.configLoadErr = errors.New("config unreadable")
	nipaUrl, err := domain.ParseNipaUrl(treeSyncURL)
	require.NoError(t, err)

	_, err = newTestPush(t, local, client).pushStaged(context.Background(), root, nipaUrl, "main", "update", "", "basecommit", "")
	require.NoError(t, err)

	require.Empty(t, local.appliedPushes, "without the config the sparse set is unknown")
	require.Equal(t, 1, client.manifestCalls)
	requireLanded(t, local)
	require.Equal(t, "config", treeSyncAttrs(t, recorder)["nipa.push.tree_sync_reason"])
}

// Merge and revert commits go through pushStaged with their own base: the
// commit the server checks is the one the local apply must be based on.
func TestPush_PendingOperationsPassTheirBaseCommit(t *testing.T) {
	t.Run("revert", func(t *testing.T) {
		root, local, client := treeSyncFixture(t, nil)
		local.revertState = &domain.RevertState{
			Targets:         []domain.CommitRef{{ID: "3", Hash: "abc", Subject: "third"}},
			CurrentTreeHash: "head-tree",
			CurrentCommitID: "revertbase",
		}
		require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "revert"))
		require.Len(t, local.appliedPushes, 1)
		require.Equal(t, "revertbase", local.appliedPushes[0].BaseCommitID)
		require.Zero(t, client.manifestCalls)
	})
	t.Run("merge", func(t *testing.T) {
		root, local, client := treeSyncFixture(t, nil)
		local.mergeState = &domain.MergeState{
			SourceCommitHash: "feedface",
			TargetTreeHash:   "target-tree",
			TargetCommitID:   "mergebase",
		}
		require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "merge"))
		require.Len(t, local.appliedPushes, 1)
		require.Equal(t, "mergebase", local.appliedPushes[0].BaseCommitID)
		require.Equal(t, "feedface", client.parent2hash)
		require.Zero(t, client.manifestCalls)
	})
}
