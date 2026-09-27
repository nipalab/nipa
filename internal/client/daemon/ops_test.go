package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type stubUpdateRunner struct {
	run      func(ctx context.Context, root string, progress ...usecase.DownloadProgress) error
	switchFn func(ctx context.Context, root, branch string, progress ...usecase.DownloadProgress) error
}

func (s *stubUpdateRunner) Run(ctx context.Context, root string, progress ...usecase.DownloadProgress) error {
	if s.run != nil {
		return s.run(ctx, root, progress...)
	}
	return nil
}

func (s *stubUpdateRunner) Switch(ctx context.Context, root, branch string, progress ...usecase.DownloadProgress) error {
	if s.switchFn != nil {
		return s.switchFn(ctx, root, branch, progress...)
	}
	return nil
}

type stubPushRunner struct {
	run func(ctx context.Context, root, message string, progress ...usecase.UploadProgress) error
}

func (s *stubPushRunner) Run(ctx context.Context, root, message string, progress ...usecase.UploadProgress) error {
	if s.run != nil {
		return s.run(ctx, root, message, progress...)
	}
	return nil
}

type stubMergeRunner struct {
	run func(ctx context.Context, root, sourceBranch string, opts usecase.MergeOptions) (*usecase.Outcome, error)
}

func (s *stubMergeRunner) Run(ctx context.Context, root, sourceBranch string, opts usecase.MergeOptions) (*usecase.Outcome, error) {
	if s.run != nil {
		return s.run(ctx, root, sourceBranch, opts)
	}
	return &usecase.Outcome{}, nil
}

type stubRevertRunner struct {
	run func(ctx context.Context, root, target string, opts usecase.RevertOptions, progress ...usecase.UploadProgress) (*usecase.RevertOutcome, error)
}

func (s *stubRevertRunner) Run(ctx context.Context, root, target string, opts usecase.RevertOptions, progress ...usecase.UploadProgress) (*usecase.RevertOutcome, error) {
	if s.run != nil {
		return s.run(ctx, root, target, opts, progress...)
	}
	return &usecase.RevertOutcome{}, nil
}

// opTestStream records the events a streaming handler sends. failAt, when
// positive, makes that 1-based send fail.
type opTestStream struct {
	grpc.ServerStream
	ctx    context.Context
	failAt int

	mu   sync.Mutex
	sent []*daemonpb.OpEvent
}

func (s *opTestStream) Context() context.Context {
	return s.ctx
}

func (s *opTestStream) Send(ev *daemonpb.OpEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failAt > 0 && len(s.sent)+1 == s.failAt {
		return errors.New("stream closed")
	}
	s.sent = append(s.sent, ev)
	return nil
}

func (s *opTestStream) events() []*daemonpb.OpEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*daemonpb.OpEvent(nil), s.sent...)
}

func newOpServer(t *testing.T, ops RepoOps) *Server {
	t.Helper()
	srv, err := NewServer(Options{
		EndpointPath: filepath.Join(t.TempDir(), "daemon.json"),
		Runners:      Runners{New: func(string) RepoOps { return ops }},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		srv.Stop()
		srv.repos.closeAll()
		_ = srv.listener.Close()
	})
	return srv
}

func progressEvents(events []*daemonpb.OpEvent) []*daemonpb.OpProgress {
	var out []*daemonpb.OpProgress
	for _, ev := range events {
		if p := ev.GetProgress(); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func terminalEvent(t *testing.T, events []*daemonpb.OpEvent) *daemonpb.OpEvent {
	t.Helper()
	require.NotEmpty(t, events)
	last := events[len(events)-1]
	require.NotNil(t, last.GetResult(), "the stream must end with a result: %v", last)
	return last
}

func TestServer_UpdateStreamsProgressAndResult(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Update: &stubUpdateRunner{run: func(_ context.Context, _ string, progress ...usecase.DownloadProgress) error {
		progress[0].DownloadStart(2, 100)
		progress[0].DownloadProgress(1, 40)
		progress[0].DownloadEnd()
		return nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	seedCommit(t, root, "commit123", "hash123")

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Update(&daemonpb.UpdateRequest{Root: root}, stream))

	events := stream.events()
	require.Equal(t, "update", events[0].GetStarted().GetPhase())
	progress := progressEvents(events)
	require.Len(t, progress, 3)
	require.Equal(t, "download", progress[0].GetPhase())
	require.EqualValues(t, 2, progress[0].GetObjectsTotal())
	require.EqualValues(t, 100, progress[0].GetBytesTotal())
	require.EqualValues(t, 1, progress[1].GetObjectsDone())
	require.EqualValues(t, 40, progress[1].GetBytesDone())
	require.EqualValues(t, 2, progress[2].GetObjectsDone(), "the end event reports the totals")

	sync := terminalEvent(t, events).GetResult().GetSync()
	require.Equal(t, "main", sync.GetBranch())
	require.Equal(t, "commit123", sync.GetCommitId())
}

func TestServer_UpdateWithoutRunnerFails(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Update(&daemonpb.UpdateRequest{Root: root}, stream))

	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 409, last.GetFailure().GetCode())
	require.Contains(t, last.GetFailure().GetMessage(), "not configured")
}

func TestServer_PushReportsCommitAndTree(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Push: &stubPushRunner{}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	seedCommit(t, root, "pushed1", "hash1")

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Push(&daemonpb.PushRequest{Root: root, Message: "m"}, stream))

	push := terminalEvent(t, stream.events()).GetResult().GetPush()
	require.Equal(t, "pushed1", push.GetCommitId())
	require.Equal(t, "hash1", push.GetCommitHash())
	require.NotEmpty(t, push.GetTreeHash(), "the tree hash comes from the refreshed snapshot")
}

func TestServer_PushFailureIsMapped(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Push: &stubPushRunner{run: func(context.Context, string, string, ...usecase.UploadProgress) error {
		return clientDomain.NewTokenError("bad credentials")
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Push(&daemonpb.PushRequest{Root: root}, stream))

	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 401, last.GetFailure().GetCode())
	require.Equal(t, "bad credentials", last.GetFailure().GetMessage())
}

func TestServer_MergeConflictsAreAResult(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Merge: &stubMergeRunner{run: func(_ context.Context, _ string, sourceBranch string, _ usecase.MergeOptions) (*usecase.Outcome, error) {
		require.Equal(t, "feature", sourceBranch)
		return &usecase.Outcome{Conflicts: []string{"a.txt"}}, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Merge(&daemonpb.MergeOpRequest{Root: root, SourceBranch: "feature"}, stream))

	merge := terminalEvent(t, stream.events()).GetResult().GetMerge()
	require.Equal(t, []string{"a.txt"}, merge.GetConflicts())
	require.False(t, merge.GetAborted())
}

func TestServer_MergeAbortIsMarked(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Merge: &stubMergeRunner{}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Merge(&daemonpb.MergeOpRequest{Root: root, Abort: true}, stream))

	merge := terminalEvent(t, stream.events()).GetResult().GetMerge()
	require.True(t, merge.GetAborted())
}

func TestServer_RevertReportsOutcome(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Revert: &stubRevertRunner{run: func(_ context.Context, _ string, target string, opts usecase.RevertOptions, _ ...usecase.UploadProgress) (*usecase.RevertOutcome, error) {
		require.Equal(t, "abc123", target)
		require.True(t, opts.NoCommit)
		return &usecase.RevertOutcome{Committed: true}, nil
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Revert(&daemonpb.RevertOpRequest{Root: root, Target: "abc123", NoCommit: true}, stream))

	revert := terminalEvent(t, stream.events()).GetResult().GetRevert()
	require.True(t, revert.GetCommitted())
}

func TestServer_QueuedEventWhileSlotBusy(t *testing.T) {
	root := newTestClone(t)
	released := make(chan struct{})
	srv := newOpServer(t, RepoOps{Push: &stubPushRunner{run: func(context.Context, string, string, ...usecase.UploadProgress) error {
		<-released
		return nil
	}}})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)

	holder, _, err := rp.coord.Acquire(context.Background(), true)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	done := make(chan error, 1)
	go func() {
		done <- srv.Push(&daemonpb.PushRequest{Root: root, Message: "m"}, stream)
	}()

	require.Eventually(t, func() bool {
		events := stream.events()
		return len(events) > 0 && events[0].GetQueued() != nil
	}, 2*time.Second, time.Millisecond, "a busy slot must emit a queued event first")

	holder()
	close(released)
	require.NoError(t, <-done)

	events := stream.events()
	require.NotNil(t, events[0].GetQueued())
	require.Equal(t, "push", events[1].GetStarted().GetPhase())
	require.NotNil(t, events[len(events)-1].GetResult())
}

func TestServer_QueuedOperationCancel(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Push: &stubPushRunner{}})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)

	holder, _, err := rp.coord.Acquire(context.Background(), true)
	require.NoError(t, err)
	defer holder()

	ctx, cancel := context.WithCancel(context.Background())
	stream := &opTestStream{ctx: ctx}
	done := make(chan error, 1)
	go func() {
		done <- srv.Push(&daemonpb.PushRequest{Root: root, Message: "m"}, stream)
	}()

	require.Eventually(t, func() bool {
		events := stream.events()
		return len(events) > 0 && events[0].GetQueued() != nil
	}, 2*time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)

	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 499, last.GetFailure().GetCode())
}

func TestServer_CancelWhileRunning(t *testing.T) {
	root := newTestClone(t)
	ctx, cancel := context.WithCancel(context.Background())
	srv := newOpServer(t, RepoOps{Push: &stubPushRunner{run: func(ctx context.Context, _, _ string, _ ...usecase.UploadProgress) error {
		<-ctx.Done()
		return ctx.Err()
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: ctx}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	require.NoError(t, srv.Push(&daemonpb.PushRequest{Root: root, Message: "m"}, stream))

	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 499, last.GetFailure().GetCode())
}

func TestServer_UnwatchedRootIsFailureEvent(t *testing.T) {
	srv := newOpServer(t, RepoOps{})
	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Update(&daemonpb.UpdateRequest{Root: newTestClone(t)}, stream))

	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 400, last.GetFailure().GetCode())
	require.Contains(t, last.GetFailure().GetMessage(), "not watched")
}

func TestServer_ShutdownDrainsRunningOperation(t *testing.T) {
	root := newTestClone(t)
	entered := make(chan struct{})
	released := make(chan struct{})
	srv := startTestServer(t, Options{
		Runners: Runners{New: func(string) RepoOps {
			return RepoOps{Push: &stubPushRunner{run: func(context.Context, string, string, ...usecase.UploadProgress) error {
				close(entered)
				<-released
				return nil
			}}}
		}},
	})
	dctx := authed(context.Background(), srv.Endpoint().Token)

	_, err := srv.client.WatchRepo(dctx, &daemonpb.WatchRepoRequest{Root: root})
	require.NoError(t, err)

	stream, err := srv.client.Push(dctx, &daemonpb.PushRequest{Root: root, Message: "m"})
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the push did not start")
	}

	_, err = srv.client.Shutdown(dctx, &daemonpb.ShutdownRequest{})
	require.NoError(t, err)

	// The running operation must still complete; the daemon exits afterwards.
	close(released)
	var events []*daemonpb.OpEvent
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		events = append(events, ev)
	}
	require.NotEmpty(t, events)
	require.NotNil(t, events[len(events)-1].GetResult(), "the drained operation must deliver its result")

	require.Eventually(t, func() bool {
		_, err := ReadEndpoint(srv.endpointPath)
		return errors.Is(err, os.ErrNotExist)
	}, 5*time.Second, 10*time.Millisecond, "the endpoint is removed only after the drain completes")
}

func TestServer_SwitchStreamsResult(t *testing.T) {
	root := newTestClone(t)
	var gotBranch string
	srv := newOpServer(t, RepoOps{Update: &stubUpdateRunner{
		switchFn: func(_ context.Context, _, branch string, _ ...usecase.DownloadProgress) error {
			gotBranch = branch
			return nil
		},
	}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	seedCommit(t, root, "switched1", "hash1")

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Switch(&daemonpb.SwitchRequest{Root: root, Branch: "feature"}, stream))
	require.Equal(t, "feature", gotBranch)

	sync := terminalEvent(t, stream.events()).GetResult().GetSync()
	require.Equal(t, "feature", sync.GetBranch())
	require.Equal(t, "switched1", sync.GetCommitId())
}

func TestServer_SwitchFailures(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Switch(&daemonpb.SwitchRequest{Root: root, Branch: "feature"}, stream))
	last := stream.events()[len(stream.events())-1]
	require.EqualValues(t, 409, last.GetFailure().GetCode())
	require.Contains(t, last.GetFailure().GetMessage(), "switch is not configured")

	failingRoot := newTestClone(t)
	failing := newOpServer(t, RepoOps{Update: &stubUpdateRunner{
		switchFn: func(context.Context, string, string, ...usecase.DownloadProgress) error {
			return clientDomain.NewTokenError("nope")
		},
	}})
	_, err = failing.repos.watch(failingRoot)
	require.NoError(t, err)
	stream = &opTestStream{ctx: context.Background()}
	require.NoError(t, failing.Switch(&daemonpb.SwitchRequest{Root: failingRoot, Branch: "feature"}, stream))
	last = stream.events()[len(stream.events())-1]
	require.EqualValues(t, 401, last.GetFailure().GetCode())
}

func TestServer_OpRunnerFailures(t *testing.T) {
	root := newTestClone(t)
	boom := clientDomain.NewUserError("runner exploded")
	srv := newOpServer(t, RepoOps{
		Update: &stubUpdateRunner{run: func(context.Context, string, ...usecase.DownloadProgress) error { return boom }},
		Push: &stubPushRunner{run: func(context.Context, string, string, ...usecase.UploadProgress) error {
			return boom
		}},
		Merge: &stubMergeRunner{run: func(context.Context, string, string, usecase.MergeOptions) (*usecase.Outcome, error) {
			return nil, boom
		}},
		Revert: &stubRevertRunner{run: func(context.Context, string, string, usecase.RevertOptions, ...usecase.UploadProgress) (*usecase.RevertOutcome, error) {
			return nil, boom
		}},
	})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	tests := []struct {
		name string
		call func(*opTestStream) error
	}{
		{"update", func(s *opTestStream) error { return srv.Update(&daemonpb.UpdateRequest{Root: root}, s) }},
		{"push", func(s *opTestStream) error { return srv.Push(&daemonpb.PushRequest{Root: root}, s) }},
		{"merge", func(s *opTestStream) error {
			return srv.Merge(&daemonpb.MergeOpRequest{Root: root, SourceBranch: "feature"}, s)
		}},
		{"revert", func(s *opTestStream) error {
			return srv.Revert(&daemonpb.RevertOpRequest{Root: root, Target: "abc123"}, s)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &opTestStream{ctx: context.Background()}
			require.NoError(t, tt.call(stream))
			last := stream.events()[len(stream.events())-1]
			require.EqualValues(t, 400, last.GetFailure().GetCode())
			require.Equal(t, "runner exploded", last.GetFailure().GetMessage())
		})
	}
}

func TestServer_OpWithoutRunnerFails(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)

	tests := []struct {
		name string
		call func(*opTestStream) error
	}{
		{"update", func(s *opTestStream) error { return srv.Update(&daemonpb.UpdateRequest{Root: root}, s) }},
		{"push", func(s *opTestStream) error { return srv.Push(&daemonpb.PushRequest{Root: root}, s) }},
		{"merge", func(s *opTestStream) error { return srv.Merge(&daemonpb.MergeOpRequest{Root: root}, s) }},
		{"revert", func(s *opTestStream) error { return srv.Revert(&daemonpb.RevertOpRequest{Root: root}, s) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := &opTestStream{ctx: context.Background()}
			require.NoError(t, tt.call(stream))
			last := stream.events()[len(stream.events())-1]
			require.EqualValues(t, 409, last.GetFailure().GetCode())
			require.Contains(t, last.GetFailure().GetMessage(), "not configured")
		})
	}
}

func TestServer_OpSendFailures(t *testing.T) {
	newPushServer := func(t *testing.T, run func(context.Context, string, string, ...usecase.UploadProgress) error) *Server {
		t.Helper()
		root := newTestClone(t)
		srv := newOpServer(t, RepoOps{Push: &stubPushRunner{run: run}})
		_, err := srv.repos.watch(root)
		require.NoError(t, err)
		return srv
	}
	rootOf := func(srv *Server) string {
		repos := srv.repos.list()
		require.Len(t, repos, 1)
		return repos[0].root
	}

	t.Run("started", func(t *testing.T) {
		srv := newPushServer(t, nil)
		stream := &opTestStream{ctx: context.Background(), failAt: 1}
		require.Error(t, srv.Push(&daemonpb.PushRequest{Root: rootOf(srv)}, stream))
	})

	t.Run("queued", func(t *testing.T) {
		srv := newPushServer(t, nil)
		root := rootOf(srv)
		rp, err := srv.repos.watch(root)
		require.NoError(t, err)
		holder, _, err := rp.coord.Acquire(context.Background(), true)
		require.NoError(t, err)
		defer holder()

		stream := &opTestStream{ctx: context.Background(), failAt: 1}
		require.Error(t, srv.Push(&daemonpb.PushRequest{Root: root}, stream))
	})

	t.Run("progress", func(t *testing.T) {
		release := make(chan struct{})
		srv := newPushServer(t, func(_ context.Context, _, _ string, progress ...usecase.UploadProgress) error {
			progress[0].UploadStart(1, 10)
			<-release
			return nil
		})
		stream := &opTestStream{ctx: context.Background(), failAt: 2}
		done := make(chan error, 1)
		go func() { done <- srv.Push(&daemonpb.PushRequest{Root: rootOf(srv)}, stream) }()
		require.Eventually(t, func() bool { return len(stream.events()) == 1 }, 2*time.Second, time.Millisecond,
			"the handler must reach the failing progress send while the operation runs")
		close(release)
		require.Error(t, <-done)
	})

	t.Run("terminal", func(t *testing.T) {
		srv := newPushServer(t, nil)
		stream := &opTestStream{ctx: context.Background(), failAt: 2}
		require.Error(t, srv.Push(&daemonpb.PushRequest{Root: rootOf(srv)}, stream))
	})
}

func TestServer_CancelKeepsSlotUntilOpStops(t *testing.T) {
	root := newTestClone(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := newOpServer(t, RepoOps{Push: &stubPushRunner{run: func(ctx context.Context, _, _ string, _ ...usecase.UploadProgress) error {
		close(entered)
		<-release
		return ctx.Err()
	}}})
	rp, err := srv.repos.watch(root)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	stream := &opTestStream{ctx: ctx}
	done := make(chan error, 1)
	go func() { done <- srv.Push(&daemonpb.PushRequest{Root: root}, stream) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the push did not start")
	}

	cancel()
	select {
	case <-done:
		t.Fatal("the handler released the slot while the operation was still running")
	case <-time.After(50 * time.Millisecond):
	}
	busy, _ := rp.coord.pending()
	require.True(t, busy, "the abandoned operation still holds the exclusive slot")

	close(release)
	require.NoError(t, <-done)
}

func TestServer_UpdateReportsReloadedBranch(t *testing.T) {
	root := newTestClone(t)
	srv := newOpServer(t, RepoOps{Update: &stubUpdateRunner{run: func(_ context.Context, _ string, _ ...usecase.DownloadProgress) error {
		lr := localrepo.NewLocalRepoWithTarget(root)
		return lr.SaveConfig(clientDomain.Config{
			Url:    "https://nipa.example.com/default/default",
			Branch: "feature",
		})
	}}})
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	seedCommit(t, root, "commit1", "hash1")

	stream := &opTestStream{ctx: context.Background()}
	require.NoError(t, srv.Update(&daemonpb.UpdateRequest{Root: root}, stream))
	sync := terminalEvent(t, stream.events()).GetResult().GetSync()
	require.Equal(t, "feature", sync.GetBranch(), "the result reflects the config rewritten by the operation")
}

func TestFailureCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int32
	}{
		{"domain", clientDomain.NewUserError("x"), 400},
		{"token", clientDomain.NewTokenError("x"), 401},
		{"canceled", context.Canceled, 499},
		{"deadline", context.DeadlineExceeded, 504},
		{"invalid argument", status.Error(codes.InvalidArgument, "x"), 400},
		{"unauthenticated", status.Error(codes.Unauthenticated, "x"), 401},
		{"permission denied", status.Error(codes.PermissionDenied, "x"), 403},
		{"not found", status.Error(codes.NotFound, "x"), 404},
		{"failed precondition", status.Error(codes.FailedPrecondition, "x"), 409},
		{"unimplemented", status.Error(codes.Unimplemented, "x"), 501},
		{"unknown", errors.New("x"), 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, failureCode(tt.err))
		})
	}
}

func seedCommit(t *testing.T, root, commitID, commitHash string) {
	t.Helper()
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveCommit(commitID, commitHash))
	require.NoError(t, lr.Close())
}
