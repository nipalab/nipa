// Package jira is the Jira Cloud and Data Center tracker. It speaks
// /rest/api/2 only, which both deployments serve with plain-text comments.
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/egress"
	"github.com/nipalab/nipa/internal/issuetracker"
)

const (
	Kind = "jira"

	Timeout        = 15 * time.Second
	MaxResponse    = 1 << 20
	DefaultLimit   = 20
	MaxSearchLimit = 50

	userAgent     = "Nipa/1.0"
	apiPrefix     = "/rest/api/2"
	applicationID = "com.nipalab.nipa"
	maxErrorText  = 300
)

func init() {
	issuetracker.Register(Kind, func(cfg issuetracker.Config) (issuetracker.Tracker, error) {
		return New(cfg)
	})
}

var errRedirect = errors.New("redirects are not followed")

type Client struct {
	base   string
	auth   string
	secret string
	http   *http.Client
}

var (
	_ issuetracker.Linker       = (*Client)(nil)
	_ issuetracker.Commenter    = (*Client)(nil)
	_ issuetracker.Transitioner = (*Client)(nil)
	_ issuetracker.Searcher     = (*Client)(nil)
)

// New validates the configuration. Without a Transport every request goes
// through the egress guard (public addresses only).
func New(cfg issuetracker.Config) (*Client, error) {
	base, err := NormalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if cfg.Secret == "" {
		return nil, errors.New("jira: a token is required")
	}
	var auth string
	switch cfg.AuthKind {
	case issuetracker.AuthBasic:
		if cfg.Account == "" {
			return nil, errors.New("jira: basic auth needs the account email")
		}
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Account+":"+cfg.Secret))
	case issuetracker.AuthBearer:
		auth = "Bearer " + cfg.Secret
	default:
		return nil, fmt.Errorf("jira: auth kind must be %q or %q", issuetracker.AuthBasic, issuetracker.AuthBearer)
	}
	transport := cfg.Transport
	if transport == nil {
		transport = egress.New(egress.Config{Label: "issue tracker"}).Transport()
	}
	return &Client{
		base:   base,
		auth:   auth,
		secret: cfg.Secret,
		http: &http.Client{
			Timeout:   Timeout,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errRedirect
			},
		},
	}, nil
}

// NormalizeBaseURL accepts an http(s) site URL with an optional context path
// (`https://jira.example.com/jira`) and no credentials, query or fragment.
func NormalizeBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("jira: invalid base url")
	}
	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return "", errors.New("jira: base url must be http or https")
	case parsed.Host == "" || parsed.Opaque != "":
		return "", errors.New("jira: base url needs a host")
	case parsed.User != nil:
		return "", errors.New("jira: base url must not carry credentials")
	case parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawFragment != "":
		return "", errors.New("jira: base url must not have a query or fragment")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (c *Client) Kind() string { return Kind }

func (c *Client) IssueURL(key string) string {
	return c.base + "/browse/" + url.PathEscape(key)
}

type myself struct {
	AccountID   string `json:"accountId"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	DisplayName string `json:"displayName"`
}

type serverInfo struct {
	Version        string `json:"version"`
	DeploymentType string `json:"deploymentType"`
	ServerTitle    string `json:"serverTitle"`
}

func (c *Client) Probe(ctx context.Context) (*issuetracker.Site, error) {
	var me myself
	if err := c.do(ctx, http.MethodGet, "/myself", nil, nil, &me); err != nil {
		return nil, err
	}
	var info serverInfo
	if err := c.do(ctx, http.MethodGet, "/serverInfo", nil, nil, &info); err != nil {
		return nil, err
	}
	accountID := me.AccountID
	if accountID == "" {
		accountID = me.Name
	}
	if accountID == "" {
		accountID = me.Key
	}
	return &issuetracker.Site{
		Kind:        Kind,
		Title:       info.ServerTitle,
		Version:     info.Version,
		Deployment:  strings.ToLower(strings.ReplaceAll(info.DeploymentType, " ", "")),
		AccountID:   accountID,
		AccountName: me.DisplayName,
	}, nil
}

type issueResponse struct {
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  *struct {
			Name           string `json:"name"`
			StatusCategory *struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		IssueType *struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Assignee *struct {
			DisplayName string `json:"displayName"`
		} `json:"assignee"`
	} `json:"fields"`
}

func (c *Client) Issue(ctx context.Context, key string) (*issuetracker.Issue, error) {
	path, err := issuePath(key, "")
	if err != nil {
		return nil, err
	}
	var resp issueResponse
	query := url.Values{"fields": {"summary,status,issuetype,assignee"}}
	if err := c.do(ctx, http.MethodGet, path, query, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Key == "" {
		resp.Key = key
	}
	issue := &issuetracker.Issue{
		Key:   resp.Key,
		Title: resp.Fields.Summary,
		URL:   c.IssueURL(resp.Key),
	}
	if status := resp.Fields.Status; status != nil {
		issue.Status = status.Name
		if status.StatusCategory != nil {
			issue.StatusCategory = statusCategory(status.StatusCategory.Key)
		}
	}
	if resp.Fields.IssueType != nil {
		issue.Type = resp.Fields.IssueType.Name
	}
	if resp.Fields.Assignee != nil {
		issue.Assignee = resp.Fields.Assignee.DisplayName
	}
	return issue, nil
}

func statusCategory(key string) string {
	switch key {
	case "new":
		return issuetracker.CategoryTodo
	case "indeterminate":
		return issuetracker.CategoryInProgress
	case "done":
		return issuetracker.CategoryDone
	}
	return ""
}

type remoteLink struct {
	GlobalID    string            `json:"globalId"`
	Application remoteApplication `json:"application"`
	Object      remoteObject      `json:"object"`
}

type remoteApplication struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type remoteObject struct {
	URL     string       `json:"url"`
	Title   string       `json:"title"`
	Summary string       `json:"summary,omitempty"`
	Status  remoteStatus `json:"status"`
}

type remoteStatus struct {
	Resolved bool `json:"resolved"`
}

// Link creates or updates the remote link whose globalId is link.ID.
func (c *Client) Link(ctx context.Context, key string, link issuetracker.Link) error {
	if link.ID == "" || link.URL == "" {
		return errors.New("jira: a link needs an id and a url")
	}
	path, err := issuePath(key, "/remotelink")
	if err != nil {
		return err
	}
	title := link.Title
	if title == "" {
		title = link.URL
	}
	body := remoteLink{
		GlobalID:    link.ID,
		Application: remoteApplication{Type: applicationID, Name: "Nipa"},
		Object: remoteObject{
			URL:     link.URL,
			Title:   title,
			Summary: link.Summary,
			Status:  remoteStatus{Resolved: link.Resolved},
		},
	}
	return c.do(ctx, http.MethodPost, path, nil, body, nil)
}

func (c *Client) Comment(ctx context.Context, key, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("jira: empty comment")
	}
	path, err := issuePath(key, "/comment")
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, nil, map[string]string{"body": text}, nil)
}

type transitionsResponse struct {
	Transitions []struct {
		ID string `json:"id"`
		To struct {
			Name string `json:"name"`
		} `json:"to"`
	} `json:"transitions"`
}

// TransitionTo moves the issue to status. An issue already in that status is
// left alone, so a retried transition that already landed succeeds.
func (c *Client) TransitionTo(ctx context.Context, key, status string) error {
	status = strings.TrimSpace(status)
	if status == "" {
		return errors.New("jira: empty status")
	}
	path, err := issuePath(key, "/transitions")
	if err != nil {
		return err
	}
	var resp transitionsResponse
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &resp); err != nil {
		return err
	}
	for _, t := range resp.Transitions {
		if strings.EqualFold(strings.TrimSpace(t.To.Name), status) {
			body := map[string]any{"transition": map[string]string{"id": t.ID}}
			return c.do(ctx, http.MethodPost, path, nil, body, nil)
		}
	}
	issue, err := c.Issue(ctx, key)
	if err != nil {
		return err
	}
	if strings.EqualFold(issue.Status, status) {
		return nil
	}
	return fmt.Errorf("%w: %s cannot move from %q to %q", issuetracker.ErrNoTransition, key, issue.Status, status)
}

type pickerResponse struct {
	Sections []struct {
		Issues []struct {
			Key         string `json:"key"`
			Summary     string `json:"summary"`
			SummaryText string `json:"summaryText"`
		} `json:"issues"`
	} `json:"sections"`
}

// Search asks the issue picker, which matches keys and summary words. Results
// carry key, title and URL only; limit defaults to 20 and is capped at 50.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]issuetracker.Issue, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxSearchLimit)
	var resp pickerResponse
	params := url.Values{"query": {strings.TrimSpace(query)}}
	if err := c.do(ctx, http.MethodGet, "/issue/picker", params, nil, &resp); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	issues := []issuetracker.Issue{}
	for _, section := range resp.Sections {
		for _, found := range section.Issues {
			if found.Key == "" || seen[found.Key] {
				continue
			}
			seen[found.Key] = true
			title := found.SummaryText
			if title == "" {
				title = found.Summary
			}
			issues = append(issues, issuetracker.Issue{Key: found.Key, Title: title, URL: c.IssueURL(found.Key)})
			if len(issues) == limit {
				return issues, nil
			}
		}
	}
	return issues, nil
}

func issuePath(key, suffix string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("jira: empty issue key")
	}
	return "/issue/" + url.PathEscape(key) + suffix, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	endpoint := c.base + apiPrefix + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return c.fail(nil, method, path, err)
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return c.fail(transportKind(ctx, err), method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponse+1))
	if err != nil {
		return c.fail(issuetracker.ErrUnavailable, method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return c.statusError(resp, method, path, data[:min(len(data), MaxResponse)])
	}
	if len(data) > MaxResponse {
		return c.fail(nil, method, path, errors.New("response over 1 MiB"))
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return c.fail(nil, method, path, fmt.Errorf("invalid response: %w", err))
	}
	return nil
}

func transportKind(ctx context.Context, err error) error {
	switch {
	case egress.IsBlocked(err), errors.Is(err, errRedirect):
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	}
	return issuetracker.ErrUnavailable
}

func (c *Client) statusError(resp *http.Response, method, path string, body []byte) error {
	detail := fmt.Errorf("%s%s", resp.Status, errorText(body))
	switch code := resp.StatusCode; {
	case code == http.StatusTooManyRequests:
		limited := &issuetracker.RateLimited{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
		return c.fail(limited, method, path, detail)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return c.fail(issuetracker.ErrUnauthorized, method, path, detail)
	case code == http.StatusNotFound:
		return c.fail(issuetracker.ErrNotFound, method, path, detail)
	case code >= 500:
		return c.fail(issuetracker.ErrUnavailable, method, path, detail)
	}
	return c.fail(nil, method, path, detail)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(max(seconds, 0)) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(now), 0)
	}
	return 0
}

func errorText(body []byte) string {
	var parsed struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return ""
	}
	parts := append([]string(nil), parsed.ErrorMessages...)
	for field, msg := range parsed.Errors {
		parts = append(parts, field+": "+msg)
	}
	text := strings.Join(parts, "; ")
	if len(text) > maxErrorText {
		text = text[:maxErrorText] + "..."
	}
	if text == "" {
		return ""
	}
	return ": " + text
}

// requestError keeps the cause chain for errors.Is/As while its text never
// carries the secret.
type requestError struct {
	msg   string
	kind  error
	cause error
}

func (e *requestError) Error() string { return e.msg }

func (e *requestError) Unwrap() []error {
	if e.kind == nil {
		return []error{e.cause}
	}
	return []error{e.kind, e.cause}
}

func (c *Client) fail(kind error, method, path string, cause error) error {
	msg := fmt.Sprintf("jira %s %s: %s", method, path, cause.Error())
	if kind != nil && !errors.Is(cause, kind) {
		msg = fmt.Sprintf("%s: %s", kind.Error(), msg)
	}
	return &requestError{msg: c.redact(msg), kind: kind, cause: cause}
}

func (c *Client) redact(s string) string {
	for _, secret := range []string{c.auth, strings.TrimPrefix(strings.TrimPrefix(c.auth, "Basic "), "Bearer "), c.secret} {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[redacted]")
		}
	}
	return s
}
