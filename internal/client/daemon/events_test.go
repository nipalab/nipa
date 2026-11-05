package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type teamService struct {
	pb.NipaServiceClient

	mu        sync.Mutex
	commits   []*pb.CommitLogEntry
	logErr    error
	locks     []*pb.FileLockDetail
	locksErr  error
	requests  []*pb.MergeRequestDetail
	mrErr     error
	reqErr    error
	logAsked  []string
	listAsked []string
}

func (s *teamService) GetCommitLog(_ context.Context, req *pb.GetCommitLogRequest, _ ...grpc.CallOption) (*pb.GetCommitLogResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logAsked = append(s.logAsked, req.GetBranch())
	if s.logErr != nil {
		return nil, s.logErr
	}
	return &pb.GetCommitLogResponse{Commits: s.commits}, nil
}

func (s *teamService) ListFileLocks(context.Context, *pb.ListFileLocksRequest, ...grpc.CallOption) (*pb.ListFileLocksResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.ListFileLocksResponse{Locks: s.locks}, s.locksErr
}

func (s *teamService) ListMergeRequests(_ context.Context, req *pb.ListMergeRequestsRequest, _ ...grpc.CallOption) (*pb.ListMergeRequestsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listAsked = append(s.listAsked, req.GetStatus())
	return &pb.ListMergeRequestsResponse{MergeRequests: s.requests}, s.mrErr
}

func (s *teamService) ListMergeRequestReviewRequests(_ context.Context, req *pb.ListMergeRequestReviewRequestsRequest, _ ...grpc.CallOption) (*pb.ListMergeRequestReviewRequestsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reqErr != nil {
		return nil, s.reqErr
	}
	res := &pb.ListMergeRequestReviewRequestsResponse{}
	for _, mr := range s.requests {
		if mr.GetNumber() != req.GetNumber() {
			continue
		}
		for _, id := range mr.GetReview().GetOutstandingReviewers() {
			res.ReviewRequests = append(res.ReviewRequests, &pb.ReviewRequestDetail{Reviewer: &pb.ReviewActor{UserId: id}})
		}
	}
	return res, nil
}

func (s *teamService) set(fn func(*teamService)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func commit(id, author, message string) *pb.CommitLogEntry {
	return &pb.CommitLogEntry{CommitId: id, AuthorName: author, Message: message}
}

func review(number int64, title string, outstanding ...string) *pb.MergeRequestDetail {
	return &pb.MergeRequestDetail{Number: number, Title: title, SourceBranch: "feature", Review: &pb.MergeRequestReviewState{OutstandingReviewers: outstanding}}
}

func TestEventInterval(t *testing.T) {
	require.Equal(t, time.Minute, eventInterval(0, minEventInterval))
	require.Equal(t, minEventInterval, eventInterval(1, minEventInterval))
	require.Equal(t, 90*time.Second, eventInterval(90, minEventInterval))
	require.Equal(t, maxEventInterval, eventInterval(99999, minEventInterval))
	require.Equal(t, 2*time.Minute, eventInterval(0, 2*time.Minute))
}

func TestCountUntilAndFirstLine(t *testing.T) {
	commits := []*pb.CommitLogEntry{commit("c3", "", ""), commit("c2", "", ""), commit("c1", "", "")}
	require.Equal(t, 1, countUntil(commits, "c2"))
	require.Equal(t, 1, countUntil(commits, "zz", "c2", "c1"))
	require.Equal(t, 3, countUntil(commits, ""))
	require.Equal(t, 0, countUntil(commits, "c3"))
	require.Equal(t, "subject", firstLine("  subject \nbody"))
	require.Equal(t, "", firstLine(""))
}

func TestTeamWatch_ReportsDifferencesAfterBaseline(t *testing.T) {
	svc := &teamService{
		commits:  []*pb.CommitLogEntry{commit("c2", "Alice", "earlier"), commit("c1", "Me", "mine")},
		locks:    []*pb.FileLockDetail{{Id: "l1", Path: "hero.psd", HeldBy: "bob", HeldByName: "Bob"}, {Id: "l2", Path: "mine.psd", HeldBy: "me"}},
		requests: []*pb.MergeRequestDetail{review(4, "Old request", "me"), review(5, "Not mine", "bob")},
	}
	in := pollInput{branch: "main", pinned: "c1", user: "me"}
	project := &pb.ProjectContext{Org: "studio", Project: "game"}
	w := &teamWatch{}
	ctx := context.Background()

	act, err := w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.NotNil(t, act, "the first poll always reports")
	require.Empty(t, act.GetEvents(), "the first poll is a baseline")
	require.EqualValues(t, 1, act.GetIncoming())
	require.EqualValues(t, 1, act.GetReviewRequests())

	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.Nil(t, act, "nothing changed")

	svc.set(func(s *teamService) {
		s.commits = append([]*pb.CommitLogEntry{commit("c4", "Carol", "Paint the hero\n\nmore"), commit("c3", "Alice", "x")}, s.commits...)
		s.locks = []*pb.FileLockDetail{{Id: "l2", Path: "mine.psd", HeldBy: "me"}}
		s.requests = append(s.requests, review(6, "New hero", "me", "bob"))
	})
	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.EqualValues(t, 3, act.GetIncoming())
	require.EqualValues(t, 2, act.GetReviewRequests())
	require.Len(t, act.GetEvents(), 3)
	pushed, released, requested := act.GetEvents()[0], act.GetEvents()[1], act.GetEvents()[2]
	require.Equal(t, EventPushed, pushed.GetKind())
	require.Equal(t, "Carol", pushed.GetActor())
	require.Equal(t, "Paint the hero", pushed.GetMessage())
	require.Equal(t, "c4", pushed.GetCommitId())
	require.Equal(t, "main", pushed.GetBranch())
	require.EqualValues(t, 2, pushed.GetCount())
	require.Equal(t, EventLockReleased, released.GetKind())
	require.Equal(t, "hero.psd", released.GetPath())
	require.Equal(t, "Bob", released.GetActor())
	require.Equal(t, EventReviewRequested, requested.GetKind())
	require.EqualValues(t, 6, requested.GetNumber())
	require.Equal(t, "New hero", requested.GetMessage())
	require.Equal(t, "feature", requested.GetBranch())

	svc.set(func(s *teamService) {
		s.commits = append([]*pb.CommitLogEntry{commit("c5", "Me", "pushed here")}, s.commits...)
	})
	in.pinned = "c5"
	act, err = w.poll(ctx, svc, project, in)
	require.NoError(t, err)
	require.Empty(t, act.GetEvents(), "the clone's own push is not news")
	require.EqualValues(t, 0, act.GetIncoming())
}

func TestTeamWatch_SkipsBranchWhenDetachedOrMissing(t *testing.T) {
	svc := &teamService{commits: []*pb.CommitLogEntry{commit("c1", "A", "m")}}
	project := &pb.ProjectContext{}
	ctx := context.Background()

	w := &teamWatch{}
	act, err := w.poll(ctx, svc, project, pollInput{branch: "main", detached: true})
	require.NoError(t, err)
	require.Zero(t, act.GetIncoming())
	require.Empty(t, svc.logAsked)
	require.Empty(t, svc.listAsked, "no user, no review lookup")

	svc.logErr = status.Error(codes.NotFound, "branch gone")
	w = &teamWatch{}
	act, err = w.poll(ctx, svc, project, pollInput{branch: "gone"})
	require.NoError(t, err)
	require.Zero(t, act.GetIncoming())

	svc.logErr = nil
	svc.commits = nil
	w = &teamWatch{}
	act, err = w.poll(ctx, svc, project, pollInput{branch: "empty"})
	require.NoError(t, err)
	require.Zero(t, act.GetIncoming())
}

func TestTeamWatch_ErrorsAndRecovery(t *testing.T) {
	boom := status.Error(codes.Unavailable, "server down")
	ctx := context.Background()
	project := &pb.ProjectContext{}
	for name, svc := range map[string]*teamService{
		"log":      {logErr: boom},
		"locks":    {locksErr: boom},
		"mrs":      {mrErr: boom},
		"requests": {reqErr: boom, requests: []*pb.MergeRequestDetail{review(1, "x")}},
	} {
		_, err := (&teamWatch{}).poll(ctx, svc, project, pollInput{branch: "main", user: "me"})
		require.ErrorIs(t, err, boom, name)
	}

	w := &teamWatch{}
	svc := &teamService{}
	_, err := w.poll(ctx, svc, project, pollInput{})
	require.NoError(t, err)
	act := w.failed(boom)
	require.Equal(t, "server down", act.GetError())
	require.Nil(t, w.failed(boom), "the same failure is reported once")
	require.Equal(t, "plain", w.failed(errors.New("plain")).GetError())
	act, err = w.poll(ctx, svc, project, pollInput{})
	require.NoError(t, err)
	require.NotNil(t, act, "recovering clears the error")
	require.Empty(t, act.GetError())
}

type activityStream struct {
	grpc.ServerStream
	ctx       context.Context
	headerErr error
	sendErr   error
	sent      chan *daemonpb.RepoActivity
}

func (s *activityStream) Context() context.Context     { return s.ctx }
func (s *activityStream) SendHeader(metadata.MD) error { return s.headerErr }
func (s *activityStream) SetHeader(metadata.MD) error  { return nil }
func (s *activityStream) Send(a *daemonpb.RepoActivity) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent <- a
	return nil
}

func recvActivity(t *testing.T, stream *activityStream) *daemonpb.RepoActivity {
	t.Helper()
	select {
	case a := <-stream.sent:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("no activity")
		return nil
	}
}

func eventServer(t *testing.T, svc *teamService, user string, userErr error) (*Server, string, *identityConn) {
	t.Helper()
	srv, root, conn := lockServer(t, "main", nil, user)
	conn.client = svc
	conn.err = userErr
	seedCommit(t, root, "c1", "h1")
	srv.eventEvery = 10 * time.Millisecond
	return srv, root, conn
}

func TestServer_WatchEventsStreamsActivity(t *testing.T) {
	svc := &teamService{commits: []*pb.CommitLogEntry{commit("c1", "Me", "base")}}
	srv, root, _ := eventServer(t, svc, "me", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &activityStream{ctx: ctx, sent: make(chan *daemonpb.RepoActivity, 8)}
	done := make(chan error, 1)
	go func() { done <- srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: root, IntervalSeconds: 1}, stream) }()

	first := recvActivity(t, stream)
	require.Empty(t, first.GetEvents())
	require.Zero(t, first.GetIncoming())

	svc.set(func(s *teamService) {
		s.commits = append([]*pb.CommitLogEntry{commit("c2", "Alice", "art")}, s.commits...)
	})
	pushed := recvActivity(t, stream)
	require.Equal(t, EventPushed, pushed.GetEvents()[0].GetKind())
	require.EqualValues(t, 1, pushed.GetIncoming())

	cancel()
	require.NoError(t, <-done)
}

func TestServer_WatchEventsEndsWhenUnwatched(t *testing.T) {
	svc := &teamService{}
	srv, root, _ := eventServer(t, svc, "me", nil)
	stream := &activityStream{ctx: context.Background(), sent: make(chan *daemonpb.RepoActivity, 8)}
	done := make(chan error, 1)
	go func() { done <- srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: root}, stream) }()
	recvActivity(t, stream)
	require.NoError(t, srv.repos.unwatch(root))
	require.NoError(t, <-done)
}

func TestServer_WatchEventsStopsWithServer(t *testing.T) {
	srv, root, _ := eventServer(t, &teamService{}, "me", nil)
	stream := &activityStream{ctx: context.Background(), sent: make(chan *daemonpb.RepoActivity, 8)}
	done := make(chan error, 1)
	go func() { done <- srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: root}, stream) }()
	recvActivity(t, stream)
	srv.Stop()
	require.NoError(t, <-done)
}

func TestServer_WatchEventsReportsPollFailures(t *testing.T) {
	srv, root, _ := eventServer(t, &teamService{}, "", errors.New("not signed in"))
	stream := &activityStream{ctx: context.Background(), sent: make(chan *daemonpb.RepoActivity, 8)}
	go func() { _ = srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: root}, stream) }()
	require.Equal(t, "not signed in", recvActivity(t, stream).GetError())
}

func TestServer_WatchEventsErrors(t *testing.T) {
	srv, root, _ := eventServer(t, &teamService{}, "me", nil)
	err := srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: t.TempDir()}, &activityStream{ctx: context.Background()})
	require.Error(t, err)

	headerErr := errors.New("header")
	err = srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: root}, &activityStream{ctx: context.Background(), headerErr: headerErr})
	require.ErrorIs(t, err, headerErr)

	sendErr := errors.New("send")
	err = srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: root}, &activityStream{ctx: context.Background(), sendErr: sendErr})
	require.ErrorIs(t, err, sendErr)
}

func TestServer_PollEventsWithoutProxyOrRepo(t *testing.T) {
	srv := newOpServer(t, RepoOps{})
	root := lockClone(t, "main")
	_, err := srv.repos.watch(root)
	require.NoError(t, err)
	act, err := srv.pollEvents(context.Background(), root, &teamWatch{})
	require.NoError(t, err)
	require.Equal(t, "proxy is not configured", act.GetError())

	_, err = srv.pollEvents(context.Background(), t.TempDir(), &teamWatch{})
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	act, err = srv.pollEvents(ctx, root, &teamWatch{})
	require.NoError(t, err)
	require.Nil(t, act, "a cancelled poll reports nothing")
}

func TestServer_WatchEventsRepollsOnWholeRepoChange(t *testing.T) {
	svc := &teamService{commits: []*pb.CommitLogEntry{commit("c1", "Me", "base")}}
	srv, root, _ := eventServer(t, svc, "me", nil)
	srv.eventEvery = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &activityStream{ctx: ctx, sent: make(chan *daemonpb.RepoActivity, 8)}
	done := make(chan error, 1)
	go func() { done <- srv.WatchEvents(&daemonpb.WatchEventsRequest{Root: root, IntervalSeconds: 1}, stream) }()
	recvActivity(t, stream)

	rp, unref, err := srv.repos.ref(root)
	require.NoError(t, err)
	defer unref()

	svc.set(func(s *teamService) {
		s.commits = append([]*pb.CommitLogEntry{commit("c2", "Alice", "art")}, s.commits...)
	})
	rp.changes.publish([]string{"a.txt"})
	select {
	case <-stream.sent:
		t.Fatal("path changes must not trigger a poll")
	case <-time.After(300 * time.Millisecond):
	}

	rp.changes.publish(nil)
	rp.changes.publish(nil)
	pushed := recvActivity(t, stream)
	require.Equal(t, EventPushed, pushed.GetEvents()[0].GetKind())

	cancel()
	require.NoError(t, <-done)
}

func TestServer_WithMutationPublishesChange(t *testing.T) {
	svc := &teamService{}
	srv, root, _ := eventServer(t, svc, "me", nil)
	rp, unref, err := srv.repos.ref(root)
	require.NoError(t, err)
	defer unref()
	sub, unsubscribe := rp.changes.subscribe()
	defer unsubscribe()

	require.NoError(t, srv.withMutation(context.Background(), root, func(pb.NipaServiceClient, *pb.ProjectContext) error { return nil }))
	require.NotNil(t, sub.take())

	failure := errors.New("boom")
	require.ErrorIs(t, srv.withMutation(context.Background(), root, func(pb.NipaServiceClient, *pb.ProjectContext) error { return failure }), failure)
	require.Nil(t, sub.take())
}
