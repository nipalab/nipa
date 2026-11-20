package issuetracker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubTracker struct{ cfg Config }

func (s stubTracker) Kind() string                                  { return "stub" }
func (s stubTracker) Probe(context.Context) (*Site, error)          { return &Site{Kind: "stub"}, nil }
func (s stubTracker) Issue(context.Context, string) (*Issue, error) { return nil, ErrNotFound }
func (s stubTracker) IssueURL(key string) string                    { return s.cfg.BaseURL + "/" + key }

var registerStubs = sync.OnceFunc(func() {
	Register("stub", func(cfg Config) (Tracker, error) { return stubTracker{cfg: cfg}, nil })
	Register("broken", func(Config) (Tracker, error) { return nil, errors.New("bad config") })
	for i := 0; i < 8; i++ {
		Register(fmt.Sprintf("concurrent-%d", i), func(cfg Config) (Tracker, error) { return stubTracker{cfg}, nil })
	}
})

func TestRegistry(t *testing.T) {
	registerStubs()

	tracker, err := New("stub", Config{BaseURL: "https://x"})
	require.NoError(t, err)
	assert.Equal(t, "https://x/A-1", tracker.IssueURL("A-1"))

	_, err = New("broken", Config{})
	require.ErrorContains(t, err, "bad config")

	_, err = New("nope", Config{})
	require.ErrorIs(t, err, ErrUnknownKind)
	require.ErrorContains(t, err, `"nope"`)

	assert.Subset(t, Kinds(), []string{"broken", "stub"})

	assert.Panics(t, func() { Register("stub", func(Config) (Tracker, error) { return nil, nil }) })
	assert.Panics(t, func() { Register("", func(Config) (Tracker, error) { return nil, nil }) })
	assert.Panics(t, func() { Register("nil", nil) })
}

func TestRegistryConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i == 0 {
				registerStubs()
			}
			_, _ = New(fmt.Sprintf("concurrent-%d", i), Config{})
			_ = Kinds()
		}(i)
	}
	wg.Wait()
}

func TestErrors(t *testing.T) {
	limited := fmt.Errorf("jira: %w", &RateLimited{RetryAfter: 3 * time.Second})
	assert.True(t, Retryable(limited))
	delay, ok := RetryAfter(limited)
	assert.True(t, ok)
	assert.Equal(t, 3*time.Second, delay)
	assert.Contains(t, limited.Error(), "retry after 3s")

	_, ok = RetryAfter(&RateLimited{})
	assert.False(t, ok)
	assert.Equal(t, "issue tracker rate limit", (&RateLimited{}).Error())

	assert.True(t, Retryable(fmt.Errorf("x: %w", ErrUnavailable)))
	for _, err := range []error{ErrNotFound, ErrUnauthorized, ErrNoTransition, errors.New("other"), nil} {
		assert.False(t, Retryable(err))
	}
	_, ok = RetryAfter(ErrUnavailable)
	assert.False(t, ok)
}
