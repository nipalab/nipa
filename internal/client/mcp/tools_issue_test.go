package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

type fakeIssueClient struct {
	issues []domain.Issue
	search *domain.IssueSearch
	err    error
	keys   []string
	query  string
	limit  int
}

func (f *fakeIssueClient) ResolveIssues(_ context.Context, _, _ string, keys []string) ([]domain.Issue, error) {
	f.keys = keys
	return f.issues, f.err
}

func (f *fakeIssueClient) SearchIssues(_ context.Context, _, _, query string, limit int) (*domain.IssueSearch, error) {
	f.query, f.limit = query, limit
	return f.search, f.err
}

func issueUseCases(client *fakeIssueClient) func() UseCases {
	return func() UseCases {
		return UseCases{
			Repo:      usecase.NewRepo(loggedInAuth(), nil, localrepo.NewLocalRepo()),
			Issue:     usecase.NewIssue(loggedInAuth(), client),
			Connector: fakeConnector{},
		}
	}
}

func TestIssuesTool(t *testing.T) {
	root := setupRepo(t, nil)
	client := &fakeIssueClient{
		issues: []domain.Issue{{Key: "GAME-1", State: domain.IssueStateOK, Title: "New rig"}},
		search: &domain.IssueSearch{Supported: true, Issues: []domain.Issue{{Key: "GAME-2", State: domain.IssueStateOK}}},
	}
	session := connect(t, Options{NewUseCases: issueUseCases(client)})

	resolved := decode[issuesOutput](t, call(t, session, "nipa_issues", map[string]any{"repo": root, "keys": []string{"GAME-1"}}))
	require.Equal(t, "New rig", resolved.Issues[0].Title)
	require.Nil(t, resolved.Supported)
	require.Equal(t, []string{"GAME-1"}, client.keys)

	found := decode[issuesOutput](t, call(t, session, "nipa_issues", map[string]any{"repo": root, "query": " rig ", "limit": 500}))
	require.Equal(t, "GAME-2", found.Issues[0].Key)
	require.True(t, *found.Supported)
	require.Equal(t, "rig", client.query)
	require.Equal(t, maxIssueResults, client.limit)

	client.issues = nil
	empty := decode[issuesOutput](t, call(t, session, "nipa_issues", map[string]any{"repo": root, "keys": []string{"OTHER-1"}}))
	require.NotNil(t, empty.Issues)
	require.Empty(t, empty.Issues)

	client.search = &domain.IssueSearch{}
	unsupported := decode[issuesOutput](t, call(t, session, "nipa_issues", map[string]any{"repo": root, "query": "rig"}))
	require.False(t, *unsupported.Supported)
	require.NotNil(t, unsupported.Issues)

	client.err = errors.New("tracker exploded")
	for _, args := range []map[string]any{{"keys": []string{"GAME-1"}}, {"query": "rig"}} {
		args["repo"] = root
		res := call(t, session, "nipa_issues", args)
		require.True(t, res.IsError)
		require.Contains(t, toolText(t, res), "tracker exploded")
	}
}

func TestIssuesTool_Refusals(t *testing.T) {
	root := setupRepo(t, nil)
	session := connect(t, Options{NewUseCases: issueUseCases(&fakeIssueClient{})})
	tooMany := make([]string, maxIssueKeys+1)
	for i := range tooMany {
		tooMany[i] = "GAME-1"
	}
	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "pass keys or a query"},
		{map[string]any{"keys": []string{"GAME-1"}, "query": "rig"}, "not both"},
		{map[string]any{"keys": tooMany}, "at most 50"},
	}
	for _, tc := range cases {
		tc.args["repo"] = root
		res := call(t, session, "nipa_issues", tc.args)
		require.True(t, res.IsError)
		require.Contains(t, toolText(t, res), tc.want)
	}

	noIssues := connect(t, Options{NewUseCases: func() UseCases {
		return UseCases{Repo: usecase.NewRepo(loggedInAuth(), nil, localrepo.NewLocalRepo()), Connector: fakeConnector{}}
	}})
	res := call(t, noIssues, "nipa_issues", map[string]any{"repo": root, "keys": []string{"GAME-1"}})
	require.True(t, res.IsError)
	require.Contains(t, toolText(t, res), "issues are not configured")

	res = call(t, connect(t, Options{NewUseCases: func() UseCases { return UseCases{} }}), "nipa_issues", map[string]any{"repo": root, "query": "x"})
	require.True(t, res.IsError)
}
