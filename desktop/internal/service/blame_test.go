package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

type blameDaemon struct {
	*opsDaemon
	events  []*daemonpb.BlameEvent
	err     error
	streamE error
	req     *daemonpb.BlameRequest
	godot   *daemonpb.MergeGodotResourceResponse
	godotE  error
	godotRq *daemonpb.MergeGodotResourceRequest
}

func (d *blameDaemon) Blame(_ context.Context, req *daemonpb.BlameRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.BlameEvent], error) {
	d.req = req
	if d.err != nil {
		return nil, d.err
	}
	return &listStream[daemonpb.BlameEvent]{items: append([]*daemonpb.BlameEvent(nil), d.events...), err: d.streamE}, nil
}

func (d *blameDaemon) MergeGodotResource(_ context.Context, req *daemonpb.MergeGodotResourceRequest, _ ...grpc.CallOption) (*daemonpb.MergeGodotResourceResponse, error) {
	d.godotRq = req
	return d.godot, d.godotE
}

func newBlameService(t *testing.T) (*Service, *blameDaemon) {
	t.Helper()
	d := &blameDaemon{opsDaemon: &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d
}

func TestService_Blame(t *testing.T) {
	svc, d := newBlameService(t)
	d.events = []*daemonpb.BlameEvent{
		{Event: &daemonpb.BlameEvent_Progress{Progress: &daemonpb.BlameProgress{Done: 1, Total: 2}}},
		{Event: &daemonpb.BlameEvent_Result{Result: &daemonpb.BlameResult{
			Path:      "src/a.cs",
			Revision:  "c9",
			Truncated: true,
			Lines:     []*daemonpb.BlameLine{{Text: "x", CommitId: "c1"}, {Text: "y", Uncommitted: true}, {Text: "z"}},
			Commits:   []*daemonpb.BlameCommit{{Id: "c1", Hash: "ff", AuthorName: "Ana", AuthorEmail: "ana@x", Message: "m", CreatedAt: "2026-11-04T09:00:00Z"}},
		}}},
	}
	got, err := svc.Blame(ctx, "/work/game", "src/a.cs", "c9")
	require.NoError(t, err)
	require.Equal(t, "src/a.cs", d.req.GetPath())
	require.Equal(t, "c9", d.req.GetRevision())
	require.Equal(t, "c9", got.Revision)
	require.True(t, got.Truncated)
	require.Equal(t, []BlameLine{{CommitID: "c1"}, {Uncommitted: true}, {}}, got.Lines)
	require.Equal(t, []Commit{{ID: "c1", Hash: "ff", Parents: []string{}, Author: "Ana", Email: "ana@x", Message: "m", Date: "2026-11-04T09:00:00Z"}}, got.Commits)
}

func TestService_BlameFailures(t *testing.T) {
	svc, d := newBlameService(t)
	d.events = []*daemonpb.BlameEvent{{Event: &daemonpb.BlameEvent_Failure{Failure: &daemonpb.OpFailure{Code: 400, Message: "a.png is not a text file"}}}}
	_, err := svc.Blame(ctx, "/work/game", "a.png", "")
	require.EqualError(t, err, "a.png is not a text file")

	d.events = nil
	_, err = svc.Blame(ctx, "/work/game", "a.txt", "")
	require.ErrorContains(t, err, "without a result")

	d.streamE = status.Error(codes.Internal, "boom")
	_, err = svc.Blame(ctx, "/work/game", "a.txt", "")
	require.ErrorContains(t, err, "boom")

	d.err = status.Error(codes.Unavailable, "gone")
	_, err = svc.Blame(ctx, "/work/game", "a.txt", "")
	require.Error(t, err)

	broken := New(func(context.Context) (Session, error) { return nil, errors.New("no daemon") }, &memStore{}, nil)
	_, err = broken.Blame(ctx, "/work/game", "a.txt", "")
	require.Error(t, err)
}

func TestService_MergeScene(t *testing.T) {
	svc, d := newBlameService(t)
	d.godot = &daemonpb.MergeGodotResourceResponse{Merged: []byte("[gd_scene]\n"), Clean: false, Conflicts: []*daemonpb.GodotMergeConflict{{Section: `node name="Player"`, Key: "position", Message: "changed on both sides"}}}
	got, err := svc.MergeScene(ctx, "/work/game", "level.tscn")
	require.NoError(t, err)
	require.Equal(t, "level.tscn", d.godotRq.GetPath())
	require.Equal(t, "/work/game", d.godotRq.GetRoot())
	require.Equal(t, &SceneMerge{Merged: "[gd_scene]\n", Conflicts: []SceneMergeConflict{{Section: `node name="Player"`, Key: "position", Message: "changed on both sides"}}}, got)

	d.godot = &daemonpb.MergeGodotResourceResponse{Merged: []byte("ok"), Clean: true}
	got, err = svc.MergeScene(ctx, "/work/game", "level.tscn")
	require.NoError(t, err)
	require.True(t, got.Clean)
	require.Empty(t, got.Conflicts)

	d.godotE = status.Error(codes.InvalidArgument, "level.tscn is stored as binary; pick ours or theirs")
	_, err = svc.MergeScene(ctx, "/work/game", "level.tscn")
	require.ErrorContains(t, err, "stored as binary")
}
