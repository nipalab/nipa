package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

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

// opTestStream records the events a streaming handler sends.
type opTestStream struct {
	grpc.ServerStream
	ctx context.Context

	mu   sync.Mutex
	sent []*daemonpb.OpEvent
}

func (s *opTestStream) Context() context.Context {
	return s.ctx
}

func (s *opTestStream) Send(ev *daemonpb.OpEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func seedCommit(t *testing.T, root, commitID, commitHash string) {
	t.Helper()
	lr := localrepo.NewLocalRepoWithTarget(root)
	require.NoError(t, lr.Init(root))
	require.NoError(t, lr.SaveCommit(commitID, commitHash))
	require.NoError(t, lr.Close())
}
