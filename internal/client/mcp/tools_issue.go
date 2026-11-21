package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/client/domain"
)

const (
	maxIssueKeys        = 50
	defaultIssueResults = 20
	maxIssueResults     = 50
)

func addIssueTools(srv *mcp.Server, s *server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nipa_issues",
		Description: "Look up issues of the project's issue tracker (Jira): pass keys (e.g. GAME-123, at most 50) to resolve their title, status, type and assignee, or query to search issues by text. Only keys of the project's configured issue keys are answered; a tracker that is down answers key and url with state unavailable.",
		Annotations: readOnly(),
	}, s.issues)
}

type issuesInput struct {
	Repo  string   `json:"repo,omitempty" jsonschema:"Path inside a nipa working copy (defaults to the server working directory)"`
	Keys  []string `json:"keys,omitempty" jsonschema:"Issue keys to resolve, e.g. GAME-123 (at most 50)"`
	Query string   `json:"query,omitempty" jsonschema:"Text to search issues for, instead of keys"`
	Limit int      `json:"limit,omitempty" jsonschema:"Maximum number of search results (default 20, at most 50)"`
}

type issuesOutput struct {
	Issues      []domain.Issue `json:"issues"`
	Supported   *bool          `json:"supported,omitempty"`
	Unavailable bool           `json:"unavailable,omitempty"`
}

func (s *server) issues(ctx context.Context, _ *mcp.CallToolRequest, in issuesInput) (*mcp.CallToolResult, issuesOutput, error) {
	var zero issuesOutput
	query := strings.TrimSpace(in.Query)
	switch {
	case len(in.Keys) == 0 && query == "":
		return nil, zero, toolError(domain.NewUserError("pass keys or a query"))
	case len(in.Keys) > 0 && query != "":
		return nil, zero, toolError(domain.NewUserError("pass either keys or a query, not both"))
	case len(in.Keys) > maxIssueKeys:
		return nil, zero, toolError(domain.NewUserError("at most 50 issue keys are allowed"))
	}
	_, _, url, uc, err := s.connectContext(ctx, in.Repo)
	if err != nil {
		return nil, zero, toolError(err)
	}
	defer uc.close()
	if uc.Issue == nil {
		return nil, zero, notConfigured("issues are")
	}
	if query == "" {
		issues, err := uc.Issue.Resolve(ctx, url.Host, url.Org, url.Project, in.Keys)
		if err != nil {
			return nil, zero, toolError(err)
		}
		return nil, issuesOutput{Issues: nonNilIssues(issues)}, nil
	}
	res, err := uc.Issue.Search(ctx, url.Host, url.Org, url.Project, query, clampLimit(in.Limit, defaultIssueResults, maxIssueResults))
	if err != nil {
		return nil, zero, toolError(err)
	}
	supported := res.Supported
	return nil, issuesOutput{Issues: nonNilIssues(res.Issues), Supported: &supported, Unavailable: res.Unavailable}, nil
}

func nonNilIssues(issues []domain.Issue) []domain.Issue {
	if issues == nil {
		return []domain.Issue{}
	}
	return issues
}
