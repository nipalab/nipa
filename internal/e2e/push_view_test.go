package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// pushViewRule is one rule of the restricted pusher in the push view tests,
// or with def set a path default of the project.
type pushViewRule struct {
	prefix     string
	permission serverDomain.Permission
	def        bool
}

// setPusherRules replaces the restricted user's rules and the project's path
// defaults with rules and drops the server's cached rule sets.
func (h *deltaHarness) setPusherRules(rules ...pushViewRule) {
	h.t.Helper()
	_, err := h.db.ExecContext(h.ctx, `DELETE FROM pbac_rules WHERE user_id = ?`, deltaUserID)
	require.NoError(h.t, err)
	_, err = h.db.ExecContext(h.ctx, `DELETE FROM project_paths_permissions WHERE project_id = 1`)
	require.NoError(h.t, err)
	repo := sqlite.NewPBACRepository(h.db)
	projectID, userID := snow.ID(1), snow.ID(deltaUserID)
	for _, rule := range rules {
		if rule.def {
			_, err := repo.UpsertPathPermission(h.ctx, serverDomain.ProjectPathPermission{ProjectID: projectID, PathPrefix: rule.prefix, Permission: rule.permission})
			require.NoError(h.t, err)
			continue
		}
		_, err := repo.CreateRule(h.ctx, serverDomain.PBACRule{
			UserID: &userID, OrgID: 1, ProjectID: &projectID, PathPrefix: rule.prefix, Permission: rule.permission,
		})
		require.NoError(h.t, err)
	}
	h.uc.Permission.InvalidateAll()
}

// pushViewClone is a working copy of one caller with the counting client and
// the recording repository, so a test sees whether its moves used a delta.
type pushViewClone struct {
	dir      string
	auth     *clientusecase.Auth
	grpc     *clientgrpc.Client
	counting *deltaCountingClient
	repo     *deltaRecordingRepo
	pusher   *clientusecase.Push
	updater  *clientusecase.Update
}

func (h *deltaHarness) emptyClone(t *testing.T, grpcClient *clientgrpc.Client, store *memoryStore, sparse []string) *pushViewClone {
	t.Helper()
	auth := clientusecase.NewAuth(grpcClient, store, failPrompt{})
	c := &pushViewClone{
		dir:      filepath.Join(t.TempDir(), "clone"),
		auth:     auth,
		grpc:     grpcClient,
		counting: &deltaCountingClient{Client: grpcClient},
		repo:     &deltaRecordingRepo{LocalRepo: localrepo.NewLocalRepo()},
	}
	t.Cleanup(func() { _ = c.repo.Close() })
	url := "http://" + h.host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	require.NoError(t, clientusecase.NewRepo(auth, c.counting, c.repo).Clone(h.ctx, url, h.host, e2eOrgSlug, e2eProjectSlug, "", sparse, c.dir))
	c.counting.requests = 0
	c.pusher = clientusecase.NewPush(auth, c.counting, localrepo.NewLocalRepo())
	c.updater = clientusecase.NewUpdate(auth, c.counting, c.repo)
	return c
}

func (c *pushViewClone) push(t *testing.T, ctx context.Context, message string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		writeAndStage(t, c.dir, path, content)
	}
	require.NoError(t, c.pusher.Run(ctx, c.dir, message))
}

// moved runs move and reports how many of its manifest requests named a
// base and how many deltas were applied.
func (c *pushViewClone) moved(t *testing.T, move func() error) (based, applied int) {
	t.Helper()
	c.counting.requests, c.counting.based, c.repo.applied = 0, 0, 0
	require.NoError(t, move())
	require.Empty(t, c.repo.errs, "every delta must apply")
	return c.counting.based, c.repo.applied
}

// assertFresh checks the clone holds exactly what a fresh clone of branch
// holds for the same caller: rows, tree hash, synced-tree record (view token
// included) and working files.
func (c *pushViewClone) assertFresh(t *testing.T, h *deltaHarness, branch string, sparse []string) {
	t.Helper()
	url := "http://" + h.host + "/" + e2eOrgSlug + "/" + e2eProjectSlug
	fresh := filepath.Join(t.TempDir(), "fresh")
	require.NoError(t, clientusecase.NewRepo(c.auth, c.grpc, localrepo.NewLocalRepo()).
		Clone(h.ctx, url, h.host, e2eOrgSlug, e2eProjectSlug, branch, sparse, fresh))
	require.NoError(t, c.repo.Close())
	require.Equal(t, storedTree(t, fresh), storedTree(t, c.dir), "stored tree")
	require.Equal(t, workingTree(t, fresh), workingTree(t, c.dir), "working copy")
}

// assertRecordedView checks the synced-tree record names the checked-out
// commit and holds the view token the server hands this caller with that
// commit's manifest, or no token at all.
func (c *pushViewClone) assertRecordedView(t *testing.T, h *deltaHarness, want bool) {
	t.Helper()
	commitID, _ := pinnedCommit(t, c.dir)
	record, err := c.repo.LoadSyncedTree()
	require.NoError(t, err)
	require.NotNil(t, record)
	require.Equal(t, commitID, record.CommitID)
	if !want {
		require.Empty(t, record.View, "no view token may be recorded")
		return
	}
	cfg, err := c.repo.LoadConfig()
	require.NoError(t, err)
	manifest, err := h.full(c.grpc, commitID, cfg.Sparse)
	require.NoError(t, err)
	require.Len(t, manifest.View, 32)
	require.Equal(t, manifest.View, record.View, "the manifest's view token is recorded")
}

// advance lets the admin push files onto branch from a clone of its own.
func (h *deltaHarness) advance(t *testing.T, branch string, files map[string]string) string {
	t.Helper()
	auth := clientusecase.NewAuth(h.admin, h.adminStore, failPrompt{})
	dir := cloneWorktree(t, h.admin, auth, h.host, branch)
	for path, content := range files {
		writeAndStage(t, dir, path, content)
	}
	require.NoError(t, clientusecase.NewPush(auth, h.admin, localrepo.NewLocalRepo()).Run(h.ctx, dir, "advance "+branch))
	commitID, _ := pinnedCommit(t, dir)
	return commitID
}

// A clone of an empty project never received a manifest, so before the push
// result carried the view token, its pushes left the stored tree without one
// and its first update or switch fetched the full manifest. Every caller
// whose view the push can vouch for must now move by delta right away.
func TestEndToEnd_EmptyClonePushKeepsDeltaBase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		admin bool
		rules []pushViewRule
	}{
		{name: "admin", admin: true},
		{name: "root rule", rules: []pushViewRule{{prefix: "", permission: serverDomain.PermissionRead | serverDomain.PermissionWrite}}},
		// Path-restricted: reads and writes a only. The admin's files in b
		// are hidden, which the delta handles like any filtered view.
		{name: "path restricted", rules: []pushViewRule{{prefix: "a", permission: serverDomain.PermissionRead | serverDomain.PermissionWrite}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newDeltaHarness(t)
			grpcClient, store := h.admin, h.adminStore
			if !tc.admin {
				h.setPusherRules(tc.rules...)
				grpcClient, store = h.user, h.userStore
			}
			c := h.emptyClone(t, grpcClient, store, nil)
			c.push(t, h.ctx, "first", map[string]string{"a/one.txt": "one\n", "a/deep/two.txt": "two\n"})
			first, _ := pinnedCommit(t, c.dir)
			c.push(t, h.ctx, "second", map[string]string{"a/three.txt": "three\n"})
			require.Zero(t, c.counting.requests, "both pushes apply locally")
			c.assertRecordedView(t, h, true)

			_ = h.advance(t, "main", map[string]string{"a/four.txt": "four\n", "b/hidden.txt": "b\n"})
			based, applied := c.moved(t, func() error { return c.updater.Run(h.ctx, c.dir) })
			require.Equal(t, 1, based, "the update names the pushed commit as its base")
			require.Equal(t, 1, applied, "the update applies a delta")

			_, err := h.admin.CreateBranch(h.ctx, e2eOrgSlug, e2eProjectSlug, "side", "", first, "")
			require.NoError(t, err)
			based, applied = c.moved(t, func() error { return c.updater.Switch(h.ctx, c.dir, "side") })
			require.Equal(t, 1, based)
			require.Equal(t, 1, applied, "the switch applies a delta")
			c.assertFresh(t, h, "side", nil)
		})
	}
}

// A pusher who writes a path they cannot read holds a stored tree with a file
// their manifest hides, so the push must not leave a view token behind: the
// next move fetches the full manifest, which drops the file, and from there
// pushes record the token again. The hidden file sits in a directory no later
// commit changes, so a delta against the pushed tree would keep it for good.
func TestEndToEnd_PushOfUnreadablePathDropsDeltaBase(t *testing.T) {
	t.Parallel()
	h := newDeltaHarness(t)
	rw := serverDomain.PermissionRead | serverDomain.PermissionWrite
	h.setPusherRules(
		pushViewRule{prefix: "", permission: rw, def: true},
		pushViewRule{prefix: "drop/inner", permission: serverDomain.PermissionWrite, def: true},
	)
	c := h.emptyClone(t, h.user, h.userStore, nil)
	c.push(t, h.ctx, "readable", map[string]string{"a/one.txt": "one\n"})
	c.assertRecordedView(t, h, true)

	c.push(t, h.ctx, "write only", map[string]string{"drop/inner/secret.txt": "secret\n"})
	require.Zero(t, c.counting.requests, "the push still applies locally")
	c.assertRecordedView(t, h, false)

	_ = h.advance(t, "main", map[string]string{"a/two.txt": "two\n"})
	based, applied := c.moved(t, func() error { return c.updater.Run(h.ctx, c.dir) })
	require.Zero(t, based, "no base: the stored tree is not the caller's view")
	require.Zero(t, applied)
	c.assertRecordedView(t, h, true)
	_, err := os.Stat(filepath.Join(c.dir, "drop", "inner", "secret.txt"))
	require.True(t, os.IsNotExist(err), "the full manifest drops the hidden file")

	c.push(t, h.ctx, "readable again", map[string]string{"a/three.txt": "three\n"})
	_ = h.advance(t, "main", map[string]string{"a/four.txt": "four\n"})
	based, applied = c.moved(t, func() error { return c.updater.Run(h.ctx, c.dir) })
	require.Equal(t, 1, based)
	require.Equal(t, 1, applied)
	c.assertFresh(t, h, "main", nil)
}

// A sparse checkout never applies a push locally: its pushes fetch the
// manifest with the sparse set, which carries the token, so an empty clone
// made sparse (an empty branch cannot be cloned sparse: the paths do not
// exist) moves by delta after its pushes as well.
func TestEndToEnd_SparseEmptyClonePushKeepsDeltaBase(t *testing.T) {
	t.Parallel()
	h := newDeltaHarness(t)
	sparse := []string{"a"}
	c := h.emptyClone(t, h.admin, h.adminStore, nil)
	require.NoError(t, c.updater.SetSparse(c.dir, sparse))
	c.push(t, h.ctx, "first", map[string]string{"a/one.txt": "one\n"})
	c.push(t, h.ctx, "second", map[string]string{"a/two.txt": "two\n"})
	require.Equal(t, 2, c.counting.requests, "each sparse push fetches the manifest")
	c.assertRecordedView(t, h, true)

	_ = h.advance(t, "main", map[string]string{"a/three.txt": "three\n", "b/outside.txt": "b\n"})
	based, applied := c.moved(t, func() error { return c.updater.Run(h.ctx, c.dir) })
	require.Equal(t, 1, based)
	require.Equal(t, 1, applied)
	c.assertFresh(t, h, "main", sparse)
}

// A fast-forward merge saves the new head's manifest with its view token, so
// the update after it is a delta too.
func TestEndToEnd_FastForwardKeepsDeltaBase(t *testing.T) {
	t.Parallel()
	h := newDeltaHarness(t)
	c := h.emptyClone(t, h.admin, h.adminStore, nil)
	c.push(t, h.ctx, "seed", map[string]string{"a/one.txt": "one\n"})
	_, err := h.admin.CreateBranch(h.ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)
	_ = h.advance(t, "feature", map[string]string{"a/two.txt": "two\n"})

	merger := clientusecase.NewMerge(c.auth, c.counting, c.repo, c.pusher)
	outcome, err := merger.Run(h.ctx, c.dir, "feature", clientusecase.MergeOptions{})
	require.NoError(t, err)
	require.True(t, outcome.FastForwarded)
	c.assertRecordedView(t, h, true)

	_ = h.advance(t, "main", map[string]string{"b/three.txt": "three\n"})
	based, applied := c.moved(t, func() error { return c.updater.Run(h.ctx, c.dir) })
	require.Equal(t, 1, based)
	require.Equal(t, 1, applied)
	c.assertFresh(t, h, "main", nil)
}
