package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	IssueStateOK          = "ok"
	IssueStateNotFound    = "not_found"
	IssueStateUnavailable = "unavailable"

	MaxResolveIssueKeys   = 50
	DefaultIssueLinkLimit = 50
	MaxIssueLinkLimit     = 100
	DefaultIssueSearch    = 20
	MaxIssueSearch        = 50
	maxIssueSearchQuery   = 200

	issueCacheTTL        = 60 * time.Second
	issueFailureTTL      = 10 * time.Second
	issueCacheMaxEntries = 5000
	issueFetchTimeout    = 10 * time.Second
	issueResolveTimeout  = 5 * time.Second
	issueResolveFanout   = 8
	issueSearchTimeout   = 10 * time.Second
)

type IssueKeyRef = domain.IssueKeyRef

// ResolvedIssue is an issue key as far as the tracker could tell: State is
// ok (Issue set), not_found or unavailable.
type ResolvedIssue struct {
	Key   string
	URL   string
	State string
	Issue *issuetracker.Issue
}

type IssueSearchResult struct {
	Issues      []ResolvedIssue
	Supported   bool
	Unavailable bool
}

// IssueLinksPage lists the work linked to one issue, newest first; Next is
// the cursor of the following page (zero = last page).
type IssueLinksPage struct {
	Key   string
	URL   string
	Links []domain.IssueLinkSubject
	Next  snow.ID
}

type issueConnection struct {
	settings *domain.ProjectIssueTracker
	tracker  *domain.IssueTracker
	client   issuetracker.Tracker
}

// connection returns the project's active connection, nil when the project
// does not use one.
func (u *IssueTracker) connection(ctx context.Context, projectID snow.ID) (*issueConnection, error) {
	settings, err := u.repo.GetProject(ctx, projectID)
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !settings.IsActive || len(settings.Keys) == 0 {
		return nil, nil
	}
	tracker, err := u.repo.GetByID(ctx, settings.TrackerID)
	if domain.IsErrorNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	client, err := u.clients.get(tracker, u.buildClient)
	if err != nil {
		return nil, err
	}
	return &issueConnection{settings: settings, tracker: tracker, client: client}, nil
}

func (u *IssueTracker) buildClient(t *domain.IssueTracker) (issuetracker.Tracker, error) {
	token, err := u.openSecret(t)
	if err != nil {
		return nil, fmt.Errorf("open issue tracker token: %w", err)
	}
	return u.factory(t.Kind, issuetracker.Config{BaseURL: t.BaseURL, Account: t.Account, Secret: token, AuthKind: t.AuthKind})
}

// SubjectIssueKeys returns the issue keys linked to each subject, with their
// tracker URLs, without calling the tracker. The caller has already checked
// that the subjects are readable. Projects without an active connection, an
// unavailable integration and lookup failures all answer nil.
func (u *IssueTracker) SubjectIssueKeys(ctx context.Context, projectID snow.ID, kind string, ids []snow.ID) map[snow.ID][]IssueKeyRef {
	if u == nil || u.box == nil || len(ids) == 0 {
		return nil
	}
	conn, err := u.connection(ctx, projectID)
	if err != nil {
		slog.Warn("loading the issue tracker connection failed", "project", projectID, "error", err)
		return nil
	}
	if conn == nil {
		return nil
	}
	links, err := u.repo.ListIssueLinksBySubjects(ctx, projectID, kind, ids)
	if err != nil {
		slog.Warn("loading issue links failed", "project", projectID, "kind", kind, "error", err)
		return nil
	}
	out := map[snow.ID][]IssueKeyRef{}
	for _, link := range links {
		key, ok := issuetracker.ParseKey(link.IssueKey, conn.settings.Keys)
		if !ok {
			continue
		}
		out[link.SubjectID] = append(out[link.SubjectID], IssueKeyRef{Key: key, URL: conn.client.IssueURL(key)})
	}
	return out
}

func (u *IssueTracker) readableConnection(ctx context.Context, projectID snow.ID) (*issueConnection, error) {
	if err := u.requireAvailable(); err != nil {
		return nil, err
	}
	if !u.perm.HasProjectAccess(ctx, projectID, domain.PermissionRead) {
		return nil, domain.NewErrorNoPermission()
	}
	conn, err := u.connection(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, domain.NewErrorNotFound("the project does not use an issue tracker")
	}
	return conn, nil
}

// ResolveIssues looks up title, status, type and assignee of up to 50 keys.
// Keys outside the project's configured keys are left out; a tracker that is
// down or refuses the credentials yields key and URL only.
func (u *IssueTracker) ResolveIssues(ctx context.Context, projectID snow.ID, rawKeys []string) ([]ResolvedIssue, error) {
	if len(rawKeys) > MaxResolveIssueKeys {
		return nil, domain.NewErrorUser(fmt.Sprintf("at most %d issue keys are allowed", MaxResolveIssueKeys))
	}
	conn, err := u.readableConnection(ctx, projectID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rawKeys))
	seen := map[string]bool{}
	for _, raw := range rawKeys {
		key, ok := issuetracker.ParseKey(raw, conn.settings.Keys)
		if ok && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	out := make([]ResolvedIssue, len(keys))
	ctx, cancel := context.WithTimeout(ctx, issueResolveTimeout)
	defer cancel()
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(issueResolveFanout)
	for i, key := range keys {
		group.Go(func() error {
			issue, state := u.issues.get(gctx, issueCacheKey(conn.tracker, key), func(ctx context.Context) (*issuetracker.Issue, error) {
				return conn.client.Issue(ctx, key)
			})
			out[i] = ResolvedIssue{Key: key, URL: conn.client.IssueURL(key), State: state, Issue: issue}
			return nil
		})
	}
	_ = group.Wait()
	return out, nil
}

// IssueLinks pages the commits, branches and merge requests linked to one
// issue. Subjects that no longer exist are skipped.
func (u *IssueTracker) IssueLinks(ctx context.Context, projectID snow.ID, rawKey string, before snow.ID, limit int) (*IssueLinksPage, error) {
	conn, err := u.readableConnection(ctx, projectID)
	if err != nil {
		return nil, err
	}
	key, ok := issuetracker.ParseKey(rawKey, conn.settings.Keys)
	if !ok {
		return nil, domain.NewErrorUser(fmt.Sprintf("%q is not an issue of this project's keys (%s)", rawKey, strings.Join(conn.settings.Keys, ", ")))
	}
	if limit <= 0 {
		limit = DefaultIssueLinkLimit
	}
	limit = min(limit, MaxIssueLinkLimit)
	rows, err := u.repo.ListIssueSubjects(ctx, projectID, key, before, limit+1)
	if err != nil {
		return nil, err
	}
	page := &IssueLinksPage{Key: key, URL: conn.client.IssueURL(key), Links: []domain.IssueLinkSubject{}}
	if len(rows) > limit {
		rows = rows[:limit]
		page.Next = rows[limit-1].ID
	}
	for _, row := range rows {
		if !row.Missing {
			page.Links = append(page.Links, row)
		}
	}
	return page, nil
}

// SearchIssues backs issue pickers when the tracker can search; results are
// limited to the project's keys. A tracker without search answers
// Supported=false, a failing one Unavailable=true.
func (u *IssueTracker) SearchIssues(ctx context.Context, projectID snow.ID, query string, limit int) (*IssueSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, domain.NewErrorUser("q is required")
	}
	if len(query) > maxIssueSearchQuery {
		return nil, domain.NewErrorUser("q is too long")
	}
	conn, err := u.readableConnection(ctx, projectID)
	if err != nil {
		return nil, err
	}
	result := &IssueSearchResult{Issues: []ResolvedIssue{}}
	searcher, ok := conn.client.(issuetracker.Searcher)
	if !ok {
		return result, nil
	}
	result.Supported = true
	if limit <= 0 {
		limit = DefaultIssueSearch
	}
	limit = min(limit, MaxIssueSearch)
	ctx, cancel := context.WithTimeout(ctx, issueSearchTimeout)
	defer cancel()
	found, err := searcher.Search(ctx, query, limit)
	if err != nil {
		slog.Warn("issue search failed", "project", projectID, "error", err)
		result.Unavailable = true
		return result, nil
	}
	for i := range found {
		key, ok := issuetracker.ParseKey(found[i].Key, conn.settings.Keys)
		if !ok {
			continue
		}
		issue := found[i]
		result.Issues = append(result.Issues, ResolvedIssue{Key: key, URL: conn.client.IssueURL(key), State: IssueStateOK, Issue: &issue})
	}
	return result, nil
}

func issueCacheKey(t *domain.IssueTracker, key string) string {
	return fmt.Sprintf("%d/%d/%s", t.ID, t.UpdatedAt.UnixNano(), key)
}

type cachedIssueClient struct {
	updatedAt time.Time
	client    issuetracker.Tracker
}

// issueClientCache keeps one client per connection, rebuilt when the
// connection's updated_at changes.
type issueClientCache struct {
	mu      sync.Mutex
	clients map[snow.ID]cachedIssueClient
}

func newIssueClientCache() *issueClientCache {
	return &issueClientCache{clients: map[snow.ID]cachedIssueClient{}}
}

func (c *issueClientCache) get(t *domain.IssueTracker, build func(*domain.IssueTracker) (issuetracker.Tracker, error)) (issuetracker.Tracker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cached, ok := c.clients[t.ID]; ok && cached.updatedAt.Equal(t.UpdatedAt) {
		return cached.client, nil
	}
	client, err := build(t)
	if err != nil {
		return nil, err
	}
	c.clients[t.ID] = cachedIssueClient{updatedAt: t.UpdatedAt, client: client}
	return client, nil
}

type issueCacheEntry struct {
	issue   *issuetracker.Issue
	state   string
	expires time.Time
}

// issueCache holds tracker answers for a short time: found and unknown keys
// for the TTL, failures for issueFailureTTL so a tracker that is down is not
// asked on every page view. Concurrent misses share one fetch.
type issueCache struct {
	mu      sync.Mutex
	entries map[string]issueCacheEntry
	group   singleflight.Group
	ttl     time.Duration
	max     int
	now     func() time.Time
}

func newIssueCache(ttl time.Duration, size int) *issueCache {
	return &issueCache{entries: map[string]issueCacheEntry{}, ttl: ttl, max: size, now: time.Now}
}

func (c *issueCache) lookup(key string) (issueCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !c.now().Before(entry.expires) {
		return issueCacheEntry{}, false
	}
	return entry, true
}

func (c *issueCache) store(key string, entry issueCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		now := c.now()
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		for k := range c.entries {
			if len(c.entries) < c.max*3/4 {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = entry
}

func (c *issueCache) get(ctx context.Context, key string, fetch func(context.Context) (*issuetracker.Issue, error)) (*issuetracker.Issue, string) {
	if entry, ok := c.lookup(key); ok {
		return entry.issue, entry.state
	}
	ch := c.group.DoChan(key, func() (any, error) {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), issueFetchTimeout)
		defer cancel()
		issue, err := fetch(fetchCtx)
		entry := issueCacheEntry{issue: issue, state: IssueStateOK, expires: c.now().Add(c.ttl)}
		switch {
		case errors.Is(err, issuetracker.ErrNotFound):
			entry = issueCacheEntry{state: IssueStateNotFound, expires: c.now().Add(c.ttl)}
		case err != nil:
			slog.Warn("issue lookup failed", "error", err)
			entry = issueCacheEntry{state: IssueStateUnavailable, expires: c.now().Add(issueFailureTTL)}
		case issue == nil:
			entry = issueCacheEntry{state: IssueStateNotFound, expires: c.now().Add(c.ttl)}
		}
		c.store(key, entry)
		return entry, nil
	})
	select {
	case res := <-ch:
		entry := res.Val.(issueCacheEntry)
		return entry.issue, entry.state
	case <-ctx.Done():
		return nil, IssueStateUnavailable
	}
}
