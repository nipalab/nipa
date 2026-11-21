package service

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/issuetracker"
)

const issueSearchLimit = 20

// IssueKey is an issue linked to a commit or merge request, with its page on
// the tracker.
type IssueKey struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// Issue is an issue of the project's tracker; State is ok, not_found or
// unavailable (the tracker did not answer: key and URL only).
type Issue struct {
	Key            string `json:"key"`
	URL            string `json:"url"`
	State          string `json:"state"`
	Title          string `json:"title"`
	Type           string `json:"type"`
	Status         string `json:"status"`
	StatusCategory string `json:"statusCategory"`
	Assignee       string `json:"assignee"`
}

// IssueSearch answers an issue picker. Configured is false when the project
// has no issue tracker, Supported false when the tracker cannot search.
type IssueSearch struct {
	Issues      []Issue `json:"issues"`
	Configured  bool    `json:"configured"`
	Supported   bool    `json:"supported"`
	Unavailable bool    `json:"unavailable"`
}

// IssueBranch is the branch to create for an issue; Issue is nil when the
// project has no issue tracker.
type IssueBranch struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Issue *Issue `json:"issue"`
}

// SearchIssues searches the project's issue tracker for the issue picker.
func (s *Service) SearchIssues(ctx context.Context, root, query string) (*IssueSearch, error) {
	out := &IssueSearch{Issues: []Issue{}, Configured: true}
	query = strings.TrimSpace(query)
	if query == "" {
		return out, nil
	}
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		res, err := c.ProxySearchIssues(ctx, &daemonpb.ProxySearchIssuesRequest{Root: root, Request: &pb.SearchIssuesRequest{Query: query, Limit: issueSearchLimit}})
		if noIssueTracker(err) {
			out.Configured = false
			return nil
		}
		if err != nil {
			return err
		}
		out.Supported, out.Unavailable = res.GetResponse().GetSupported(), res.GetResponse().GetUnavailable()
		out.Issues = toIssues(res.GetResponse().GetIssues())
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveIssues looks up the title and status of linked issues for the
// detail headers; a project without an issue tracker answers none.
func (s *Service) ResolveIssues(ctx context.Context, root string, keys []string) ([]Issue, error) {
	out := []Issue{}
	if len(keys) == 0 {
		return out, nil
	}
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		res, err := c.ProxyResolveIssues(ctx, &daemonpb.ProxyResolveIssuesRequest{Root: root, Request: &pb.ResolveIssuesRequest{Keys: keys}})
		if noIssueTracker(err) {
			return nil
		}
		if err != nil {
			return err
		}
		out = toIssues(res.GetResponse().GetIssues())
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// IssueBranch checks an issue key with the project's tracker and names the
// branch for it like `nipa branch --issue`: an unknown issue or a key outside
// the project's keys is refused, a project without a tracker or a tracker
// that is down only skips the check.
func (s *Service) IssueBranch(ctx context.Context, root, rawKey, name string) (*IssueBranch, error) {
	key, ok := issuetracker.ParseAnyKey(rawKey)
	if !ok {
		return nil, fmt.Errorf("%q is not an issue key (expected PROJECT-123)", strings.TrimSpace(rawKey))
	}
	var issues []Issue
	configured := true
	err := s.call(ctx, root, func(c daemonpb.NipaDaemonClient) error {
		res, err := c.ProxyResolveIssues(ctx, &daemonpb.ProxyResolveIssuesRequest{Root: root, Request: &pb.ResolveIssuesRequest{Keys: []string{key}}})
		if noIssueTracker(err) {
			configured = false
			return nil
		}
		if err != nil {
			return err
		}
		issues = toIssues(res.GetResponse().GetIssues())
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !configured {
		return &IssueBranch{Key: key, Name: issuetracker.BranchName(key, name, "")}, nil
	}
	if len(issues) == 0 {
		return nil, fmt.Errorf("%s is not an issue of this project's issue tracker keys", key)
	}
	issue := issues[0]
	switch issue.State {
	case "not_found":
		return nil, fmt.Errorf("issue %s was not found in the issue tracker", key)
	case "ok":
		return &IssueBranch{Key: key, Name: issuetracker.BranchName(key, name, issue.Title), Issue: &issue}, nil
	default:
		return &IssueBranch{Key: key, Name: issuetracker.BranchName(key, name, ""), Issue: &issue}, nil
	}
}

// noIssueTracker tells the answers of a project without an issue tracker,
// a server without the integration and a server that predates it.
func noIssueTracker(err error) bool {
	if err == nil {
		return false
	}
	switch status.Code(err) {
	case codes.NotFound, codes.FailedPrecondition, codes.Unimplemented:
		return true
	}
	return false
}

func toIssues(list []*pb.IssueDetail) []Issue {
	out := make([]Issue, 0, len(list))
	for _, i := range list {
		out = append(out, Issue{
			Key: i.GetKey(), URL: i.GetUrl(), State: i.GetState(), Title: i.GetTitle(), Type: i.GetType(),
			Status: i.GetStatus(), StatusCategory: i.GetStatusCategory(), Assignee: i.GetAssignee(),
		})
	}
	return out
}

func toIssueKeys(keys []*pb.IssueKey) []IssueKey {
	if len(keys) == 0 {
		return nil
	}
	out := make([]IssueKey, 0, len(keys))
	for _, k := range keys {
		out = append(out, IssueKey{Key: k.GetKey(), URL: k.GetUrl()})
	}
	return out
}
