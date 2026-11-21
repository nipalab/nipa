package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/issuetracker"
)

type issueClient interface {
	ResolveIssues(ctx context.Context, org, project string, keys []string) ([]domain.Issue, error)
	SearchIssues(ctx context.Context, org, project, query string, limit int) (*domain.IssueSearch, error)
}

// Issue reads the issues of the project's tracker through the server.
type Issue struct {
	auth   *Auth
	client issueClient
}

func NewIssue(auth *Auth, client issueClient) *Issue {
	return &Issue{auth: auth, client: client}
}

func (u *Issue) Resolve(ctx context.Context, host, org, project string, keys []string) ([]domain.Issue, error) {
	if err := u.auth.MakeSureLoggedIn(ctx, host); err != nil {
		return nil, err
	}
	return u.client.ResolveIssues(ctx, org, project, keys)
}

func (u *Issue) Search(ctx context.Context, host, org, project, query string, limit int) (*domain.IssueSearch, error) {
	if err := u.auth.MakeSureLoggedIn(ctx, host); err != nil {
		return nil, err
	}
	return u.client.SearchIssues(ctx, org, project, query, limit)
}

// IssueBranch is the branch to create for an issue. Issue is nil when the
// project has no issue tracker; its State is unavailable when the tracker did
// not answer.
type IssueBranch struct {
	Key   string
	Name  string
	Issue *domain.Issue
}

// BranchFor checks an issue key with the project's tracker and names the
// branch after it (see issuetracker.BranchName). An unknown issue or a key
// outside the project's keys is refused; a project without a tracker or a
// tracker that is down only skips the check.
func (u *Issue) BranchFor(ctx context.Context, host, org, project, rawKey, name string) (*IssueBranch, error) {
	key, ok := issuetracker.ParseAnyKey(rawKey)
	if !ok {
		return nil, domain.NewUserError(fmt.Sprintf("%q is not an issue key (expected PROJECT-123)", rawKey))
	}
	issues, err := u.Resolve(ctx, host, org, project, []string{key})
	if notConfigured(err) {
		return &IssueBranch{Key: key, Name: issuetracker.BranchName(key, name, "")}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return nil, domain.NewUserError(fmt.Sprintf("%s is not an issue of this project's issue tracker keys", key))
	}
	issue := issues[0]
	switch issue.State {
	case domain.IssueStateNotFound:
		return nil, domain.NewUserError(fmt.Sprintf("issue %s was not found in the issue tracker", key))
	case domain.IssueStateOK:
		return &IssueBranch{Key: key, Name: issuetracker.BranchName(key, name, issue.Title), Issue: &issue}, nil
	default:
		return &IssueBranch{Key: key, Name: issuetracker.BranchName(key, name, ""), Issue: &issue}, nil
	}
}

// notConfigured reports the answers of a project without an issue tracker
// (404) and of a server without the integration (409 / 503).
func notConfigured(err error) bool {
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		return false
	}
	return domErr.Code == 404 || domErr.Code == 409 || domErr.Code == 503
}
