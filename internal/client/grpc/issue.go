package grpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (c *Client) ResolveIssues(ctx context.Context, org, project string, keys []string) ([]clientDomain.Issue, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ResolveIssues(ctx, &pb.ResolveIssuesRequest{Context: &pb.ProjectContext{Org: org, Project: project}, Keys: keys})
	if err != nil {
		return nil, issueError(err)
	}
	return toIssues(res.GetIssues()), nil
}

func (c *Client) SearchIssues(ctx context.Context, org, project, query string, limit int) (*clientDomain.IssueSearch, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.SearchIssues(ctx, &pb.SearchIssuesRequest{Context: &pb.ProjectContext{Org: org, Project: project}, Query: query, Limit: int32(limit)})
	if err != nil {
		return nil, issueError(err)
	}
	return &clientDomain.IssueSearch{Issues: toIssues(res.GetIssues()), Supported: res.GetSupported(), Unavailable: res.GetUnavailable()}, nil
}

func toIssues(issues []*pb.IssueDetail) []clientDomain.Issue {
	out := make([]clientDomain.Issue, 0, len(issues))
	for _, issue := range issues {
		out = append(out, clientDomain.Issue{
			Key: issue.GetKey(), URL: issue.GetUrl(), State: issue.GetState(), Title: issue.GetTitle(), Type: issue.GetType(),
			Status: issue.GetStatus(), StatusCategory: issue.GetStatusCategory(), Assignee: issue.GetAssignee(),
		})
	}
	return out
}

func toIssueKeys(keys []*pb.IssueKey) []serverDomain.IssueKeyRef {
	if len(keys) == 0 {
		return nil
	}
	out := make([]serverDomain.IssueKeyRef, 0, len(keys))
	for _, key := range keys {
		out = append(out, serverDomain.IssueKeyRef{Key: key.GetKey(), URL: key.GetUrl()})
	}
	return out
}

// issueError maps a server that predates issue trackers to not found, the
// same answer as a project without one.
func issueError(err error) error {
	if status.Code(err) == codes.Unimplemented {
		return clientDomain.NewNotFoundError("the server does not support issue trackers")
	}
	return toDomainError(err)
}
