package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

type stubIssueClient struct {
	issues []domain.Issue
	search *domain.IssueSearch
	err    error
	keys   []string
	query  string
	limit  int
}

func (s *stubIssueClient) ResolveIssues(_ context.Context, _, _ string, keys []string) ([]domain.Issue, error) {
	s.keys = keys
	return s.issues, s.err
}

func (s *stubIssueClient) SearchIssues(_ context.Context, _, _, query string, limit int) (*domain.IssueSearch, error) {
	s.query, s.limit = query, limit
	return s.search, s.err
}

func newTestIssueUsecase(t *testing.T, client issueClient) *Issue {
	t.Helper()
	storage := &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "secret")}}
	return NewIssue(NewAuth(nil, storage, nil), client)
}

func TestIssue_ResolveAndSearch(t *testing.T) {
	client := &stubIssueClient{
		issues: []domain.Issue{{Key: "GAME-1", State: domain.IssueStateOK}},
		search: &domain.IssueSearch{Supported: true},
	}
	uc := newTestIssueUsecase(t, client)
	got, err := uc.Resolve(context.Background(), "host", "org", "proj", []string{"GAME-1"})
	require.NoError(t, err)
	require.Equal(t, "GAME-1", got[0].Key)
	require.Equal(t, []string{"GAME-1"}, client.keys)

	search, err := uc.Search(context.Background(), "host", "org", "proj", "rig", 5)
	require.NoError(t, err)
	require.True(t, search.Supported)
	require.Equal(t, "rig", client.query)
	require.Equal(t, 5, client.limit)

	loggedOut := NewIssue(NewAuth(&stubLoginExecutor{}, &stubSecureStorage{loadErr: errors.New("no token")}, &stubUserInput{err: errors.New("interrupted")}), client)
	_, err = loggedOut.Resolve(context.Background(), "host", "org", "proj", nil)
	require.Error(t, err)
	_, err = loggedOut.Search(context.Background(), "host", "org", "proj", "rig", 0)
	require.Error(t, err)
}

func TestIssue_BranchFor(t *testing.T) {
	ctx := context.Background()
	client := &stubIssueClient{issues: []domain.Issue{{Key: "GAME-12", State: domain.IssueStateOK, Title: "New rig for the hero"}}}
	uc := newTestIssueUsecase(t, client)

	branch, err := uc.BranchFor(ctx, "host", "org", "proj", " game-12 ", "")
	require.NoError(t, err)
	require.Equal(t, "GAME-12", branch.Key)
	require.Equal(t, "GAME-12-new-rig-for-the-hero", branch.Name)
	require.Equal(t, "New rig for the hero", branch.Issue.Title)
	require.Equal(t, []string{"GAME-12"}, client.keys)

	branch, err = uc.BranchFor(ctx, "host", "org", "proj", "GAME-12", "rig")
	require.NoError(t, err)
	require.Equal(t, "GAME-12-rig", branch.Name)

	client.issues = []domain.Issue{{Key: "GAME-12", State: domain.IssueStateUnavailable}}
	branch, err = uc.BranchFor(ctx, "host", "org", "proj", "GAME-12", "")
	require.NoError(t, err)
	require.Equal(t, "GAME-12", branch.Name)
	require.Equal(t, domain.IssueStateUnavailable, branch.Issue.State)

	for _, code := range []int{404, 409, 503} {
		client.err = &domain.Error{Code: code, Message: "off"}
		branch, err = uc.BranchFor(ctx, "host", "org", "proj", "GAME-12", "GAME-12-rig")
		require.NoError(t, err)
		require.Equal(t, "GAME-12-rig", branch.Name)
		require.Nil(t, branch.Issue)
	}

	client.err = &domain.Error{Code: 403, Message: "no permission"}
	_, err = uc.BranchFor(ctx, "host", "org", "proj", "GAME-12", "")
	require.ErrorContains(t, err, "no permission")
	client.err = errors.New("network down")
	_, err = uc.BranchFor(ctx, "host", "org", "proj", "GAME-12", "")
	require.ErrorContains(t, err, "network down")

	client.err = nil
	client.issues = nil
	_, err = uc.BranchFor(ctx, "host", "org", "proj", "OTHER-1", "")
	require.ErrorContains(t, err, "not an issue of this project")

	client.issues = []domain.Issue{{Key: "GAME-99", State: domain.IssueStateNotFound}}
	_, err = uc.BranchFor(ctx, "host", "org", "proj", "GAME-99", "")
	require.ErrorContains(t, err, "was not found")

	_, err = uc.BranchFor(ctx, "host", "org", "proj", "rig", "")
	require.ErrorContains(t, err, "is not an issue key")
}
