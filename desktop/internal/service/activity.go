package service

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/daemon"
	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
)

const EventRepoActivity = "repo:activity"

// TeamEvent is something a teammate did that concerns a clone.
type TeamEvent struct {
	Kind     string `json:"kind"`
	Actor    string `json:"actor"`
	Branch   string `json:"branch"`
	CommitID string `json:"commitId"`
	Message  string `json:"message"`
	Count    int    `json:"count"`
	Path     string `json:"path"`
	Number   int64  `json:"number"`
}

// RepoActivity is the latest server-side state of one clone. Events only
// carries what is new in this update; the copy kept for Activity has none.
type RepoActivity struct {
	Root           string      `json:"root"`
	Name           string      `json:"name"`
	Incoming       int         `json:"incoming"`
	ReviewRequests int         `json:"reviewRequests"`
	Error          string      `json:"error"`
	Events         []TeamEvent `json:"events"`
}

// Notification is a desktop notification; clicking it navigates to Target.
type Notification struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Body   string     `json:"body"`
	Target Navigation `json:"target"`
}

// Activity returns the latest activity of every followed clone.
func (s *Service) Activity() []RepoActivity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activityListLocked()
}

func (s *Service) activityListLocked() []RepoActivity {
	out := make([]RepoActivity, 0, len(s.activity))
	for _, a := range s.activity {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out
}

// startActivityLocked follows the daemon's team events for root. A daemon
// without WatchEvents ends the stream at once and nothing is reported.
func (s *Service) startActivityLocked(sess Session, root string) {
	if cancel, ok := s.followers[root]; ok {
		cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.followers[root] = cancel
	interval := int32(s.prefs().PollSeconds)
	go s.followEvents(ctx, sess, root, interval)
}

func (s *Service) followEvents(ctx context.Context, sess Session, root string, interval int32) {
	for ctx.Err() == nil {
		stream, err := sess.Client().WatchEvents(ctx, &daemonpb.WatchEventsRequest{Root: root, IntervalSeconds: interval})
		if err == nil {
			for {
				act, err := stream.Recv()
				if err != nil {
					break
				}
				s.recordActivity(ctx, root, act)
			}
		}
		if status.Code(err) == codes.Unimplemented {
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.followRetry()):
		}
	}
}

func (s *Service) followRetry() time.Duration {
	if s.FollowRetry > 0 {
		return s.FollowRetry
	}
	return 30 * time.Second
}

func (s *Service) stopActivityLocked(root string) {
	if cancel, ok := s.followers[root]; ok {
		cancel()
		delete(s.followers, root)
	}
	delete(s.activity, root)
}

// restartActivity re-follows every clone, so a new poll interval applies.
func (s *Service) restartActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return
	}
	for root := range s.followers {
		s.startActivityLocked(s.active, root)
	}
}

func (s *Service) recordActivity(ctx context.Context, root string, pb *daemonpb.RepoActivity) {
	act := RepoActivity{
		Root:           root,
		Name:           filepath.Base(root),
		Incoming:       int(pb.GetIncoming()),
		ReviewRequests: int(pb.GetReviewRequests()),
		Error:          pb.GetError(),
		Events:         []TeamEvent{},
	}
	for _, e := range pb.GetEvents() {
		act.Events = append(act.Events, TeamEvent{
			Kind:     e.GetKind(),
			Actor:    e.GetActor(),
			Branch:   e.GetBranch(),
			CommitID: e.GetCommitId(),
			Message:  e.GetMessage(),
			Count:    int(e.GetCount()),
			Path:     e.GetPath(),
			Number:   e.GetNumber(),
		})
	}
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if _, followed := s.followers[root]; !followed {
		s.mu.Unlock()
		return
	}
	kept := act
	kept.Events = []TeamEvent{}
	s.activity[root] = kept
	list := s.activityListLocked()
	s.mu.Unlock()

	s.emit(EventRepoActivity, act)
	if s.ActivityHook != nil {
		s.ActivityHook(list)
	}
	if s.Notifier == nil {
		return
	}
	prefs := s.prefs()
	for _, e := range act.Events {
		if watched := s.watchedLockNotifications(act.Name, root, e); len(watched) > 0 {
			for _, n := range watched {
				_ = s.Notifier(n)
			}
			continue
		}
		if n, ok := notificationFor(act.Name, root, e, prefs.Notify.Pushes, prefs.Notify.Locks, prefs.Notify.Reviews); ok {
			_ = s.Notifier(n)
		}
	}
}

func notificationFor(name, root string, e TeamEvent, pushes, locks, reviews bool) (Notification, bool) {
	switch e.Kind {
	case daemon.EventPushed:
		if !pushes {
			return Notification{}, false
		}
		title := fmt.Sprintf("%s: new commit on %s", name, e.Branch)
		if e.Count > 1 {
			title = fmt.Sprintf("%s: %d new commits on %s", name, e.Count, e.Branch)
		}
		return Notification{
			ID:     "push:" + root + ":" + e.CommitID,
			Title:  title,
			Body:   fmt.Sprintf("%s: %s", e.Actor, e.Message),
			Target: Navigation{Root: root, Tab: "history"},
		}, true
	case daemon.EventLockReleased:
		if !locks {
			return Notification{}, false
		}
		return Notification{
			ID:     "lock:" + root + ":" + e.Path,
			Title:  name + ": lock released",
			Body:   fmt.Sprintf("%s released %s", e.Actor, e.Path),
			Target: Navigation{Root: root, Tab: "locks"},
		}, true
	case daemon.EventReviewRequested:
		if !reviews {
			return Notification{}, false
		}
		return Notification{
			ID:     fmt.Sprintf("review:%s:%d", root, e.Number),
			Title:  name + ": review requested",
			Body:   fmt.Sprintf("!%d %s", e.Number, e.Message),
			Target: Navigation{Root: root, Tab: "mrs", Number: e.Number},
		}, true
	}
	return Notification{}, false
}

// Data is the payload a notification carries so a click can navigate.
func (n Notification) Data() map[string]any {
	return map[string]any{"root": n.Target.Root, "tab": n.Target.Tab, "number": n.Target.Number, "path": n.Target.Path, "action": n.Target.Action}
}

// NavigationFromData reads a clicked notification's payload; numbers may
// come back as floats after a JSON round trip.
func NavigationFromData(data map[string]any) Navigation {
	nav := Navigation{}
	nav.Root, _ = data["root"].(string)
	nav.Tab, _ = data["tab"].(string)
	nav.Path, _ = data["path"].(string)
	nav.Action, _ = data["action"].(string)
	switch n := data["number"].(type) {
	case int64:
		nav.Number = n
	case int:
		nav.Number = int64(n)
	case float64:
		nav.Number = int64(n)
	}
	if nav.Tab == "" {
		nav.Tab = "changes"
	}
	return nav
}
