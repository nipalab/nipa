// Package issuetracker links Nipa work to issues that live in an external
// tracker. Trackers implement the small Tracker core; the optional Linker,
// Commenter, Transitioner and Searcher capabilities are type-asserted by the
// caller, which skips what a tracker cannot do.
package issuetracker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	AuthBasic  = "basic"
	AuthBearer = "bearer"

	CategoryTodo       = "todo"
	CategoryInProgress = "in_progress"
	CategoryDone       = "done"
)

type Tracker interface {
	Kind() string
	Probe(ctx context.Context) (*Site, error)
	Issue(ctx context.Context, key string) (*Issue, error)
	IssueURL(key string) string
}

type Linker interface {
	// Link is an upsert keyed by Link.ID.
	Link(ctx context.Context, key string, link Link) error
}

type Commenter interface {
	Comment(ctx context.Context, key, text string) error
}

type Transitioner interface {
	// TransitionTo moves the issue to the named status. ErrNoTransition when
	// the workflow has no such step from the current status.
	TransitionTo(ctx context.Context, key, status string) error
}

type Searcher interface {
	Search(ctx context.Context, query string, limit int) ([]Issue, error)
}

// Site describes the tracker and the account a connection signs in as.
type Site struct {
	Kind        string
	Title       string
	Version     string
	Deployment  string
	AccountID   string
	AccountName string
}

type Issue struct {
	Key            string
	Title          string
	Type           string
	URL            string
	Status         string
	StatusCategory string
	Assignee       string
}

type Link struct {
	ID       string
	URL      string
	Title    string
	Summary  string
	Resolved bool
}

type Config struct {
	BaseURL  string
	Account  string
	Secret   string
	AuthKind string
	// Transport carries every request; trackers default to an egress-guarded
	// transport when it is nil.
	Transport http.RoundTripper
}

type Factory func(Config) (Tracker, error)

var (
	ErrNotFound     = errors.New("issue not found")
	ErrUnauthorized = errors.New("issue tracker refused the credentials")
	ErrNoTransition = errors.New("no matching transition")
	ErrUnavailable  = errors.New("issue tracker unavailable")
)

type RateLimited struct {
	RetryAfter time.Duration
}

func (e *RateLimited) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("issue tracker rate limit, retry after %s", e.RetryAfter)
	}
	return "issue tracker rate limit"
}

// Retryable reports whether a failed call may succeed later: rate limits and
// unavailability are retried, everything else is permanent.
func Retryable(err error) bool {
	var limited *RateLimited
	return errors.As(err, &limited) || errors.Is(err, ErrUnavailable)
}

// RetryAfter returns the delay a rate-limited tracker asked for, if any.
func RetryAfter(err error) (time.Duration, bool) {
	var limited *RateLimited
	if errors.As(err, &limited) && limited.RetryAfter > 0 {
		return limited.RetryAfter, true
	}
	return 0, false
}
