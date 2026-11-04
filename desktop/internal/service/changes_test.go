package service

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	greet "github.com/nipalab/nipa/internal/grpc/pb"
)

type listStream[T any] struct {
	grpc.ClientStream
	items []*T
	err   error
}

func (s *listStream[T]) Recv() (*T, error) {
	if len(s.items) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	item := s.items[0]
	s.items = s.items[1:]
	return item, nil
}

type chanStream[T any] struct {
	grpc.ClientStream
	ctx context.Context
	ch  chan *T
}

func (s *chanStream[T]) Recv() (*T, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case item, ok := <-s.ch:
		if !ok {
			return nil, io.EOF
		}
		return item, nil
	}
}

type opsDaemon struct {
	*fakeDaemon
	status    *daemonpb.StatusResponse
	callErr   error
	lastStage *daemonpb.StageRequest
	restored  []string
	diff      []*daemonpb.DiffFileEvent
	diffErr   error
	blob      []*daemonpb.BlobEvent
	blobErr   error
	op        []*daemonpb.OpEvent
	opErr     error
	pushMsg   string
}

func (d *opsDaemon) Status(context.Context, *daemonpb.StatusRequest, ...grpc.CallOption) (*daemonpb.StatusResponse, error) {
	return d.status, d.callErr
}

func (d *opsDaemon) Stage(_ context.Context, req *daemonpb.StageRequest, _ ...grpc.CallOption) (*daemonpb.StatusResponse, error) {
	d.lastStage = req
	return d.status, d.callErr
}

func (d *opsDaemon) Restore(_ context.Context, req *daemonpb.RestoreRequest, _ ...grpc.CallOption) (*daemonpb.StatusResponse, error) {
	d.restored = req.GetPaths()
	return d.status, d.callErr
}

func (d *opsDaemon) DiffFiles(context.Context, *daemonpb.DiffRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.DiffFileEvent], error) {
	if d.diffErr != nil {
		return nil, d.diffErr
	}
	return &listStream[daemonpb.DiffFileEvent]{items: d.diff}, nil
}

func (d *opsDaemon) ReadBlob(context.Context, *daemonpb.ReadBlobRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.BlobEvent], error) {
	if d.blobErr != nil {
		return nil, d.blobErr
	}
	return &listStream[daemonpb.BlobEvent]{items: d.blob}, nil
}

func (d *opsDaemon) Push(_ context.Context, req *daemonpb.PushRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	d.pushMsg = req.GetMessage()
	return d.opStream()
}

func (d *opsDaemon) Update(context.Context, *daemonpb.UpdateRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	return d.opStream()
}

func (d *opsDaemon) opStream() (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	if d.opErr != nil {
		return nil, d.opErr
	}
	return &listStream[daemonpb.OpEvent]{items: d.op}, nil
}

type opsSession struct {
	client daemonpb.NipaDaemonClient
	closed int
}

func (s *opsSession) Client() daemonpb.NipaDaemonClient { return s.client }
func (s *opsSession) Info() DaemonInfo                  { return DaemonInfo{} }
func (s *opsSession) Close() error                      { s.closed++; return nil }

type recorder struct {
	mu     sync.Mutex
	events []any
}

func (r *recorder) emit(_ string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, data)
}

func (r *recorder) snapshot() []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]any(nil), r.events...)
}

func newOps(t *testing.T) (*Service, *opsDaemon, *recorder, *int) {
	t.Helper()
	d := &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}, changes: make(chan *daemonpb.RepoChange, 4)}}
	rec := &recorder{}
	dials := 0
	svc := New(func(context.Context) (Session, error) {
		dials++
		return &opsSession{client: d}, nil
	}, &memStore{}, nil)
	svc.Events = rec.emit
	t.Cleanup(func() { _ = svc.ServiceShutdown() })
	return svc, d, rec, &dials
}

func TestService_ChangesGroupsStatus(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.status = &daemonpb.StatusResponse{
		Branch:           "main",
		Head:             &daemonpb.StatusHead{Kind: "tag", Name: "v1"},
		Staged:           []string{"src/b.go", "art/hero.png"},
		Added:            []string{"art/hero.png"},
		Deleted:          []string{"old.txt"},
		Modified:         []string{"z.txt"},
		Missing:          []string{"gone.wav"},
		Untracked:        []string{"new.csv"},
		Conflicts:        []string{"c.txt"},
		Pending:          "revert",
		PendingRemaining: 2,
	}
	changes, err := svc.Changes(ctx, "/work/game")
	require.NoError(t, err)
	require.Equal(t, Changes{
		Branch:   "main",
		Detached: "v1",
		Staged: []FileChange{
			{Path: "art/hero.png", Status: "added", Kind: "image"},
			{Path: "old.txt", Status: "deleted", Kind: "text"},
			{Path: "src/b.go", Status: "modified", Kind: "text"},
		},
		Unstaged: []FileChange{
			{Path: "gone.wav", Status: "deleted", Kind: "audio"},
			{Path: "new.csv", Status: "untracked", Kind: "table"},
			{Path: "z.txt", Status: "modified", Kind: "text"},
		},
		Conflicts:        []string{"c.txt"},
		Pending:          "revert",
		PendingRemaining: 2,
	}, changes)
	require.Equal(t, 1, d.watches, "the clone is watched once per session")

	_, err = svc.Changes(ctx, "/work/game")
	require.NoError(t, err)
	require.Equal(t, 1, d.watches)
}

func TestService_StageUnstageDiscard(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.status = &daemonpb.StatusResponse{Branch: "main"}

	changes, err := svc.Stage(ctx, "/work/game", []string{"a"})
	require.NoError(t, err)
	require.Empty(t, changes.Staged)
	require.NotNil(t, changes.Conflicts)
	require.Equal(t, []string{"a"}, d.lastStage.GetAdd())

	_, err = svc.Unstage(ctx, "/work/game", []string{"b"})
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, d.lastStage.GetUnstage())

	_, err = svc.Discard(ctx, "/work/game", []string{"c"})
	require.NoError(t, err)
	require.Equal(t, []string{"c"}, d.restored)

	d.callErr = status.Error(codes.InvalidArgument, "c has staged changes")
	_, err = svc.Discard(ctx, "/work/game", []string{"c"})
	require.EqualError(t, err, "c has staged changes")
}

func TestService_ReconnectRewatches(t *testing.T) {
	svc, d, _, dials := newOps(t)
	d.status = &daemonpb.StatusResponse{}
	_, err := svc.Changes(ctx, "/work/game")
	require.NoError(t, err)

	d.callErr = status.Error(codes.Unavailable, "daemon gone")
	_, err = svc.Changes(ctx, "/work/game")
	require.EqualError(t, err, "daemon gone")

	d.callErr = nil
	_, err = svc.Changes(ctx, "/work/game")
	require.NoError(t, err)
	require.Equal(t, 2, *dials)
	require.Equal(t, 2, d.watches, "a new daemon session re-watches the clone")

	d.watchErr["/work/lost"] = status.Error(codes.InvalidArgument, "not a clone")
	_, err = svc.Changes(ctx, "/work/lost")
	require.EqualError(t, err, "not a clone")
}

func TestService_FollowEmitsRepoChanges(t *testing.T) {
	svc, d, rec, _ := newOps(t)
	d.changes = make(chan *daemonpb.RepoChange, 2)
	d.status = &daemonpb.StatusResponse{}
	_, err := svc.Changes(ctx, "/work/game")
	require.NoError(t, err)

	d.changes <- &daemonpb.RepoChange{Paths: []string{"a.png"}}
	d.changes <- &daemonpb.RepoChange{}
	require.Eventually(t, func() bool { return len(rec.snapshot()) == 2 }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, []any{
		RepoChanged{Root: "/work/game", Paths: []string{"a.png"}},
		RepoChanged{Root: "/work/game"},
	}, rec.snapshot())

	require.NoError(t, svc.RemoveRepo(ctx, "/work/game"))
	svc.follow(&opsSession{client: d}, "/work/other")
}

func textDiff() *daemonpb.DiffFile {
	one, two := int64(1), int64(2)
	return &daemonpb.DiffFile{
		Path: "b.txt", Status: "renamed", Kind: "text", Similarity: 90,
		Old:       &daemonpb.DiffFileSide{Path: "a.txt", Mode: 0o644, SizeBytes: 3, Hash: "aa"},
		New:       &daemonpb.DiffFileSide{Path: "b.txt", Mode: 0o644, SizeBytes: 4, Hash: "bb"},
		Additions: 1, Deletions: 1,
		Hunks: []*greet.DiffHunkDetail{{
			OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2,
			Lines: []*greet.DiffLineDetail{
				{Kind: "remove", OldLine: &one, Text: "x"},
				{Kind: "add", NewLine: &one, Text: "y"},
				{Kind: "context", OldLine: &two, NewLine: &two, Text: "z", NoNewline: true},
			},
		}},
	}
}

func TestService_FileDiffConvertsHunks(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.diff = []*daemonpb.DiffFileEvent{
		{Event: &daemonpb.DiffFileEvent_File{File: &daemonpb.DiffFile{Path: "other.txt"}}},
		{Event: &daemonpb.DiffFileEvent_File{File: textDiff()}},
	}
	diff, err := svc.FileDiff(ctx, "/work/game", "a.txt")
	require.NoError(t, err)
	require.Equal(t, &FileDiff{
		Path: "b.txt", OldPath: "a.txt", Status: "renamed", Kind: "text", Similarity: 90,
		Old:       &FileSide{Path: "a.txt", Mode: 0o644, Size: 3, Hash: "aa"},
		New:       &FileSide{Path: "b.txt", Mode: 0o644, Size: 4, Hash: "bb"},
		Additions: 1, Deletions: 1,
		Hunks: []DiffHunk{{
			OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2,
			Lines: []DiffLine{
				{Kind: "removed", Old: 1, Text: "x"},
				{Kind: "added", New: 1, Text: "y"},
				{Kind: "context", Old: 2, New: 2, Text: "z", NoNewline: true},
			},
		}},
	}, diff)
}

func TestService_FileDiffErrors(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.diffErr = status.Error(codes.InvalidArgument, "bad path")
	_, err := svc.FileDiff(ctx, "/work/game", "a")
	require.EqualError(t, err, "bad path")

	d.diffErr = nil
	d.diff = []*daemonpb.DiffFileEvent{{Event: &daemonpb.DiffFileEvent_Failure{Failure: &daemonpb.OpFailure{Code: 500, Message: "boom"}}}}
	_, err = svc.FileDiff(ctx, "/work/game", "a")
	require.EqualError(t, err, "boom")

	d.diff = nil
	d.status = &daemonpb.StatusResponse{Untracked: []string{"a"}}
	d.blobErr = status.Error(codes.Internal, "blob down")
	_, err = svc.FileDiff(ctx, "/work/game", "a")
	require.EqualError(t, err, "blob down")

	d.blobErr = nil
	d.blob = []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Failure{Failure: &daemonpb.OpFailure{Code: 500, Message: "read failed"}}}}
	_, err = svc.FileDiff(ctx, "/work/game", "a")
	require.EqualError(t, err, "read failed")

	d.blob = []*daemonpb.BlobEvent{{Event: &daemonpb.BlobEvent_Failure{Failure: &daemonpb.OpFailure{Code: 404, Message: "a does not exist"}}}}
	diff, err := svc.FileDiff(ctx, "/work/game", "a")
	require.NoError(t, err)
	require.Nil(t, diff, "a file that vanished has no diff")

	d.blob = nil
	diff, err = svc.FileDiff(ctx, "/work/game", "a")
	require.NoError(t, err)
	require.Nil(t, diff)

	d.status = &daemonpb.StatusResponse{Staged: []string{"a"}}
	d.blob = []*daemonpb.BlobEvent{blobInfo("a", 3, false), blobData("abc")}
	diff, err = svc.FileDiff(ctx, "/work/game", "a")
	require.NoError(t, err)
	require.Nil(t, diff, "a tracked file matching the snapshot is not an addition")

	d.callErr = status.Error(codes.InvalidArgument, "status failed")
	_, err = svc.FileDiff(ctx, "/work/game", "a")
	require.EqualError(t, err, "status failed")
}

func blobInfo(path string, size int64, binary bool) *daemonpb.BlobEvent {
	return &daemonpb.BlobEvent{Event: &daemonpb.BlobEvent_Info{Info: &daemonpb.BlobInfo{Path: path, SizeBytes: size, IsBinary: binary, Kind: "text"}}}
}

func blobData(data string) *daemonpb.BlobEvent {
	return &daemonpb.BlobEvent{Event: &daemonpb.BlobEvent_Data{Data: []byte(data)}}
}

func TestService_FileDiffUntracked(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.status = &daemonpb.StatusResponse{Untracked: []string{"new.txt", "empty.txt", "big.txt", "hero.png"}}
	d.blob = []*daemonpb.BlobEvent{blobInfo("new.txt", 7, false), blobData("one\n"), blobData("two")}
	diff, err := svc.FileDiff(ctx, "/work/game", "new.txt")
	require.NoError(t, err)
	require.True(t, diff.Untracked)
	require.Equal(t, "added", diff.Status)
	require.False(t, diff.HunksOmitted)
	require.EqualValues(t, 2, diff.Additions)
	require.Equal(t, []DiffLine{
		{Kind: "added", New: 1, Text: "one"},
		{Kind: "added", New: 2, Text: "two", NoNewline: true},
	}, diff.Hunks[0].Lines)

	d.blob = []*daemonpb.BlobEvent{blobInfo("empty.txt", 0, false)}
	diff, err = svc.FileDiff(ctx, "/work/game", "empty.txt")
	require.NoError(t, err)
	require.Empty(t, diff.Hunks)
	require.False(t, diff.HunksOmitted)

	d.blob = []*daemonpb.BlobEvent{blobInfo("big.txt", maxUntrackedText+1, false)}
	diff, err = svc.FileDiff(ctx, "/work/game", "big.txt")
	require.NoError(t, err)
	require.True(t, diff.HunksOmitted)

	d.blob = []*daemonpb.BlobEvent{blobInfo("hero.png", 10, true)}
	diff, err = svc.FileDiff(ctx, "/work/game", "hero.png")
	require.NoError(t, err)
	require.False(t, diff.HunksOmitted)
	require.True(t, diff.New.Binary)
}

func opEvents(events ...*daemonpb.OpEvent) []*daemonpb.OpEvent { return events }

func TestService_PushStreamsProgress(t *testing.T) {
	svc, d, rec, _ := newOps(t)
	d.op = opEvents(
		&daemonpb.OpEvent{Event: &daemonpb.OpEvent_Queued{Queued: &daemonpb.OpQueued{Ahead: 1}}},
		&daemonpb.OpEvent{Event: &daemonpb.OpEvent_Started{Started: &daemonpb.OpStarted{Phase: "push"}}},
		&daemonpb.OpEvent{Event: &daemonpb.OpEvent_Progress{Progress: &daemonpb.OpProgress{Phase: "upload", ObjectsDone: 1, ObjectsTotal: 2, BytesDone: 10, BytesTotal: 20}}},
		&daemonpb.OpEvent{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Push{Push: &daemonpb.PushResult{CommitId: "c1", CommitHash: "h1"}}}}},
	)
	out, err := svc.Push(ctx, "/work/game", "Add hero")
	require.NoError(t, err)
	require.Equal(t, OpOutcome{CommitID: "c1", CommitHash: "h1"}, out)
	require.Equal(t, "Add hero", d.pushMsg)
	require.Equal(t, []any{
		OpProgress{Root: "/work/game", Op: "push", Phase: "queued", Ahead: 1},
		OpProgress{Root: "/work/game", Op: "push", Phase: "push"},
		OpProgress{Root: "/work/game", Op: "push", Phase: "upload", ObjectsDone: 1, ObjectsTotal: 2, BytesDone: 10, BytesTotal: 20},
	}, rec.snapshot())
}

func TestService_UpdateOutcomeAndFailures(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.op = opEvents(&daemonpb.OpEvent{Event: &daemonpb.OpEvent_Result{Result: &daemonpb.OpResult{Outcome: &daemonpb.OpResult_Sync{Sync: &daemonpb.SyncResult{Branch: "main", CommitId: "c2"}}}}})
	out, err := svc.Update(ctx, "/work/game")
	require.NoError(t, err)
	require.Equal(t, OpOutcome{Branch: "main", CommitID: "c2"}, out)

	d.op = opEvents(&daemonpb.OpEvent{Event: &daemonpb.OpEvent_Failure{Failure: &daemonpb.OpFailure{Code: 409, Message: "push rejected"}}})
	_, err = svc.Update(ctx, "/work/game")
	require.EqualError(t, err, "push rejected")

	d.op = nil
	_, err = svc.Update(ctx, "/work/game")
	require.EqualError(t, err, "update ended without a result")

	d.opErr = status.Error(codes.Internal, "no stream")
	_, err = svc.Push(ctx, "/work/game", "m")
	require.EqualError(t, err, "no stream")

	d.opErr = nil
	d.watchErr["/work/lost"] = status.Error(codes.InvalidArgument, "not a clone")
	_, err = svc.Push(ctx, "/work/lost", "m")
	require.EqualError(t, err, "not a clone")
}

type erroringOps struct {
	*opsDaemon
	err error
}

func (e *erroringOps) Push(context.Context, *daemonpb.PushRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.OpEvent], error) {
	return &listStream[daemonpb.OpEvent]{err: e.err}, nil
}

func (e *erroringOps) DiffFiles(context.Context, *daemonpb.DiffRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.DiffFileEvent], error) {
	return &listStream[daemonpb.DiffFileEvent]{err: e.err}, nil
}

func (e *erroringOps) ReadBlob(context.Context, *daemonpb.ReadBlobRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[daemonpb.BlobEvent], error) {
	return &listStream[daemonpb.BlobEvent]{err: e.err}, nil
}

func TestService_StreamErrorsMidway(t *testing.T) {
	inner := &opsDaemon{fakeDaemon: &fakeDaemon{watchErr: map[string]error{}}}
	d := &erroringOps{opsDaemon: inner, err: status.Error(codes.Unavailable, "daemon gone")}
	svc := New(func(context.Context) (Session, error) { return &opsSession{client: d}, nil }, &memStore{}, nil)

	_, err := svc.Push(ctx, "/work/game", "m")
	require.EqualError(t, err, "daemon gone")
	_, err = svc.FileDiff(ctx, "/work/game", "a")
	require.EqualError(t, err, "daemon gone")
	_, err = svc.untrackedDiff(ctx, d, "/work/game", "a")
	require.EqualError(t, err, "daemon gone")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	d.err = errors.New("stream cancelled")
	_, err = svc.Push(cancelled, "/work/game", "m")
	require.ErrorIs(t, err, context.Canceled)
}

func TestService_FollowerEndForgetsRoot(t *testing.T) {
	svc, d, _, _ := newOps(t)
	d.changes = nil
	d.status = &daemonpb.StatusResponse{}
	_, err := svc.Changes(ctx, "/work/game")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		_, ok := svc.watched["/work/game"]
		return !ok
	}, 2*time.Second, 5*time.Millisecond, "a follower that ended must not keep the root marked as watched")

	d.changes = make(chan *daemonpb.RepoChange, 1)
	_, err = svc.Changes(ctx, "/work/game")
	require.NoError(t, err)
	d.changes <- &daemonpb.RepoChange{}
	require.Eventually(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		_, ok := svc.watched["/work/game"]
		return ok && len(d.changes) == 0
	}, 2*time.Second, 5*time.Millisecond, "the next call follows the clone again")
}
