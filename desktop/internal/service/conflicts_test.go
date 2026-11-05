package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type conflictDaemon struct {
	*historyDaemon
	conflicts   *daemonpb.ConflictsResponse
	conflictErr error
	merged      *daemonpb.MergeOpRequest
	resolved    *daemonpb.ResolveConflictRequest
	blobs       map[string][]*daemonpb.BlobEvent
}

func (d *conflictDaemon) Merge(_ context.Context, req *daemonpb.MergeOpRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.merged = req
	return d.opStream()
}

func (d *conflictDaemon) Conflicts(context.Context, *daemonpb.ConflictsRequest, ...grpc.CallOption) (*daemonpb.ConflictsResponse, error) {
	return d.conflicts, d.conflictErr
}

func (d *conflictDaemon) ResolveConflict(_ context.Context, req *daemonpb.ResolveConflictRequest, _ ...grpc.CallOption) (*daemonpb.StatusResponse, error) {
	d.resolved = req
	return d.status, d.callErr
}

func (d *conflictDaemon) ReadBlob(_ context.Context, req *daemonpb.ReadBlobRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.BlobEvent], error) {
	key := req.GetBlob().GetSource() + ":" + req.GetBlob().GetRevision()
	events, ok := d.blobs[key]
	if !ok {
		events = []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Failure{Failure: &daemonpb.OpFailure{Code: 404, Message: "missing"}}}}
	}
	return &listStream[daemonpb.BlobEvent]{items: events}, nil
}

func newConflictService(t *testing.T) (*Service, *conflictDaemon) {
	t.Helper()
	_, h := newHistory(t)
	d := &conflictDaemon{historyDaemon: h, blobs: map[string][]*daemonpb.BlobEvent{}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func mergeSet() *daemonpb.ConflictsResponse {
	return &daemonpb.ConflictsResponse{
		Op:     "merge",
		Base:   &daemonpb.ConflictRevision{Id: "b1", Label: "merge base"},
		Ours:   &daemonpb.ConflictRevision{Id: "o1", Label: "main"},
		Theirs: &daemonpb.ConflictRevision{Id: "t1", Label: "feature"},
		Files:  []*daemonpb.ConflictFile{{Path: "a.txt", Kind: "text", Viewer: "text"}, {Path: "hero.png", Kind: "binary", Binary: true, Resolved: true, Viewer: "image"}},
	}
}

func blobText(text string) []*daemonpb.BlobEvent {
	return []*daemonpb.BlobEvent{
		{Event: &daemonpb.BlobEvent_Info{Info: &daemonpb.BlobInfo{SizeBytes: int64(len(text))}}},
		{Event: &daemonpb.BlobEvent_Data{Data: []byte(text)}},
	}
}

func TestService_MergeAndAbort(t *testing.T) {
	svc, d := newConflictService(t)
	_, err := svc.Merge(ctx, "/work/game", " ")
	require.ErrorContains(t, err, "choose a branch")

	d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Merge{Merge: &daemonpb.MergeResult{Conflicts: []string{"a.txt"}}}}}}}
	out, err := svc.Merge(ctx, "/work/game", "feature")
	require.NoError(t, err)
	assert.Equal(t, &MergeOutcome{Conflicts: []string{"a.txt"}}, out.Merge)
	assert.Equal(t, "feature", d.merged.GetSourceBranch())

	d.op = []*daemonpb.OpEvent{{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Merge{Merge: &daemonpb.MergeResult{Aborted: true}}}}}}
	out, err = svc.AbortMerge(ctx, "/work/game")
	require.NoError(t, err)
	assert.True(t, out.Merge.Aborted)
	assert.True(t, d.merged.GetAbort())
}

func TestService_Conflicts(t *testing.T) {
	svc, d := newConflictService(t)
	d.conflicts = mergeSet()
	set, err := svc.Conflicts(ctx, "/work/game")
	require.NoError(t, err)
	assert.Equal(t, &ConflictSet{
		Op:     "merge",
		Base:   ConflictRevision{ID: "b1", Label: "merge base"},
		Ours:   ConflictRevision{ID: "o1", Label: "main"},
		Theirs: ConflictRevision{ID: "t1", Label: "feature"},
		Files: []ConflictFile{
			{Path: "a.txt", Kind: "text", Viewer: "text"},
			{Path: "hero.png", Kind: "binary", Binary: true, Resolved: true, Viewer: "image"},
		},
	}, set)

	d.conflicts = &daemonpb.ConflictsResponse{}
	set, err = svc.Conflicts(ctx, "/work/game")
	require.NoError(t, err)
	assert.Empty(t, set.Op)
	assert.Empty(t, set.Files)

	d.conflictErr = errors.New("boom")
	_, err = svc.Conflicts(ctx, "/work/game")
	require.ErrorContains(t, err, "boom")
}

func TestService_ConflictTexts(t *testing.T) {
	svc, d := newConflictService(t)
	d.conflicts = mergeSet()
	d.blobs["revision:b1"] = blobText("base\n")
	d.blobs["revision:o1"] = blobText("ours\n")
	d.blobs["working:"] = blobText("<<<<<<< ours\n")
	texts, err := svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.NoError(t, err)
	assert.Equal(t, &ConflictTexts{
		Base:    ConflictText{Text: "base\n"},
		Ours:    ConflictText{Text: "ours\n"},
		Theirs:  ConflictText{Missing: true},
		Working: ConflictText{Text: "<<<<<<< ours\n"},
	}, texts)

	d.conflicts.Base.Id = ""
	texts, err = svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.NoError(t, err)
	assert.True(t, texts.Base.Missing)

	d.blobs["revision:o1"] = []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Data{Data: []byte{0xff, 0xfe}}}}
	_, err = svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.ErrorContains(t, err, "is not text")

	d.blobs["revision:o1"] = []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Info{Info: &daemonpb.BlobInfo{SizeBytes: maxConflictText + 1}}}}
	_, err = svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.ErrorContains(t, err, "too large")

	d.blobs["revision:o1"] = []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Failure{Failure: &daemonpb.OpFailure{Code: 500, Message: "offline"}}}}
	_, err = svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.ErrorContains(t, err, "offline")

	d.conflicts = &daemonpb.ConflictsResponse{}
	_, err = svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.ErrorContains(t, err, "no merge or revert")

	d.conflictErr = errors.New("boom")
	_, err = svc.ConflictTexts(ctx, "/work/game", "a.txt")
	require.ErrorContains(t, err, "boom")
}

func TestService_ConflictDiff(t *testing.T) {
	svc, d := newConflictService(t)
	d.conflicts = mergeSet()
	d.diff = []*daemonpb.DiffFileEvent{
		{Event: &daemonpb.DiffFileEvent_File{File: &daemonpb.DiffFile{Path: "other.png", Status: "modified"}}},
		{Event: &daemonpb.DiffFileEvent_File{File: &daemonpb.DiffFile{Path: "hero.png", Status: "modified", Kind: "image"}}},
	}
	diff, err := svc.ConflictDiff(ctx, "/work/game", "hero.png")
	require.NoError(t, err)
	assert.Equal(t, "hero.png", diff.Path)
	assert.Equal(t, "o1", diff.OldRevision)
	assert.Equal(t, "t1", diff.NewRevision)
	assert.Equal(t, []string{"o1", "t1"}, d.diffReq.GetRevisions())
	assert.Equal(t, []string{"hero.png"}, d.diffReq.GetPaths())

	diff, err = svc.ConflictDiff(ctx, "/work/game", "missing.png")
	require.NoError(t, err)
	assert.Nil(t, diff)

	d.streamErr = errors.New("offline")
	_, err = svc.ConflictDiff(ctx, "/work/game", "hero.png")
	require.ErrorContains(t, err, "offline")

	d.conflicts.Theirs.Id = ""
	diff, err = svc.ConflictDiff(ctx, "/work/game", "hero.png")
	require.NoError(t, err)
	assert.Nil(t, diff)

	d.conflicts = &daemonpb.ConflictsResponse{}
	_, err = svc.ConflictDiff(ctx, "/work/game", "hero.png")
	require.ErrorContains(t, err, "no merge or revert")

	d.conflictErr = errors.New("boom")
	_, err = svc.ConflictDiff(ctx, "/work/game", "hero.png")
	require.ErrorContains(t, err, "boom")
}

func TestService_ResolveConflict(t *testing.T) {
	svc, d := newConflictService(t)
	d.status = &daemonpb.StatusResponse{Branch: "main", Conflicts: []string{"a.txt"}, Resolved: []string{"a.txt"}, Pending: "merge"}

	_, err := svc.ResolveConflict(ctx, "/work/game", ResolveInput{Path: "a.txt", Choice: "mine"})
	require.ErrorContains(t, err, "unknown choice")
	_, err = svc.ResolveConflict(ctx, "/work/game", ResolveInput{Choice: "ours"})
	require.ErrorContains(t, err, "choose a file")

	out, err := svc.ResolveConflict(ctx, "/work/game", ResolveInput{Path: "a.txt", Choice: "content", Content: "fixed\n"})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.txt"}, out.Resolved)
	assert.Equal(t, "merge", out.Pending)
	assert.Equal(t, "content", d.resolved.GetChoice())
	assert.Equal(t, []byte("fixed\n"), d.resolved.GetContent())
}
