package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/egress"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

type fakeCoreTracker struct{}

func (fakeCoreTracker) Kind() string { return "fake" }
func (fakeCoreTracker) Probe(context.Context) (*issuetracker.Site, error) {
	return &issuetracker.Site{}, nil
}
func (fakeCoreTracker) Issue(context.Context, string) (*issuetracker.Issue, error) {
	return nil, issuetracker.ErrNotFound
}
func (fakeCoreTracker) IssueURL(key string) string { return "https://jira/" + key }

type fakeFullTracker struct {
	fakeCoreTracker
	mu       sync.Mutex
	calls    []string
	err      error
	issue    *issuetracker.Issue
	issueErr error
}

func (f *fakeFullTracker) record(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.err
}

func (f *fakeFullTracker) Issue(_ context.Context, key string) (*issuetracker.Issue, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "issue "+key)
	f.mu.Unlock()
	return f.issue, f.issueErr
}

func (f *fakeFullTracker) Link(_ context.Context, key string, link issuetracker.Link) error {
	return f.record(fmt.Sprintf("link %s %s %s %v", key, link.ID, link.Title, link.Resolved))
}

func (f *fakeFullTracker) Unlink(_ context.Context, key, linkID string) error {
	return f.record("unlink " + key + " " + linkID)
}

func (f *fakeFullTracker) Comment(_ context.Context, key, text string) error {
	return f.record("comment " + key + " " + text)
}

func (f *fakeFullTracker) TransitionTo(_ context.Context, key, status string) error {
	return f.record("transition " + key + " " + status)
}

func (f *fakeFullTracker) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type dispatchFixture struct {
	store   *MockissueJobQueue
	clients *MockissueJobClients
	tracker *fakeFullTracker
	metrics *obs.Metrics
	d       *IssueDispatcher
	now     time.Time
}

func newDispatchFixture(t *testing.T, cfg IssueDispatchConfig) *dispatchFixture {
	ctrl := gomock.NewController(t)
	f := &dispatchFixture{
		store:   NewMockissueJobQueue(ctrl),
		clients: NewMockissueJobClients(ctrl),
		tracker: &fakeFullTracker{},
		metrics: obs.NewMetrics(),
		now:     time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	}
	f.d = NewIssueDispatcher(f.store, f.clients, cfg).WithMetrics(f.metrics)
	f.d.now = func() time.Time { return f.now }
	return f
}

func (f *dispatchFixture) client() {
	f.clients.EXPECT().JobClient(gomock.Any(), snow.ID(1), snow.ID(9)).Return(f.tracker, nil)
}

func issueJob(action, payload string, attempt int64) domain.IssueTrackerJob {
	return domain.IssueTrackerJob{ID: 50, TrackerID: 9, ProjectID: 1, IssueKey: "GAME-1", Action: action, Payload: payload, Attempt: attempt, State: domain.IssueJobPending}
}

func scrapeIssueMetric(t *testing.T, m *obs.Metrics, series string) string {
	t.Helper()
	rr := httptest.NewRecorder()
	m.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(rr.Body.String(), "\n") {
		if strings.HasPrefix(line, series+" ") {
			return strings.TrimPrefix(line, series+" ")
		}
	}
	return ""
}

func (f *dispatchFixture) requests(t *testing.T, op, result string) string {
	return scrapeIssueMetric(t, f.metrics, `nipa_issue_tracker_requests_total{kind="fake",op="`+op+`",result="`+result+`"}`)
}

func TestIssueDispatcher_DeliversEachAction(t *testing.T) {
	f := newDispatchFixture(t, IssueDispatchConfig{})
	for _, tc := range []struct {
		action, payload, call string
	}{
		{domain.IssueJobLink, `{"link_id":"nipa:a/b:mr:1","url":"https://x","title":"#1 Rig","resolved":true}`, "link GAME-1 nipa:a/b:mr:1 #1 Rig true"},
		{domain.IssueJobComment, `{"text":"Ana merged"}`, "comment GAME-1 Ana merged"},
		{domain.IssueJobUnlink, `{"link_id":"nipa:a/b:mr:1"}`, "unlink GAME-1 nipa:a/b:mr:1"},
		{domain.IssueJobTransition, `{"status":"Done"}`, "transition GAME-1 Done"},
	} {
		f.tracker.calls = nil
		f.d.throttle = newIssueThrottle(time.Nanosecond)
		f.client()
		f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), f.now).Return(true, nil)
		f.d.deliver(issueJob(tc.action, tc.payload, 1))
		require.Equal(t, []string{tc.call}, f.tracker.Calls())
		require.Equal(t, "1", f.requests(t, tc.action, issueRequestOK))
	}
}

func TestIssueDispatcher_OpenedTransitionSkipsDoneIssues(t *testing.T) {
	f := newDispatchFixture(t, IssueDispatchConfig{MinInterval: time.Nanosecond})
	payload := `{"status":"In Review","skip_done":true}`

	f.tracker.issue = &issuetracker.Issue{Key: "GAME-1", Status: "Done", StatusCategory: issuetracker.CategoryDone}
	f.client()
	f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), f.now).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobTransition, payload, 1))
	require.Equal(t, []string{"issue GAME-1"}, f.tracker.Calls())

	f.tracker.calls = nil
	f.tracker.issue = &issuetracker.Issue{Key: "GAME-1", Status: "In Progress", StatusCategory: issuetracker.CategoryInProgress}
	f.client()
	f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), f.now).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobTransition, payload, 1))
	require.Equal(t, []string{"issue GAME-1", "transition GAME-1 In Review"}, f.tracker.Calls())

	f.tracker.issueErr = issuetracker.ErrNotFound
	f.client()
	f.store.EXPECT().MarkIssueJobFailed(gomock.Any(), snow.ID(50), int64(1), gomock.Any()).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobTransition, payload, 1))
}

func TestIssueDispatcher_RetriesWithBackoffAndRetryAfter(t *testing.T) {
	f := newDispatchFixture(t, IssueDispatchConfig{InitialBackoff: time.Second, BackoffFactor: 2, MinInterval: time.Nanosecond})

	f.tracker.err = fmt.Errorf("%w: jira 503", issuetracker.ErrUnavailable)
	f.client()
	f.store.EXPECT().ScheduleIssueJobRetry(gomock.Any(), snow.ID(50), int64(3), "issue tracker unavailable: jira 503", f.now.Add(4*time.Second)).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 3))
	require.Equal(t, "1", f.requests(t, domain.IssueJobLink, issueRequestRetry))

	f.tracker.err = &issuetracker.RateLimited{RetryAfter: time.Minute}
	f.client()
	f.store.EXPECT().ScheduleIssueJobRetry(gomock.Any(), snow.ID(50), int64(1), gomock.Any(), f.now.Add(time.Minute)).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobComment, `{"text":"x"}`, 1))
	require.Equal(t, time.Minute, f.d.throttle.reserve(9, f.now), "the connection is held for Retry-After")

	f.d.throttle = newIssueThrottle(time.Nanosecond)
	f.tracker.err = context.DeadlineExceeded
	f.client()
	f.store.EXPECT().ScheduleIssueJobRetry(gomock.Any(), snow.ID(50), int64(1), gomock.Any(), f.now.Add(time.Second)).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobComment, `{"text":"x"}`, 1))

	f.tracker.err = issuetracker.ErrUnavailable
	f.client()
	f.store.EXPECT().MarkIssueJobFailed(gomock.Any(), snow.ID(50), int64(8), "issue tracker unavailable").Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobComment, `{"text":"x"}`, 8))
	require.Equal(t, "1", f.requests(t, domain.IssueJobComment, issueRequestFailed))
}

func TestIssueDispatcher_PermanentFailures(t *testing.T) {
	f := newDispatchFixture(t, IssueDispatchConfig{MinInterval: time.Nanosecond})
	for _, err := range []error{issuetracker.ErrNotFound, issuetracker.ErrUnauthorized, issuetracker.ErrNoTransition, egress.ErrBlocked} {
		f.tracker.err = err
		f.client()
		f.store.EXPECT().MarkIssueJobFailed(gomock.Any(), snow.ID(50), int64(1), err.Error()).Return(true, nil)
		f.d.deliver(issueJob(domain.IssueJobTransition, `{"status":"Done"}`, 1))
	}
	f.tracker.err = nil

	for _, job := range []domain.IssueTrackerJob{
		issueJob(domain.IssueJobLink, `{`, 1),
		issueJob("delete", `{}`, 1),
	} {
		f.client()
		f.store.EXPECT().MarkIssueJobFailed(gomock.Any(), snow.ID(50), int64(1), gomock.Any()).Return(true, nil)
		f.d.deliver(job)
	}

	core := fakeCoreTracker{}
	for _, action := range []string{domain.IssueJobLink, domain.IssueJobUnlink, domain.IssueJobComment, domain.IssueJobTransition} {
		f.clients.EXPECT().JobClient(gomock.Any(), snow.ID(1), snow.ID(9)).Return(core, nil)
		f.store.EXPECT().MarkIssueJobFailed(gomock.Any(), snow.ID(50), int64(1), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ snow.ID, _ int64, reason string) (bool, error) {
				require.Contains(t, reason, "fake trackers do not support")
				return true, nil
			})
		f.d.deliver(issueJob(action, `{}`, 1))
	}

	f.store.EXPECT().MarkIssueJobFailed(gomock.Any(), snow.ID(50), int64(9), "gave up after 8 attempts: boom").Return(true, nil)
	job := issueJob(domain.IssueJobLink, `{}`, 9)
	job.LastError = "boom"
	f.d.deliver(job)
}

func TestIssueDispatcher_ConnectionLookup(t *testing.T) {
	f := newDispatchFixture(t, IssueDispatchConfig{InitialBackoff: time.Second})

	f.clients.EXPECT().JobClient(gomock.Any(), snow.ID(1), snow.ID(9)).Return(nil, fmt.Errorf("%w: the issue tracker is no longer active for this project", errIssueJobPermanent))
	f.store.EXPECT().MarkIssueJobFailed(gomock.Any(), snow.ID(50), int64(1), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ snow.ID, _ int64, reason string) (bool, error) {
			require.Contains(t, reason, "no longer active")
			return true, nil
		})
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 1))

	f.clients.EXPECT().JobClient(gomock.Any(), snow.ID(1), snow.ID(9)).Return(nil, errors.New("db down"))
	f.store.EXPECT().ScheduleIssueJobRetry(gomock.Any(), snow.ID(50), int64(1), "db down", f.now.Add(time.Second)).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 1))
}

func TestIssueDispatcher_SettleReleasesRearmedJobs(t *testing.T) {
	f := newDispatchFixture(t, IssueDispatchConfig{MinInterval: time.Nanosecond})
	f.client()
	f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), f.now).Return(false, nil)
	f.store.EXPECT().ReleaseIssueJob(gomock.Any(), snow.ID(50), f.now).Return(nil)
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 1))
	require.Len(t, f.d.kick, 1)

	<-f.d.kick
	f.client()
	f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), f.now).Return(false, nil)
	f.store.EXPECT().ReleaseIssueJob(gomock.Any(), snow.ID(50), f.now).Return(errors.New("db down"))
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 1))
	require.Empty(t, f.d.kick)

	f.client()
	f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), f.now).Return(false, errors.New("db down"))
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 1))
}

func TestIssueDispatcher_ThrottleSpacesAndPostpones(t *testing.T) {
	throttle := newIssueThrottle(time.Second)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	require.Zero(t, throttle.reserve(9, now))
	require.Equal(t, time.Second, throttle.reserve(9, now))
	require.Zero(t, throttle.reserve(10, now))
	throttle.hold(9, now.Add(time.Second))
	require.Equal(t, 2*time.Second, throttle.reserve(9, now))

	f := newDispatchFixture(t, IssueDispatchConfig{MinInterval: 20 * time.Millisecond, MaxThrottleWait: time.Second})
	f.d.now = time.Now
	f.d.throttle.hold(9, time.Now().Add(time.Hour))
	f.client()
	f.store.EXPECT().DeferIssueJob(gomock.Any(), snow.ID(50), int64(2), gomock.Any()).Return(true, nil)
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 2))

	f.d.throttle = newIssueThrottle(30 * time.Millisecond)
	f.d.throttle.reserve(9, time.Now())
	f.client()
	f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), gomock.Any()).Return(true, nil)
	start := time.Now()
	f.d.deliver(issueJob(domain.IssueJobLink, `{}`, 1))
	require.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
}

func TestIssueDispatcher_BackoffAndDefaults(t *testing.T) {
	d := NewIssueDispatcher(nil, nil, IssueDispatchConfig{})
	require.Equal(t, defaultIssueWorkers, d.cfg.Workers)
	require.Equal(t, int64(defaultIssueMaxAttempts), d.cfg.MaxAttempts)
	require.Equal(t, 30*time.Second, d.backoff(1))
	require.Equal(t, 90*time.Second, d.backoff(2))
	require.Equal(t, defaultIssueMaxBackoff, d.backoff(20))

	long := strings.Repeat("é", maxIssueJobErrorBytes)
	text := jobErrorText(long)
	require.LessOrEqual(t, len(text), maxIssueJobErrorBytes+3)
	require.True(t, strings.HasSuffix(text, "..."))
	require.Equal(t, "short", jobErrorText("short"))

	d.Kick()
	d.Kick()
	require.Len(t, d.kick, 1)
	d.refreshGauge()
}

func TestIssueDispatcher_LoopClaimsDeliversAndSweeps(t *testing.T) {
	f := newDispatchFixture(t, IssueDispatchConfig{PollInterval: 10 * time.Millisecond, RetentionSweep: 10 * time.Millisecond, GaugeInterval: 10 * time.Millisecond, MinInterval: time.Nanosecond})
	f.d.now = time.Now
	var paused atomic.Bool
	f.d.SetPause(paused.Load)
	f.d.pauseCheck = 5 * time.Millisecond

	delivered := make(chan struct{})
	var claimed atomic.Bool
	f.store.EXPECT().ClaimIssueJobs(gomock.Any(), gomock.Any(), gomock.Any(), int64(defaultIssueBatchSize)).
		DoAndReturn(func(_ context.Context, now, lease time.Time, _ int64) ([]*domain.IssueTrackerJob, error) {
			require.Equal(t, defaultIssueLease, lease.Sub(now))
			if claimed.CompareAndSwap(false, true) {
				job := issueJob(domain.IssueJobLink, `{}`, 1)
				return []*domain.IssueTrackerJob{&job}, nil
			}
			return nil, errors.New("db down")
		}).AnyTimes()
	f.clients.EXPECT().JobClient(gomock.Any(), snow.ID(1), snow.ID(9)).Return(f.tracker, nil)
	f.store.EXPECT().MarkIssueJobDelivered(gomock.Any(), snow.ID(50), int64(1), gomock.Any()).
		DoAndReturn(func(context.Context, snow.ID, int64, time.Time) (bool, error) {
			close(delivered)
			return true, nil
		})
	var trimmed atomic.Int32
	f.store.EXPECT().SweepIssueJobs(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, time.Time) (int64, error) {
		if trimmed.Add(1) == 1 {
			return 0, errors.New("db down")
		}
		return 2, nil
	}).AnyTimes()
	var counted atomic.Int32
	f.store.EXPECT().CountIssueJobsByState(gomock.Any()).DoAndReturn(func(context.Context) (map[string]int64, error) {
		if counted.Add(1) == 1 {
			return nil, errors.New("db down")
		}
		return map[string]int64{domain.IssueJobPending: 3}, nil
	}).AnyTimes()

	f.d.Start()
	f.d.Start()
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("the job was not delivered")
	}
	require.Eventually(t, func() bool { return trimmed.Load() >= 2 && counted.Load() >= 2 }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, "3", scrapeIssueMetric(t, f.metrics, `nipa_issue_tracker_jobs{state="pending"}`))

	paused.Store(true)
	f.d.Kick()
	f.d.sweep()
	f.d.trim()
	f.d.jobs <- issueJob(domain.IssueJobLink, `{}`, 1)
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, f.d.Stop(ctx))
	require.NoError(t, f.d.Stop(ctx))
}
