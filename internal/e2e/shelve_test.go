package e2e

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	clientgrpc "github.com/nipalab/nipa/internal/client/grpc"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	clientusecase "github.com/nipalab/nipa/internal/client/usecase"
	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/merge"
)

type shelfHarness struct {
	ctx    context.Context
	host   string
	url    string
	auth   *clientusecase.Auth
	client *clientgrpc.Client
}

func newShelfHarness(t *testing.T) *shelfHarness {
	t.Helper()
	ctx := context.Background()
	host := startTestServer(t, openTestDB(t))
	store := newMemoryStore()
	transport := clientgrpc.NewTransport()
	client := clientgrpc.NewClient(transport, clientusecase.NewSession(store, transport, failPrompt{}))
	require.NoError(t, client.Connect(ctx, host))
	login, err := client.LoginWithUsernamePassword(ctx, host, e2eSuperAdminEmail, e2eSuperAdminPass)
	require.NoError(t, err)
	require.NoError(t, store.SaveToken(login))
	return &shelfHarness{
		ctx:    ctx,
		host:   host,
		url:    "http://" + host + "/" + e2eOrgSlug + "/" + e2eProjectSlug,
		auth:   clientusecase.NewAuth(client, store, failPrompt{}),
		client: client,
	}
}

func (h *shelfHarness) clone(t *testing.T, name string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	repo := clientusecase.NewRepo(h.auth, h.client, localrepo.NewLocalRepo())
	require.NoError(t, repo.Clone(h.ctx, h.url, h.host, e2eOrgSlug, e2eProjectSlug, "", nil, target))
	return target
}

func (h *shelfHarness) push(t *testing.T, target, message string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		writeFile(t, target, p, content)
		stagePath(t, target, p)
	}
	require.NoError(t, clientusecase.NewPush(h.auth, h.client, localrepo.NewLocalRepo()).Run(h.ctx, target, message))
}

func (h *shelfHarness) update(t *testing.T, target string) {
	t.Helper()
	require.NoError(t, clientusecase.NewUpdate(h.auth, h.client, localrepo.NewLocalRepo()).Run(h.ctx, target))
}

// open builds the shelve usecase the way the CLI does: one localrepo
// handle, a working copy that may download snapshot content.
func (h *shelfHarness) open(t *testing.T, target string) (*clientusecase.Shelve, *clientusecase.WorkingCopy, *localrepo.LocalRepo) {
	t.Helper()
	lr := localrepo.NewLocalRepo()
	wc, err := clientusecase.NewWorkingCopy(lr, target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lr.Close() })
	wc.WithDownloader(h.auth, h.client)
	return clientusecase.NewShelve(lr, wc, target), wc, lr
}

func readWorking(t *testing.T, target, p string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(p)))
	require.NoError(t, err)
	return string(data)
}

func serverFiles(t *testing.T, h *shelfHarness, branch string) map[string]merge.File {
	t.Helper()
	tree, err := h.client.GetTreeNodeManifest(h.ctx, e2eOrgSlug, e2eProjectSlug, branch, nil)
	require.NoError(t, err)
	return merge.Flatten(tree)
}

var (
	pngV1 = string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02, 0x03})
	pngV2 = string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x09, 0x08, 0x07, 0x06})
)

// TestEndToEnd_ShelveSwitchApplyPush shelves text, binary and new files on
// main, switches to a branch that moved the same text file, applies the
// shelf there (three-way text merge) and pushes the result.
func TestEndToEnd_ShelveSwitchApplyPush(t *testing.T) {
	h := newShelfHarness(t)
	work := h.clone(t, "work")
	h.push(t, work, "seed", map[string]string{"notes.txt": "1\n2\n3\n4\n5\n", "art/hero.png": pngV1})
	_, err := h.client.CreateBranch(h.ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)

	other := h.clone(t, "other")
	updater := clientusecase.NewUpdate(h.auth, h.client, localrepo.NewLocalRepo())
	require.NoError(t, updater.Switch(h.ctx, other, "feature"))
	h.push(t, other, "feature edit", map[string]string{"notes.txt": "1\n2\n3\n4\nfive\n"})

	writeFile(t, work, "notes.txt", "one\n2\n3\n4\n5\n")
	writeFile(t, work, "art/hero.png", pngV2)
	writeFile(t, work, "docs/new.txt", "new doc\n")
	stagePath(t, work, "docs/new.txt")

	sh, wc, _ := h.open(t, work)
	shelf, err := sh.Create(h.ctx, clientusecase.ShelveOptions{Name: "wip"})
	require.NoError(t, err)
	require.Len(t, shelf.Entries, 3)
	require.Equal(t, "main", shelf.Branch)
	require.Equal(t, "1\n2\n3\n4\n5\n", readWorking(t, work, "notes.txt"))
	require.Equal(t, pngV1, readWorking(t, work, "art/hero.png"), "the raw binary is fetched back from the server")
	require.NoFileExists(t, filepath.Join(work, "docs/new.txt"))
	st, err := wc.Status(h.ctx)
	require.NoError(t, err)
	require.Empty(t, st.Staged)
	require.Empty(t, st.Modified)

	require.NoError(t, updater.Switch(h.ctx, work, "feature"))
	sh, wc, _ = h.open(t, work)
	res, err := sh.Apply(h.ctx, shelf.ID, false)
	require.NoError(t, err)
	require.Empty(t, res.Conflicts)
	require.True(t, res.Dropped)
	require.Equal(t, "one\n2\n3\n4\nfive\n", readWorking(t, work, "notes.txt"))
	require.Equal(t, pngV2, readWorking(t, work, "art/hero.png"))
	require.Equal(t, "new doc\n", readWorking(t, work, "docs/new.txt"))
	st, err = wc.Status(h.ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"docs/new.txt"}, st.Staged, "the staged flag comes back")
	require.ElementsMatch(t, []string{"art/hero.png", "notes.txt"}, st.Modified)
	list, err := sh.List()
	require.NoError(t, err)
	require.Empty(t, list)

	stagePath(t, work, "notes.txt")
	stagePath(t, work, "art/hero.png")
	_, err = h.client.LockFile(h.ctx, e2eOrgSlug, e2eProjectSlug, "art/hero.png", "feature", 0)
	require.NoError(t, err)
	require.NoError(t, clientusecase.NewPush(h.auth, h.client, localrepo.NewLocalRepo()).Run(h.ctx, work, "apply shelf"))
	files := serverFiles(t, h, "feature")
	require.Contains(t, files, "docs/new.txt")
	require.Equal(t, int64(len(pngV2)), files["art/hero.png"].SizeBytes)
}

// TestEndToEnd_ShelveConflictResolveAndAbort applies a shelf after a
// teammate changed the same lines: the apply waits, push refuses, abort
// restores the synced version, and a second apply is finished by resolving.
func TestEndToEnd_ShelveConflictResolveAndAbort(t *testing.T) {
	h := newShelfHarness(t)
	work := h.clone(t, "work")
	h.push(t, work, "seed", map[string]string{"a.txt": "base\n", "art/hero.png": pngV1})

	writeFile(t, work, "a.txt", "mine\n")
	writeFile(t, work, "art/hero.png", pngV2)
	sh, _, _ := h.open(t, work)
	shelf, err := sh.Create(h.ctx, clientusecase.ShelveOptions{})
	require.NoError(t, err)

	teammate := h.clone(t, "teammate")
	_, err = h.client.LockFile(h.ctx, e2eOrgSlug, e2eProjectSlug, "art/hero.png", "", 0)
	require.NoError(t, err)
	h.push(t, teammate, "upstream", map[string]string{"a.txt": "theirs\n", "art/hero.png": string([]byte{0x89, 'P', 'N', 'G', 0x00, 0x05})})
	h.update(t, work)

	sh, wc, lr := h.open(t, work)
	res, err := sh.Apply(h.ctx, shelf.ID, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"a.txt", "art/hero.png"}, res.Conflicts)
	require.Contains(t, readWorking(t, work, "a.txt"), "<<<<<<< ours")
	st, err := wc.Status(h.ctx)
	require.NoError(t, err)
	require.Equal(t, "shelf", st.Pending)

	err = clientusecase.NewPush(h.auth, h.client, localrepo.NewLocalRepo()).Run(h.ctx, work, "too early")
	require.ErrorContains(t, err, "stopped on conflicts")

	require.NoError(t, sh.Abort(h.ctx))
	require.Equal(t, "theirs\n", readWorking(t, work, "a.txt"))
	st, err = wc.Status(h.ctx)
	require.NoError(t, err)
	require.Empty(t, st.Pending)
	require.Empty(t, st.Modified)

	_, err = sh.Apply(h.ctx, shelf.ID, false)
	require.NoError(t, err)
	conflicts := clientusecase.NewConflicts(lr, wc, work)
	set, err := conflicts.List()
	require.NoError(t, err)
	require.Equal(t, "shelf", set.Op)
	require.Equal(t, shelf.ID, set.Theirs.ID)

	diffUc := clientusecase.NewDiff(h.auth, h.client, localrepo.NewLocalRepo())
	_, _, theirs := clientusecase.ConflictSides(set, "art/hero.png")
	var shelved bytes.Buffer
	require.NoError(t, diffUc.ReadBlob(h.ctx, work, *theirs, func(_ diff.Entry, r io.Reader) error {
		_, err := shelved.ReadFrom(r)
		return err
	}))
	require.Equal(t, pngV2, shelved.String())
	base, _, _ := clientusecase.ConflictSides(set, "a.txt")
	var baseText bytes.Buffer
	require.NoError(t, diffUc.ReadBlob(h.ctx, work, *base, func(_ diff.Entry, r io.Reader) error {
		_, err := baseText.ReadFrom(r)
		return err
	}))
	require.Equal(t, "base\n", baseText.String())

	require.NoError(t, conflicts.Resolve(h.ctx, "art/hero.png", clientusecase.Resolution{Content: bytes.NewReader(shelved.Bytes()), AllowMarkers: true}))
	require.NoError(t, conflicts.Resolve(h.ctx, "a.txt", clientusecase.Resolution{Content: bytes.NewReader([]byte("theirs and mine\n"))}))
	st, err = wc.Status(h.ctx)
	require.NoError(t, err)
	require.Empty(t, st.Pending, "the last resolution finishes the apply")
	require.Empty(t, st.Staged, "shelf resolutions are not staged")
	list, err := sh.List()
	require.NoError(t, err)
	require.Empty(t, list)
	require.Equal(t, pngV2, readWorking(t, work, "art/hero.png"))
}

// TestEndToEnd_SyncPlanShelveThroughDaemon drives the desktop flow: the
// overwrite check before an update, "shelve and continue", the update, and
// a conflicting apply resolved from the shelf.
func TestEndToEnd_SyncPlanShelveThroughDaemon(t *testing.T) {
	h := newShelfHarness(t)
	work := h.clone(t, "work")
	h.push(t, work, "seed", map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	_, err := h.client.CreateBranch(h.ctx, e2eOrgSlug, e2eProjectSlug, "feature", "main", "", "")
	require.NoError(t, err)

	client, dctx := startDaemon(t, h.auth, h.client)
	_, err = client.WatchRepo(dctx, &daemonpb.WatchRepoRequest{Root: work})
	require.NoError(t, err)

	writeFile(t, work, "a.txt", "local a\n")
	writeFile(t, work, "b.txt", "local b\n")
	h.push(t, h.clone(t, "teammate"), "upstream", map[string]string{"a.txt": "upstream a\n"})

	plan, err := client.SyncPlan(dctx, &daemonpb.SyncPlanRequest{Root: work})
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, plan.GetOverwritten(), "b.txt is unchanged upstream")
	plan, err = client.SyncPlan(dctx, &daemonpb.SyncPlanRequest{Root: work, Branch: "feature"})
	require.NoError(t, err)
	require.Empty(t, plan.GetOverwritten(), "feature still has the synced versions")

	stream, err := client.ShelveCreate(dctx, &daemonpb.ShelveCreateRequest{Root: work, Name: "Before updating", Paths: []string{"a.txt"}})
	require.NoError(t, err)
	created := terminalResult(t, collectOpEvents(t, stream)).GetShelve().GetShelf()
	require.Equal(t, "Before updating", created.GetName())
	require.Equal(t, "a\n", readWorking(t, work, "a.txt"))
	require.Equal(t, "local b\n", readWorking(t, work, "b.txt"))

	plan, err = client.SyncPlan(dctx, &daemonpb.SyncPlanRequest{Root: work})
	require.NoError(t, err)
	require.Empty(t, plan.GetOverwritten())
	update, err := client.Update(dctx, &daemonpb.UpdateRequest{Root: work})
	require.NoError(t, err)
	terminalResult(t, collectOpEvents(t, update))
	require.Equal(t, "upstream a\n", readWorking(t, work, "a.txt"))

	files, err := client.ShelveFiles(dctx, &daemonpb.ShelveFilesRequest{Root: work, Id: created.GetId()})
	require.NoError(t, err)
	require.Len(t, files.GetFiles(), 1)
	require.EqualValues(t, 1, files.GetFiles()[0].GetAdditions())

	apply, err := client.ShelveApply(dctx, &daemonpb.ShelveApplyRequest{Root: work, Id: created.GetId()})
	require.NoError(t, err)
	applied := terminalResult(t, collectOpEvents(t, apply)).GetShelveApply()
	require.Equal(t, []string{"a.txt"}, applied.GetConflicts())
	list, err := client.ShelveList(dctx, &daemonpb.ShelveListRequest{Root: work})
	require.NoError(t, err)
	require.Equal(t, created.GetId(), list.GetApplying())
	set, err := client.Conflicts(dctx, &daemonpb.ConflictsRequest{Root: work})
	require.NoError(t, err)
	require.Equal(t, "shelf", set.GetOp())

	st, err := client.ResolveConflict(dctx, &daemonpb.ResolveConflictRequest{Root: work, Path: "a.txt", Choice: "theirs"})
	require.NoError(t, err)
	require.Empty(t, st.GetPending())
	require.ElementsMatch(t, []string{"a.txt", "b.txt"}, st.GetModified())
	require.Equal(t, "local a\n", readWorking(t, work, "a.txt"))
	list, err = client.ShelveList(dctx, &daemonpb.ShelveListRequest{Root: work})
	require.NoError(t, err)
	require.Empty(t, list.GetShelves())

	stream, err = client.ShelveCreate(dctx, &daemonpb.ShelveCreateRequest{Root: work})
	require.NoError(t, err)
	everything := terminalResult(t, collectOpEvents(t, stream)).GetShelve().GetShelf()
	require.Len(t, everything.GetEntries(), 2)
	st, err = client.Status(dctx, &daemonpb.StatusRequest{Root: work})
	require.NoError(t, err)
	require.Empty(t, st.GetModified())
	require.Empty(t, st.GetStaged())

	stream, err = client.ShelveApply(dctx, &daemonpb.ShelveApplyRequest{Root: work, Id: everything.GetId(), Keep: true})
	require.NoError(t, err)
	kept := terminalResult(t, collectOpEvents(t, stream)).GetShelveApply()
	require.Empty(t, kept.GetConflicts())
	require.False(t, kept.GetDropped())
	_, err = client.ShelveDrop(dctx, &daemonpb.ShelveDropRequest{Root: work, Id: everything.GetId()})
	require.NoError(t, err)
	require.Equal(t, "local b\n", readWorking(t, work, "b.txt"))
}
