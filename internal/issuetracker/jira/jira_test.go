package jira

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/egress"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
)

func newFake(t *testing.T, opts jiratest.Options) (*jiratest.Server, *Client) {
	t.Helper()
	fake := jiratest.New(opts)
	t.Cleanup(fake.Close)
	client, err := New(fake.Config())
	require.NoError(t, err)
	return fake, client
}

func TestNewValidates(t *testing.T) {
	ok := issuetracker.Config{BaseURL: "https://acme.atlassian.net", Account: "a@b.c", Secret: "tok", AuthKind: issuetracker.AuthBasic}
	client, err := New(ok)
	require.NoError(t, err)
	assert.Equal(t, Kind, client.Kind())

	tests := map[string]func(*issuetracker.Config){
		"no token":        func(c *issuetracker.Config) { c.Secret = "" },
		"no account":      func(c *issuetracker.Config) { c.Account = "" },
		"unknown auth":    func(c *issuetracker.Config) { c.AuthKind = "oauth" },
		"empty auth":      func(c *issuetracker.Config) { c.AuthKind = "" },
		"bad url":         func(c *issuetracker.Config) { c.BaseURL = "ftp://x" },
		"url credentials": func(c *issuetracker.Config) { c.BaseURL = "https://u:p@x" },
	}
	for name, mutate := range tests {
		cfg := ok
		mutate(&cfg)
		_, err := New(cfg)
		assert.Error(t, err, name)
	}

	bearer := issuetracker.Config{BaseURL: "https://jira.example.com", Secret: "pat", AuthKind: issuetracker.AuthBearer}
	_, err = New(bearer)
	require.NoError(t, err)
}

func TestNormalizeBaseURL(t *testing.T) {
	good := map[string]string{
		"https://acme.atlassian.net/":     "https://acme.atlassian.net",
		" HTTPS://Jira.Example.com/jira/": "https://jira.example.com/jira",
		"http://10.0.0.5:8080":            "http://10.0.0.5:8080",
	}
	for in, want := range good {
		got, err := NormalizeBaseURL(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got)
	}
	for _, bad := range []string{"", "acme.atlassian.net", "ftp://x", "https://", "https://u@x", "https://x?a=1", "https://x?", "https://x#f", "http://%zz", "https:opaque"} {
		_, err := NormalizeBaseURL(bad)
		assert.Error(t, err, bad)
	}
}

func TestRegistered(t *testing.T) {
	fake := jiratest.New(jiratest.Options{})
	defer fake.Close()
	tracker, err := issuetracker.New(Kind, fake.Config())
	require.NoError(t, err)
	assert.Equal(t, fake.URL+"/browse/GAME-1", tracker.IssueURL("GAME-1"))
	_, err = issuetracker.New(Kind, issuetracker.Config{})
	require.Error(t, err)
}

func TestProbe(t *testing.T) {
	_, cloud := newFake(t, jiratest.Options{})
	site, err := cloud.Probe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, &issuetracker.Site{
		Kind: Kind, Title: "Fake Jira", Version: "1001.0.0-SNAPSHOT", Deployment: "cloud",
		AccountID: "5b10a2844c20165700ede21g", AccountName: "Nipa Bot",
	}, site)

	fake, dc := newFake(t, jiratest.Options{AuthKind: issuetracker.AuthBearer, Secret: "pat-123", Deployment: "Data Center"})
	site, err = dc.Probe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "datacenter", site.Deployment)
	assert.Equal(t, "nipa-bot", site.AccountID)
	assert.Equal(t, "Bearer pat-123", fake.LastHeaders().Get("Authorization"))
	assert.Equal(t, userAgent, fake.LastHeaders().Get("User-Agent"))

	fake.Fail(jiratest.Failure{Status: http.StatusOK, Body: `{"key":"JIRAUSER1"}`})
	site, err = dc.Probe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "JIRAUSER1", site.AccountID)

	fake.Fail(jiratest.Failure{Status: http.StatusOK, Body: `{}`}, jiratest.Failure{Status: http.StatusBadGateway})
	_, err = dc.Probe(context.Background())
	require.ErrorIs(t, err, issuetracker.ErrUnavailable)
}

func TestProbeWrongToken(t *testing.T) {
	fake := jiratest.New(jiratest.Options{})
	defer fake.Close()
	cfg := fake.Config()
	cfg.Secret = "wrong-secret-value"
	client, err := New(cfg)
	require.NoError(t, err)

	_, err = client.Probe(context.Background())
	require.ErrorIs(t, err, issuetracker.ErrUnauthorized)
	assert.False(t, issuetracker.Retryable(err))
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "must be authenticated")
	assert.NotContains(t, err.Error(), "wrong-secret-value")
}

func TestIssue(t *testing.T) {
	fake, client := newFake(t, jiratest.Options{})
	fake.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "New rig", Type: "Story", Status: "In Progress", Assignee: "Ana"})
	fake.AddIssue(jiratest.Issue{Key: "GAME-2", Summary: "Done one", Status: "Done"})
	fake.AddIssue(jiratest.Issue{Key: "GAME-3", Summary: "Fresh"})

	issue, err := client.Issue(context.Background(), "GAME-1")
	require.NoError(t, err)
	assert.Equal(t, &issuetracker.Issue{
		Key: "GAME-1", Title: "New rig", Type: "Story", URL: fake.URL + "/browse/GAME-1",
		Status: "In Progress", StatusCategory: issuetracker.CategoryInProgress, Assignee: "Ana",
	}, issue)

	issue, err = client.Issue(context.Background(), "GAME-2")
	require.NoError(t, err)
	assert.Equal(t, issuetracker.CategoryDone, issue.StatusCategory)
	assert.Empty(t, issue.Assignee)

	issue, err = client.Issue(context.Background(), "GAME-3")
	require.NoError(t, err)
	assert.Equal(t, issuetracker.CategoryTodo, issue.StatusCategory)
	assert.Equal(t, "Task", issue.Type)

	_, err = client.Issue(context.Background(), "GAME-404")
	require.ErrorIs(t, err, issuetracker.ErrNotFound)
	assert.Contains(t, err.Error(), "does not exist")

	_, err = client.Issue(context.Background(), " ")
	require.Error(t, err)

	fake.Fail(jiratest.Failure{Status: http.StatusOK, Body: `{"fields":{"summary":"bare","status":{"name":"Odd","statusCategory":{"key":"undefined"}}}}`})
	issue, err = client.Issue(context.Background(), "GAME-9")
	require.NoError(t, err)
	assert.Equal(t, "GAME-9", issue.Key)
	assert.Empty(t, issue.StatusCategory)

	fake.Fail(jiratest.Failure{Status: http.StatusOK, Body: `not json`})
	_, err = client.Issue(context.Background(), "GAME-1")
	require.ErrorContains(t, err, "invalid response")
}

func TestIssueEscapesKey(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		http.NotFound(w, r)
	}))
	defer server.Close()
	client, err := New(issuetracker.Config{BaseURL: server.URL + "/ctx/", Secret: "s", AuthKind: issuetracker.AuthBearer, Transport: http.DefaultTransport})
	require.NoError(t, err)

	_, err = client.Issue(context.Background(), "../../admin?x=1")
	require.ErrorIs(t, err, issuetracker.ErrNotFound)
	assert.Equal(t, "/ctx/rest/api/2/issue/..%2F..%2Fadmin%3Fx=1", path)
	assert.Equal(t, server.URL+"/ctx/browse/a%2Fb", client.IssueURL("a/b"))
}

func TestLink(t *testing.T) {
	fake, client := newFake(t, jiratest.Options{})
	fake.AddIssue(jiratest.Issue{Key: "GAME-1"})
	ctx := context.Background()

	link := issuetracker.Link{ID: "nipa:acme/game:mr:7", URL: "https://nipa.example/acme/game/merge-requests/7", Title: "MR !7 New rig", Summary: "open"}
	require.NoError(t, client.Link(ctx, "GAME-1", link))
	link.Resolved = true
	link.Summary = "merged"
	require.NoError(t, client.Link(ctx, "GAME-1", link))
	require.NoError(t, client.Link(ctx, "GAME-1", issuetracker.Link{ID: "nipa:acme/game:commit:abc", URL: "https://nipa.example/c/abc"}))

	links := fake.RemoteLinks("GAME-1")
	require.Len(t, links, 2)
	assert.Equal(t, jiratest.RemoteLink{GlobalID: link.ID, URL: link.URL, Title: link.Title, Summary: "merged", Resolved: true}, links[0])
	assert.Equal(t, "https://nipa.example/c/abc", links[1].Title)

	require.Error(t, client.Link(ctx, "GAME-1", issuetracker.Link{URL: "x"}))
	require.Error(t, client.Link(ctx, "", link))
	require.ErrorIs(t, client.Link(ctx, "GAME-2", link), issuetracker.ErrNotFound)
}

func TestComment(t *testing.T) {
	fake, client := newFake(t, jiratest.Options{})
	fake.AddIssue(jiratest.Issue{Key: "GAME-1"})
	ctx := context.Background()

	require.NoError(t, client.Comment(ctx, "GAME-1", "Ana merged !7 into main"))
	assert.Equal(t, []string{"Ana merged !7 into main"}, fake.Comments("GAME-1"))
	assert.Equal(t, "application/json", fake.LastHeaders().Get("Content-Type"))

	require.Error(t, client.Comment(ctx, "GAME-1", "  "))
	require.Error(t, client.Comment(ctx, "", "x"))
	require.ErrorIs(t, client.Comment(ctx, "GAME-2", "x"), issuetracker.ErrNotFound)
}

func TestTransitionTo(t *testing.T) {
	fake, client := newFake(t, jiratest.Options{})
	fake.AddIssue(jiratest.Issue{Key: "GAME-1"})
	ctx := context.Background()

	require.NoError(t, client.TransitionTo(ctx, "GAME-1", "in progress"))
	issue, _ := fake.Issue("GAME-1")
	assert.Equal(t, "In Progress", issue.Status)

	require.NoError(t, client.TransitionTo(ctx, "GAME-1", " In Progress "), "already there")

	err := client.TransitionTo(ctx, "GAME-1", "Released")
	require.ErrorIs(t, err, issuetracker.ErrNoTransition)
	assert.False(t, issuetracker.Retryable(err))
	assert.Contains(t, err.Error(), `"In Progress"`)

	require.NoError(t, client.TransitionTo(ctx, "GAME-1", "Done"))
	issue, _ = fake.Issue("GAME-1")
	assert.Equal(t, "Done", issue.Status)

	require.Error(t, client.TransitionTo(ctx, "GAME-1", ""))
	require.Error(t, client.TransitionTo(ctx, "", "Done"))
	require.ErrorIs(t, client.TransitionTo(ctx, "GAME-2", "Done"), issuetracker.ErrNotFound)

	fake.Fail(jiratest.Failure{Status: http.StatusOK, Body: `{"transitions":[]}`}, jiratest.Failure{Status: http.StatusServiceUnavailable})
	require.ErrorIs(t, client.TransitionTo(ctx, "GAME-1", "To Do"), issuetracker.ErrUnavailable)
}

func TestSearch(t *testing.T) {
	fake, client := newFake(t, jiratest.Options{})
	fake.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "New rig"})
	fake.AddIssue(jiratest.Issue{Key: "GAME-2", Summary: "Rig weights"})
	fake.AddIssue(jiratest.Issue{Key: "GAME-3", Summary: "Audio"})
	ctx := context.Background()

	issues, err := client.Search(ctx, "rig", 0)
	require.NoError(t, err)
	assert.Equal(t, []issuetracker.Issue{
		{Key: "GAME-1", Title: "New rig", URL: fake.URL + "/browse/GAME-1"},
		{Key: "GAME-2", Title: "Rig weights", URL: fake.URL + "/browse/GAME-2"},
	}, issues)

	issues, err = client.Search(ctx, "", 1)
	require.NoError(t, err)
	assert.Len(t, issues, 1)

	issues, err = client.Search(ctx, "nothing", 500)
	require.NoError(t, err)
	assert.Empty(t, issues)
	assert.NotNil(t, issues)

	fake.Fail(jiratest.Failure{Status: http.StatusOK, Body: `{"sections":[{"issues":[{"key":""},{"key":"A-1","summary":"html"}]}]}`})
	issues, err = client.Search(ctx, "a", 10)
	require.NoError(t, err)
	assert.Equal(t, []issuetracker.Issue{{Key: "A-1", Title: "html", URL: fake.URL + "/browse/A-1"}}, issues)

	fake.Fail(jiratest.Failure{Status: http.StatusInternalServerError})
	_, err = client.Search(ctx, "rig", 5)
	require.ErrorIs(t, err, issuetracker.ErrUnavailable)
}

func TestStatusMapping(t *testing.T) {
	fake, client := newFake(t, jiratest.Options{})
	ctx := context.Background()

	fake.Fail(jiratest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "7"})
	_, err := client.Issue(ctx, "GAME-1")
	var limited *issuetracker.RateLimited
	require.ErrorAs(t, err, &limited)
	assert.Equal(t, 7*time.Second, limited.RetryAfter)
	assert.True(t, issuetracker.Retryable(err))

	fake.Fail(jiratest.Failure{Status: http.StatusForbidden, Body: `{"errorMessages":[],"errors":{"project":"no access"}}`})
	_, err = client.Issue(ctx, "GAME-1")
	require.ErrorIs(t, err, issuetracker.ErrUnauthorized)
	assert.Contains(t, err.Error(), "project: no access")

	fake.Fail(jiratest.Failure{Status: http.StatusBadRequest, Body: `{"errorMessages":["` + strings.Repeat("x", 400) + `"]}`})
	_, err = client.Issue(ctx, "GAME-1")
	require.Error(t, err)
	assert.False(t, issuetracker.Retryable(err))
	assert.Contains(t, err.Error(), "400")
	assert.Contains(t, err.Error(), "...")

	fake.Fail(jiratest.Failure{Status: http.StatusConflict, Body: `<html>`})
	_, err = client.Issue(ctx, "GAME-1")
	require.ErrorContains(t, err, "409 Conflict")

	fake.Fail(jiratest.Failure{Status: http.StatusFound})
	_, err = client.Issue(ctx, "GAME-1")
	require.Error(t, err)
	assert.False(t, issuetracker.Retryable(err))

	fake.Fail(jiratest.Failure{Status: http.StatusOK, Body: strings.Repeat(" ", MaxResponse+1)})
	_, err = client.Issue(ctx, "GAME-1")
	require.ErrorContains(t, err, "over 1 MiB")

	fake.Fail(jiratest.Failure{Status: http.StatusBadGateway, Body: strings.Repeat("x", MaxResponse+1)})
	_, err = client.Issue(ctx, "GAME-1")
	require.ErrorIs(t, err, issuetracker.ErrUnavailable)
	assert.True(t, issuetracker.Retryable(err))

	fake.Fail(jiratest.Failure{Status: http.StatusTooManyRequests, RetryAfter: "3", Body: strings.Repeat("x", MaxResponse+1)})
	_, err = client.Issue(ctx, "GAME-1")
	require.ErrorAs(t, err, &limited)
	assert.Equal(t, 3*time.Second, limited.RetryAfter)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	assert.Zero(t, parseRetryAfter("", now))
	assert.Zero(t, parseRetryAfter("soon", now))
	assert.Zero(t, parseRetryAfter("-5", now))
	assert.Equal(t, 30*time.Second, parseRetryAfter("30", now))
	assert.Equal(t, 90*time.Second, parseRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now))
	assert.Zero(t, parseRetryAfter(now.Add(-time.Hour).Format(http.TimeFormat), now))
}

func TestNoRedirects(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed = true }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client, err := New(issuetracker.Config{BaseURL: redirector.URL, Secret: "s", AuthKind: issuetracker.AuthBearer, Transport: http.DefaultTransport})
	require.NoError(t, err)
	_, err = client.Issue(context.Background(), "GAME-1")
	require.ErrorIs(t, err, errRedirect)
	assert.False(t, issuetracker.Retryable(err))
	assert.False(t, followed)
}

func TestTransportErrors(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	client, err := New(issuetracker.Config{BaseURL: url, Account: "a@b.c", Secret: "super-secret", AuthKind: issuetracker.AuthBasic, Transport: http.DefaultTransport})
	require.NoError(t, err)
	_, err = client.Probe(context.Background())
	require.ErrorIs(t, err, issuetracker.ErrUnavailable)
	assert.True(t, issuetracker.Retryable(err))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Issue(ctx, "GAME-1")
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, issuetracker.Retryable(err))

	guarded, err := New(issuetracker.Config{BaseURL: url, Secret: "s", AuthKind: issuetracker.AuthBearer})
	require.NoError(t, err)
	_, err = guarded.Issue(context.Background(), "GAME-1")
	require.True(t, egress.IsBlocked(err), "the default transport refuses loopback")
	assert.False(t, issuetracker.Retryable(err))
}

type echoTransport struct{}

func (echoTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("leaked " + r.Header.Get("Authorization"))
}

func TestErrorsRedactSecret(t *testing.T) {
	for _, cfg := range []issuetracker.Config{
		{BaseURL: "https://x.example", Account: "a@b.c", Secret: "super-secret", AuthKind: issuetracker.AuthBasic, Transport: echoTransport{}},
		{BaseURL: "https://x.example", Secret: "super-secret", AuthKind: issuetracker.AuthBearer, Transport: echoTransport{}},
	} {
		client, err := New(cfg)
		require.NoError(t, err)
		_, err = client.Issue(context.Background(), "GAME-1")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "super-secret")
		assert.NotContains(t, err.Error(), strings.TrimPrefix(client.auth, "Basic "))
		assert.Contains(t, err.Error(), "[redacted]")
	}
}
