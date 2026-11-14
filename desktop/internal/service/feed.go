package service

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

const (
	feedBranches     = 10
	feedCommits      = 30
	feedRequestPages = 4
	feedMemberNames  = 200
	feedDefaultDays  = 7
)

const (
	FeedCommit   = "commit"
	FeedOpened   = "mr_opened"
	FeedMerged   = "mr_merged"
	FeedLock     = "lock"
	feedNoOneYet = "Someone"
)

// FeedEvent is one line of the project timeline.
type FeedEvent struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // commit, mr_opened, mr_merged, lock
	Time     string `json:"time"` // RFC 3339
	Actor    string `json:"actor"`
	Title    string `json:"title"`
	Branch   string `json:"branch"`
	CommitID string `json:"commitId"` // commit events
	Number   int64  `json:"number"`   // merge request events
	Path     string `json:"path"`     // lock events
}

// Feed is the merged timeline, newest first. Truncated means a source had
// more events inside the window than the feed reads; Partial means a source
// could not be read.
type Feed struct {
	Events    []FeedEvent `json:"events"`
	Truncated bool        `json:"truncated"`
	Partial   bool        `json:"partial"`
}

// ProjectActivity composes what happened since the given RFC 3339 time (the
// last week when empty) from the recently updated branches' commits, open
// and merged merge requests and the current locks.
func (s *Service) ProjectActivity(ctx context.Context, root, since string) (*Feed, error) {
	from := time.Now().AddDate(0, 0, -feedDefaultDays)
	if t, err := time.Parse(time.RFC3339, since); err == nil {
		from = t
	}
	feed := &Feed{Events: []FeedEvent{}}
	names := s.memberNames(ctx, root)

	branches, err := s.recentBranches(ctx, root, from, feed)
	if err != nil {
		return nil, err
	}
	var commits []FeedEvent
	for _, b := range branches {
		page, err := s.History(ctx, root, HistoryQuery{Branch: b.Name, Limit: feedCommits})
		if err != nil {
			feed.Partial = true
			continue
		}
		inside := 0
		for _, c := range page.Commits {
			if !within(c.Date, from) {
				continue
			}
			inside++
			commits = append(commits, FeedEvent{ID: "commit:" + c.ID, Kind: FeedCommit, Time: c.Date, Actor: c.Author, Title: firstLine(c.Message), Branch: b.Name, CommitID: c.ID})
		}
		if inside == feedCommits {
			feed.Truncated = true
		}
	}

	var requests []FeedEvent
	for _, status := range []string{"open", "merged"} {
		events, err := s.requestEvents(ctx, root, status, from, names, feed)
		if err != nil {
			feed.Partial = true
		}
		requests = append(requests, events...)
	}

	var locks []FeedEvent
	if status, err := s.LockStatus(ctx, root); err != nil {
		feed.Partial = true
	} else {
		for _, l := range status.Locks {
			if !within(l.AcquiredAt, from) {
				continue
			}
			locks = append(locks, FeedEvent{ID: "lock:" + l.ID, Kind: FeedLock, Time: l.AcquiredAt, Actor: orSomeone(l.HeldByName), Title: l.Path, Branch: l.Branch, Path: l.Path})
		}
	}

	feed.Events = mergeFeed(commits, requests, locks)
	return feed, nil
}

// requestEvents pages through the requests of one status, which the server
// orders by number. An open request was created in number order, so its list
// stops at the first page that reaches back before the window; a merged one
// may have been merged at any time, so that list is read up to feedRequestPages
// pages and the feed is marked truncated when more remain.
func (s *Service) requestEvents(ctx context.Context, root, status string, from time.Time, names map[string]string, feed *Feed) ([]FeedEvent, error) {
	var out []FeedEvent
	after := int64(0)
	for pages := 0; ; pages++ {
		if pages == feedRequestPages {
			feed.Truncated = true
			return out, nil
		}
		page, err := s.MergeRequests(ctx, root, MergeRequestFilter{Status: status, After: after})
		if err != nil {
			return out, err
		}
		reachedBack := false
		for _, mr := range page.Items {
			at, kind := mr.CreatedAt, FeedOpened
			if status == "merged" {
				at, kind = mr.UpdatedAt, FeedMerged
			}
			if !within(at, from) {
				reachedBack = reachedBack || status == "open"
				continue
			}
			out = append(out, FeedEvent{ID: "mr:" + strconv.FormatInt(mr.Number, 10) + ":" + kind, Kind: kind, Time: at, Actor: nameOf(names, mr.AuthorID), Title: mr.Title, Branch: mr.Source, Number: mr.Number})
		}
		if page.Next == 0 || reachedBack {
			return out, nil
		}
		after = page.Next
	}
}

// recentBranches returns the branches updated inside the window, newest
// update first except that the default branch leads, so commits shared with
// it are attributed to it.
func (s *Service) recentBranches(ctx context.Context, root string, from time.Time, feed *Feed) ([]Branch, error) {
	var res *daemonpb.ProxyBranchListResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyBranchList(ctx, &daemonpb.ProxyBranchListRequest{Root: root, Request: &pb.GetListBranchRequest{Limit: feedBranches + 1}})
		return err
	})
	if err != nil {
		return nil, err
	}
	var out []Branch
	for i, b := range res.GetResponse().GetBranches() {
		if b.GetUpdatedAt() != nil && b.GetUpdatedAt().AsTime().Before(from) {
			continue
		}
		if i == feedBranches {
			feed.Truncated = true
			break
		}
		out = append(out, toBranch(b))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Default && !out[j].Default })
	return out, nil
}

func (s *Service) memberNames(ctx context.Context, root string) map[string]string {
	var res *daemonpb.ProxyProjectMembersResponse
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) (err error) {
		res, err = c.ProxyProjectMembers(ctx, &daemonpb.ProxyProjectMembersRequest{Root: root, Request: &pb.ListProjectMembersRequest{Limit: feedMemberNames}})
		return err
	})
	names := map[string]string{}
	if err != nil {
		return names
	}
	for _, m := range res.GetResponse().GetMembers() {
		names[m.GetId()] = m.GetName()
	}
	return names
}

// mergeFeed concatenates the sources, drops repeated ids (first wins) and
// orders newest first, ties by id so the order is stable.
func mergeFeed(sources ...[]FeedEvent) []FeedEvent {
	seen := map[string]bool{}
	out := []FeedEvent{}
	for _, list := range sources {
		for _, e := range list {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := parseTime(out[i].Time), parseTime(out[j].Time)
		if !a.Equal(b) {
			return a.After(b)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func within(stamp string, from time.Time) bool {
	t := parseTime(stamp)
	return !t.IsZero() && !t.Before(from)
}

func parseTime(stamp string) time.Time {
	t, _ := time.Parse(time.RFC3339, stamp)
	return t
}

func nameOf(names map[string]string, id string) string {
	return orSomeone(names[id])
}

func orSomeone(name string) string {
	if name == "" {
		return feedNoOneYet
	}
	return name
}

func firstLine(message string) string {
	for i, r := range message {
		if r == '\n' {
			return message[:i]
		}
	}
	return message
}
