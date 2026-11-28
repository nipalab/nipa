package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/treedelta"
)

// viewPushClient is a push client whose manifests carry a view token, like
// the real transport.
type viewPushClient struct {
	*stubPushClient
	view    []byte
	targets []treedelta.Target
}

func (c *viewPushClient) GetTreeManifestDelta(ctx context.Context, org, project string, target treedelta.Target, paths []string, base treedelta.Base) (*treedelta.Manifest, error) {
	c.targets = append(c.targets, target)
	tree, err := c.GetTreeNodeManifest(ctx, org, project, "", paths)
	if err != nil {
		return nil, err
	}
	return &treedelta.Manifest{Full: tree, View: c.view}, nil
}

// viewLocalRepo is a local repository that records the view token a manifest
// was saved with.
type viewLocalRepo struct {
	*stubLocalRepo
}

func (r *viewLocalRepo) SaveSyncedTreeView(root *serverDomain.TreeNode, commitID string, sparse []string, view []byte) error {
	if err := r.SaveSyncedTree(root, commitID, sparse); err != nil {
		return err
	}
	r.syncedTree.View = view
	return nil
}

// The push response's view reaches the local apply as it came, an absent one
// included: localrepo.ApplyPush decides what to record.
func TestPush_LocalApplyCarriesThePushView(t *testing.T) {
	for name, view := range map[string]serverDomain.PushView{
		"absent":       {},
		"unrestricted": {Token: []byte("token"), Unrestricted: true},
		"hides pushed": {Token: []byte("token"), HidesPushed: true},
	} {
		t.Run(name, func(t *testing.T) {
			root, local, client := treeSyncFixture(t, nil)
			client.pushResult.View = view
			require.NoError(t, newTestPush(t, local, client).Run(context.Background(), root, "update"))
			require.Len(t, local.appliedPushes, 1)
			require.Equal(t, view, local.appliedPushes[0].View)
			require.Zero(t, client.manifestCalls)
		})
	}
}

// A push that falls back to the manifest records the manifest's token, which
// describes the tree it saves, never the push response's.
func TestPush_RefetchRecordsTheManifestView(t *testing.T) {
	for name, sparse := range map[string][]string{"full": nil, "sparse": {"src"}} {
		t.Run(name, func(t *testing.T) {
			root, local, stub := treeSyncFixture(t, sparse)
			local.applyPushErr = domain.ErrPushApplyBase
			stub.pushResult.View = serverDomain.PushView{Token: []byte("push-token"), Unrestricted: true}
			client := &viewPushClient{stubPushClient: stub, view: []byte("manifest-token")}

			require.NoError(t, newTestPush(t, &viewLocalRepo{local}, client).Run(context.Background(), root, "update"))
			require.Equal(t, []treedelta.Target{{CommitID: treeSyncCommit.Base36()}}, client.targets, "asked by the pushed commit")
			require.Equal(t, &domain.SyncedTree{
				CommitID: treeSyncCommit.Base36(),
				Sparse:   append([]string{}, sparse...),
				View:     []byte("manifest-token"),
			}, local.syncedTree)
		})
	}
}

// viewMergeClient is a merge client whose manifests carry a view token.
type viewMergeClient struct {
	*stubMergeClient
	targets []treedelta.Target
}

func (c *viewMergeClient) GetTreeManifestDelta(ctx context.Context, org, project string, target treedelta.Target, paths []string, _ treedelta.Base) (*treedelta.Manifest, error) {
	c.targets = append(c.targets, target)
	tree, err := c.GetTreeNodeManifest(ctx, org, project, "main", paths)
	if err != nil {
		return nil, err
	}
	return &treedelta.Manifest{Full: tree, View: []byte("ff-token")}, nil
}

// plainMergeRepo hides SaveSyncedTree: a repository without the record.
type plainMergeRepo struct {
	mergeLocalRepo
}

func ffFixture(t *testing.T) (*stubMergeClient, *stubLocalRepo, snow.ID) {
	t.Helper()
	id := snow.ID(42)
	hash := serverDomain.Hash{0xaa}
	local := &stubLocalRepo{loadConfig: &domain.Config{Url: "http://example.com/org/project", Branch: "main"}}
	client := &stubMergeClient{
		baseInfo: &domain.MergeBaseInfo{TargetCommitID: "T1", SourceCommitID: "F1", SourceCommitHash: hash.String(), MergeBaseCommitID: "T1"},
		treeByBranch: map[string]*serverDomain.TreeNode{
			"main": treeWithFiles(map[string]serverDomain.File{"a.txt": fileOf("a.txt", hash, nil)}),
		},
		ffBranch: &serverDomain.Branch{Name: "main", CommitID: &id},
	}
	return client, local, id
}

// A fast-forward records the new head's manifest with its view token, asked
// by commit, so the next update can be a delta.
func TestMerge_FastForwardRecordsTheView(t *testing.T) {
	stub, local, id := ffFixture(t)
	client := &viewMergeClient{stubMergeClient: stub}
	merger := newTestMerge(t, stub, local, nil)
	merger.client = client
	merger.localRepo = &viewLocalRepo{local}

	outcome, err := merger.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.FastForwarded)
	require.Equal(t, []treedelta.Target{{CommitID: id.Base36()}}, client.targets)
	require.Equal(t, &domain.SyncedTree{CommitID: id.Base36(), Sparse: []string{}, View: []byte("ff-token")}, local.syncedTree)
}

// A fast-forward to a branch without commits asks by name and records
// nothing; a repository without the record gets the plain tree.
func TestMerge_FastForwardWithoutRecord(t *testing.T) {
	stub, local, _ := ffFixture(t)
	stub.ffBranch = &serverDomain.Branch{Name: "main"}
	client := &viewMergeClient{stubMergeClient: stub}
	merger := newTestMerge(t, stub, local, nil)
	merger.client = client
	_, err := merger.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.Equal(t, []treedelta.Target{{Branch: "main"}}, client.targets)
	require.Nil(t, local.syncedTree)

	stub, local, _ = ffFixture(t)
	merger = newTestMerge(t, stub, local, nil)
	merger.localRepo = plainMergeRepo{local}
	_, err = merger.Run(context.Background(), t.TempDir(), "feature", MergeOptions{})
	require.NoError(t, err)
	require.NotNil(t, local.tree)
	require.Nil(t, local.syncedTree, "SaveTree keeps no record")
}
