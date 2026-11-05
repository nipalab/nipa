package daemon

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

const (
	defaultEventInterval = time.Minute
	minEventInterval     = 15 * time.Second
	maxEventInterval     = time.Hour
	incomingLimit        = 100
	maxTeamEvents        = 20
	maxReviewScan        = 50
)

const (
	EventPushed          = "pushed"
	EventLockReleased    = "lock_released"
	EventReviewRequested = "review_requested"
)

func eventInterval(seconds int32, floor time.Duration) time.Duration {
	if seconds <= 0 {
		return max(defaultEventInterval, floor)
	}
	d := time.Duration(seconds) * time.Second
	return min(max(d, floor), maxEventInterval)
}

// WatchEvents polls the server for what teammates did that concerns the
// clone: new commits on its branch, released locks and merge requests waiting
// on the caller's review. The first poll only records a baseline; later polls
// report the differences. The stream ends when the repo is unwatched or the
// daemon stops.
func (s *Server) WatchEvents(req *daemonpb.WatchEventsRequest, stream daemonpb.NipaDaemon_WatchEventsServer) error {
	rp, unref, err := s.repos.ref(req.GetRoot())
	if err != nil {
		return toStatusError(err)
	}
	sub, unsubscribe := rp.changes.subscribe()
	unref()
	defer unsubscribe()
	if err := stream.SendHeader(metadata.MD{}); err != nil {
		return err
	}
	interval := eventInterval(req.GetIntervalSeconds(), minEventInterval)
	if s.eventEvery > 0 {
		interval = s.eventEvery
	}
	ctx := stream.Context()
	w := &teamWatch{}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.stop:
			return nil
		case <-sub.done:
			return nil
		case <-sub.signal:
			if c := sub.take(); c == nil || len(c.GetPaths()) > 0 {
				continue
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Second)
			continue
		case <-timer.C:
		}
		act, err := s.pollEvents(ctx, req.GetRoot(), w)
		if err != nil {
			return nil
		}
		if act != nil {
			if err := stream.Send(act); err != nil {
				return err
			}
		}
		timer.Reset(interval)
	}
}

// pollEvents runs one poll; an error means the repo is gone.
func (s *Server) pollEvents(ctx context.Context, root string, w *teamWatch) (*daemonpb.RepoActivity, error) {
	rp, unref, err := s.repos.ref(root)
	if err != nil {
		return nil, err
	}
	cfg := rp.config()
	in := pollInput{branch: cfg.Branch, detached: cfg.Head != nil, pinned: rp.headCommitID()}
	ident, _ := rp.proxy.(Identity)
	unref()
	var act *daemonpb.RepoActivity
	err = s.withProxy(ctx, root, func(client pb.NipaServiceClient, project *pb.ProjectContext) error {
		if ident != nil {
			user, err := ident.UserID(ctx)
			if err != nil {
				return err
			}
			in.user = user
		}
		var err error
		act, err = w.poll(ctx, client, project, in)
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil
		}
		return w.failed(err), nil
	}
	return act, nil
}

type pollInput struct {
	branch   string
	detached bool
	pinned   string
	user     string
}

// teamWatch remembers what the previous poll saw.
type teamWatch struct {
	primed   bool
	head     string
	locks    map[string]*pb.FileLockDetail
	reviews  map[int64]bool
	incoming int32
	requests int32
	err      string
}

func (w *teamWatch) failed(err error) *daemonpb.RepoActivity {
	msg := err.Error()
	if st, ok := status.FromError(err); ok {
		msg = st.Message()
	}
	if msg == w.err {
		return nil
	}
	w.err = msg
	return &daemonpb.RepoActivity{Incoming: w.incoming, ReviewRequests: w.requests, Error: msg}
}

func (w *teamWatch) poll(ctx context.Context, client pb.NipaServiceClient, project *pb.ProjectContext, in pollInput) (*daemonpb.RepoActivity, error) {
	act := &daemonpb.RepoActivity{}
	head, err := w.pollBranch(ctx, client, project, in, act)
	if err != nil {
		return nil, err
	}
	locks, err := w.pollLocks(ctx, client, project, in, act)
	if err != nil {
		return nil, err
	}
	reviews, err := w.pollReviews(ctx, client, project, in, act)
	if err != nil {
		return nil, err
	}
	if len(act.Events) > maxTeamEvents {
		act.Events = act.Events[:maxTeamEvents]
	}
	changed := !w.primed || len(act.Events) > 0 || w.err != "" || act.Incoming != w.incoming || act.ReviewRequests != w.requests
	w.primed, w.head, w.locks, w.reviews, w.err = true, head, locks, reviews, ""
	w.incoming, w.requests = act.Incoming, act.ReviewRequests
	if !changed {
		return nil, nil
	}
	return act, nil
}

func (w *teamWatch) pollBranch(ctx context.Context, client pb.NipaServiceClient, project *pb.ProjectContext, in pollInput, act *daemonpb.RepoActivity) (string, error) {
	if in.branch == "" || in.detached {
		return "", nil
	}
	log, err := client.GetCommitLog(ctx, &pb.GetCommitLogRequest{Context: project, Branch: in.branch, Limit: incomingLimit})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", nil
		}
		return "", err
	}
	commits := log.GetCommits()
	if len(commits) == 0 {
		return "", nil
	}
	act.Incoming = int32(countUntil(commits, in.pinned))
	head := commits[0].GetCommitId()
	if w.primed && head != w.head && head != in.pinned {
		if fresh := commits[:countUntil(commits, w.head, in.pinned)]; len(fresh) > 0 {
			act.Events = append(act.Events, &daemonpb.TeamEvent{
				Kind:     EventPushed,
				Actor:    fresh[0].GetAuthorName(),
				Branch:   in.branch,
				CommitId: head,
				Message:  firstLine(fresh[0].GetMessage()),
				Count:    int32(len(fresh)),
			})
		}
	}
	return head, nil
}

func (w *teamWatch) pollLocks(ctx context.Context, client pb.NipaServiceClient, project *pb.ProjectContext, in pollInput, act *daemonpb.RepoActivity) (map[string]*pb.FileLockDetail, error) {
	res, err := client.ListFileLocks(ctx, &pb.ListFileLocksRequest{Context: project})
	if err != nil {
		return nil, err
	}
	current := map[string]*pb.FileLockDetail{}
	for _, l := range res.GetLocks() {
		if l.GetHeldBy() != in.user {
			current[l.GetId()] = l
		}
	}
	if !w.primed {
		return current, nil
	}
	var released []*pb.FileLockDetail
	for id, l := range w.locks {
		if _, ok := current[id]; !ok {
			released = append(released, l)
		}
	}
	sort.Slice(released, func(i, j int) bool { return released[i].GetPath() < released[j].GetPath() })
	for _, l := range released {
		act.Events = append(act.Events, &daemonpb.TeamEvent{Kind: EventLockReleased, Actor: l.GetHeldByName(), Path: l.GetPath(), Branch: l.GetBranch()})
	}
	return current, nil
}

// pollReviews counts the open merge requests with a pending review request
// for the caller. The list's review summary carries no requests, so each open
// request (up to maxReviewScan) is asked for its own.
func (w *teamWatch) pollReviews(ctx context.Context, client pb.NipaServiceClient, project *pb.ProjectContext, in pollInput, act *daemonpb.RepoActivity) (map[int64]bool, error) {
	reviews := map[int64]bool{}
	if in.user == "" {
		return reviews, nil
	}
	res, err := client.ListMergeRequests(ctx, &pb.ListMergeRequestsRequest{Context: project, Status: "open", Limit: maxReviewScan})
	if err != nil {
		return nil, err
	}
	for _, mr := range res.GetMergeRequests() {
		requests, err := client.ListMergeRequestReviewRequests(ctx, &pb.ListMergeRequestReviewRequestsRequest{Context: project, Number: mr.GetNumber()})
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(requests.GetReviewRequests(), func(r *pb.ReviewRequestDetail) bool { return r.GetReviewer().GetUserId() == in.user }) {
			continue
		}
		reviews[mr.GetNumber()] = true
		act.ReviewRequests++
		if w.primed && !w.reviews[mr.GetNumber()] {
			act.Events = append(act.Events, &daemonpb.TeamEvent{Kind: EventReviewRequested, Number: mr.GetNumber(), Message: mr.GetTitle(), Branch: mr.GetSourceBranch()})
		}
	}
	return reviews, nil
}

// countUntil returns how many commits come before the first one whose id is
// one of stops (all of them when none matches).
func countUntil(commits []*pb.CommitLogEntry, stops ...string) int {
	for i, c := range commits {
		if c.GetCommitId() != "" && slices.Contains(stops, c.GetCommitId()) {
			return i
		}
	}
	return len(commits)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
