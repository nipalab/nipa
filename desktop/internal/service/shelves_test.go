package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type shelfDaemon struct {
	*conflictDaemon
	list     *daemonpb.ShelveListResponse
	files    *daemonpb.ShelveFilesResponse
	plan     *daemonpb.SyncPlanResponse
	planReq  *daemonpb.SyncPlanRequest
	created  *daemonpb.ShelveCreateRequest
	applied  *daemonpb.ShelveApplyRequest
	dropped  string
	shelfErr error
}

func (d *shelfDaemon) ShelveList(context.Context, *daemonpb.ShelveListRequest, ...grpc.CallOption) (*daemonpb.ShelveListResponse, error) {
	return d.list, d.shelfErr
}

func (d *shelfDaemon) ShelveFiles(context.Context, *daemonpb.ShelveFilesRequest, ...grpc.CallOption) (*daemonpb.ShelveFilesResponse, error) {
	return d.files, d.shelfErr
}

func (d *shelfDaemon) ShelveDrop(_ context.Context, req *daemonpb.ShelveDropRequest, _ ...grpc.CallOption) (*daemonpb.ShelveDropResponse, error) {
	d.dropped = req.GetId()
	return &daemonpb.ShelveDropResponse{}, d.shelfErr
}

func (d *shelfDaemon) SyncPlan(_ context.Context, req *daemonpb.SyncPlanRequest, _ ...grpc.CallOption) (*daemonpb.SyncPlanResponse, error) {
	d.planReq = req
	return d.plan, d.shelfErr
}

func (d *shelfDaemon) ShelveCreate(_ context.Context, req *daemonpb.ShelveCreateRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.created = req
	return d.opStream()
}

func (d *shelfDaemon) ShelveApply(_ context.Context, req *daemonpb.ShelveApplyRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.applied = req
	return d.opStream()
}

func newShelfService(t *testing.T) (*Service, *shelfDaemon) {
	t.Helper()
	_, c := newConflictService(t)
	d := &shelfDaemon{conflictDaemon: c}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func pbShelf() *daemonpb.Shelf {
	return &daemonpb.Shelf{
		Id: "s1", Name: "wip", CreatedAt: 1767225600, Branch: "main", Tag: "v1", BaseCommitId: "c1", SizeBytes: 9,
		Entries: []*daemonpb.ShelfEntry{{Path: "hero.png", Status: "modified", Staged: true, Locked: true, SizeBytes: 4, IsBinary: true, Kind: "image"}, {Path: "new.txt", Status: "added", Untracked: true, Kind: "text"}},
	}
}

func TestService_ShelvesListAndDrop(t *testing.T) {
	svc, d := newShelfService(t)
	d.list = &daemonpb.ShelveListResponse{Shelves: []*daemonpb.Shelf{pbShelf()}, Applying: "s1"}
	list, err := svc.Shelves(ctx, "/work/game")
	require.NoError(t, err)
	require.Equal(t, "s1", list.Applying)
	require.Len(t, list.Shelves, 1)
	sh := list.Shelves[0]
	require.Equal(t, "2026-01-01T00:00:00Z", sh.CreatedAt)
	require.Equal(t, "v1", sh.Tag)
	require.Equal(t, ShelfEntry{Path: "hero.png", Status: "modified", Staged: true, Locked: true, Size: 4, Binary: true, Kind: "image"}, sh.Entries[0])
	require.True(t, sh.Entries[1].Untracked)

	require.NoError(t, svc.DropShelf(ctx, "/work/game", "s1"))
	require.Equal(t, "s1", d.dropped)

	d.shelfErr = errors.New("boom")
	_, err = svc.Shelves(ctx, "/work/game")
	require.ErrorContains(t, err, "boom")
	_, err = svc.ShelfFiles(ctx, "/work/game", "s1")
	require.ErrorContains(t, err, "boom")
	_, err = svc.SyncPlan(ctx, "/work/game", "", "")
	require.ErrorContains(t, err, "boom")
}

func TestService_ShelveAndApply(t *testing.T) {
	svc, d := newShelfService(t)
	d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Shelve{Shelve: &daemonpb.ShelveResult{Shelf: pbShelf()}}}}}}
	out, err := svc.Shelve(ctx, "/work/game", ShelveInput{Name: " wip ", Paths: []string{"hero.png"}, IncludeUntracked: true})
	require.NoError(t, err)
	require.Equal(t, "wip", out.Shelf.Name)
	require.Equal(t, "wip", d.created.GetName())
	require.Equal(t, []string{"hero.png"}, d.created.GetPaths())
	require.True(t, d.created.GetIncludeUntracked())

	_, err = svc.ApplyShelf(ctx, "/work/game", "", false)
	require.ErrorContains(t, err, "choose a shelf")
	d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_ShelveApply{ShelveApply: &daemonpb.ShelveApplyResult{ShelfId: "s1", Name: "wip", Conflicts: []string{"a.txt"}}}}}}}
	out, err = svc.ApplyShelf(ctx, "/work/game", "s1", true)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, out.ShelfApply.Conflicts)
	require.True(t, d.applied.GetKeep())

	d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_ShelveApply{ShelveApply: &daemonpb.ShelveApplyResult{Aborted: true}}}}}}
	out, err = svc.AbortShelfApply(ctx, "/work/game")
	require.NoError(t, err)
	require.True(t, out.ShelfApply.Aborted)
	require.Empty(t, out.ShelfApply.Conflicts)
	require.True(t, d.applied.GetAbort())
}

func TestService_ShelfFilesAndSyncPlan(t *testing.T) {
	svc, d := newShelfService(t)
	d.files = &daemonpb.ShelveFilesResponse{Shelf: pbShelf(), Files: []*daemonpb.DiffFile{{Path: "hero.png", Status: "modified", Kind: "image", Old: &daemonpb.DiffFileSide{Path: "hero.png"}, New: &daemonpb.DiffFileSide{Path: "hero.png"}}}}
	files, err := svc.ShelfFiles(ctx, "/work/game", "s1")
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "shelf_base", files[0].OldSource)
	require.Equal(t, "shelf", files[0].NewSource)
	require.Equal(t, "s1", files[0].NewRevision)

	d.plan = &daemonpb.SyncPlanResponse{Overwritten: []string{"a.txt"}}
	plan, err := svc.SyncPlan(ctx, "/work/game", "feature", "")
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, plan.Overwritten)
	require.Equal(t, "feature", d.planReq.GetBranch())
	d.plan = &daemonpb.SyncPlanResponse{}
	plan, err = svc.SyncPlan(ctx, "/work/game", "", "v1")
	require.NoError(t, err)
	require.NotNil(t, plan.Overwritten)
}

func shelfSet() *daemonpb.ConflictsResponse {
	return &daemonpb.ConflictsResponse{
		Op:     "shelf",
		Base:   &daemonpb.ConflictRevision{Id: "c1", Label: "shelved from"},
		Ours:   &daemonpb.ConflictRevision{Id: "c2", Label: "main"},
		Theirs: &daemonpb.ConflictRevision{Id: "s1", Label: "shelf wip"},
		Files: []*daemonpb.ConflictFile{
			{Path: "a.txt", Kind: "text", Viewer: "text"},
			{Path: "hero.png", Kind: "binary", Binary: true, Viewer: "image"},
			{Path: "gone.png", Kind: "delete_modify", Binary: true, Viewer: "image"},
			{Path: "kept.png", Kind: "modify_delete", Binary: true, Viewer: "image"},
		},
	}
}

func TestService_ShelfConflictReads(t *testing.T) {
	svc, d := newShelfService(t)
	d.conflicts = shelfSet()
	d.blobs["shelf_base:s1"] = blobText("base\n")
	d.blobs["snapshot:"] = blobText("ours\n")
	d.blobs["shelf:s1"] = blobText("theirs\n")
	d.blobs["working:"] = blobText("work\n")
	texts, err := svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.NoError(t, err)
	require.Equal(t, "base\n", texts.Base.Text)
	require.Equal(t, "ours\n", texts.Ours.Text)
	require.Equal(t, "theirs\n", texts.Theirs.Text)

	diff, err := svc.ConflictDiff(ctx, "/work/game", "hero.png")
	require.NoError(t, err)
	require.Equal(t, "snapshot", diff.OldSource)
	require.Equal(t, "shelf", diff.NewSource)
	require.Equal(t, "s1", diff.NewRevision)
	require.Equal(t, "image", diff.Kind)
	diff, err = svc.ConflictDiff(ctx, "/work/game", "gone.png")
	require.NoError(t, err)
	require.Nil(t, diff.Old)
	diff, err = svc.ConflictDiff(ctx, "/work/game", "kept.png")
	require.NoError(t, err)
	require.Nil(t, diff.New)
	diff, err = svc.ConflictDiff(ctx, "/work/game", "missing.png")
	require.NoError(t, err)
	require.Nil(t, diff)
}
