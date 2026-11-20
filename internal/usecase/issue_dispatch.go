package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=issue_dispatch_mock_test.go -package=usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/dispatch"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	defaultIssueWorkers        = 2
	defaultIssuePollInterval   = 10 * time.Second
	defaultIssueBatchSize      = 10
	defaultIssueMaxAttempts    = 8
	defaultIssueInitialBackoff = 30 * time.Second
	defaultIssueBackoffFactor  = 3
	defaultIssueMaxBackoff     = 6 * time.Hour
	defaultIssueLease          = 10 * time.Minute
	defaultIssueCallTimeout    = 30 * time.Second
	defaultIssueRetention      = 30 * 24 * time.Hour
	defaultIssueRetentionSweep = time.Hour
	defaultIssueGaugeInterval  = time.Minute
	defaultIssueMinInterval    = 200 * time.Millisecond
	defaultIssueMaxThrottle    = 2 * time.Second
	maxIssueJobErrorBytes      = 1000

	issueRequestOK     = "ok"
	issueRequestRetry  = "retry"
	issueRequestFailed = "failed"
)

// IssueDispatchConfig tunes the issue tracker outbox. Zero values fall back
// to defaults.
type IssueDispatchConfig struct {
	Workers        int
	PollInterval   time.Duration
	BatchSize      int64
	MaxAttempts    int64
	InitialBackoff time.Duration
	BackoffFactor  int
	MaxBackoff     time.Duration
	// Lease is how long a claimed job is hidden from other dispatchers; a
	// crashed attempt is claimed again once it ran out.
	Lease          time.Duration
	CallTimeout    time.Duration
	Retention      time.Duration
	RetentionSweep time.Duration
	GaugeInterval  time.Duration
	// MinInterval spaces the calls to one connection.
	MinInterval time.Duration
	// MaxThrottleWait is the longest a worker waits for its connection's
	// turn; a longer wait postpones the job without spending an attempt.
	MaxThrottleWait time.Duration
}

type issueJobQueue interface {
	ClaimIssueJobs(ctx context.Context, now, leaseUntil time.Time, limit int64) ([]*domain.IssueTrackerJob, error)
	MarkIssueJobDelivered(ctx context.Context, id snow.ID, attempt int64, at time.Time) (bool, error)
	ScheduleIssueJobRetry(ctx context.Context, id snow.ID, attempt int64, lastError string, next time.Time) (bool, error)
	MarkIssueJobFailed(ctx context.Context, id snow.ID, attempt int64, lastError string) (bool, error)
	DeferIssueJob(ctx context.Context, id snow.ID, attempt int64, next time.Time) (bool, error)
	ReleaseIssueJob(ctx context.Context, id snow.ID, now time.Time) error
	SweepIssueJobs(ctx context.Context, before time.Time) (int64, error)
	CountIssueJobsByState(ctx context.Context) (map[string]int64, error)
}

type issueJobClients interface {
	JobClient(ctx context.Context, projectID, trackerID snow.ID) (issuetracker.Tracker, error)
}

// IssueDispatcher sends the queued issue tracker jobs: it claims due rows
// under a lease (safe with several server processes), spaces the calls to
// each connection, retries rate limits and outages with exponential backoff
// and fails everything else at once.
type IssueDispatcher struct {
	store   issueJobQueue
	clients issueJobClients
	cfg     IssueDispatchConfig

	runner     *dispatch.Runner
	jobs       chan domain.IssueTrackerJob
	kick       chan struct{}
	metrics    *obs.Metrics
	pause      dispatch.Pause
	pauseCheck time.Duration
	throttle   *issueThrottle
	now        func() time.Time
}

func NewIssueDispatcher(store issueJobQueue, clients issueJobClients, cfg IssueDispatchConfig) *IssueDispatcher {
	cfg = cfg.withDefaults()
	return &IssueDispatcher{
		store:    store,
		clients:  clients,
		cfg:      cfg,
		runner:   dispatch.NewRunner(),
		jobs:     make(chan domain.IssueTrackerJob, cfg.BatchSize),
		kick:     make(chan struct{}, 1),
		throttle: newIssueThrottle(cfg.MinInterval),
		now:      time.Now,
	}
}

func (d *IssueDispatcher) WithMetrics(metrics *obs.Metrics) *IssueDispatcher {
	d.metrics = metrics
	return d
}

// SetPause holds claiming, retention sweeps and calls while paused reports
// true. A job a worker held during the pause is left to its lease and
// claimed again later. Call it before Start.
func (d *IssueDispatcher) SetPause(paused func() bool) {
	d.pause = paused
}

func (d *IssueDispatcher) Start() {
	d.runner.Start(d.cfg.Workers, d.worker, d.run)
}

func (d *IssueDispatcher) Stop(ctx context.Context) error {
	return d.runner.Stop(ctx)
}

// Kick asks for a poll now. It never blocks.
func (d *IssueDispatcher) Kick() {
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

func (d *IssueDispatcher) run() {
	d.sweep()
	d.refreshGauge()
	poll := time.NewTicker(d.cfg.PollInterval)
	defer poll.Stop()
	retention := time.NewTicker(d.cfg.RetentionSweep)
	defer retention.Stop()
	gauge := time.NewTicker(d.cfg.GaugeInterval)
	defer gauge.Stop()
	for {
		select {
		case <-d.runner.StopCh():
			return
		case <-poll.C:
			d.sweep()
		case <-d.kick:
			d.sweep()
		case <-retention.C:
			d.trim()
		case <-gauge.C:
			d.refreshGauge()
		}
	}
}

func (d *IssueDispatcher) worker() {
	for {
		select {
		case <-d.runner.StopCh():
			return
		case job := <-d.jobs:
			if d.pause.Active() {
				if !d.pause.Wait(d.runner.StopCh(), d.pauseCheck) {
					return
				}
				continue
			}
			d.deliver(job)
			if len(d.jobs) == 0 {
				d.Kick()
			}
		}
	}
}

// sweep claims due jobs only when the workers have drained the previous
// batch, so no claimed job waits in memory long enough to outlive its lease.
func (d *IssueDispatcher) sweep() {
	if d.pause.Active() || len(d.jobs) > 0 {
		return
	}
	now := d.now()
	rows, err := d.store.ClaimIssueJobs(context.Background(), now, now.Add(d.cfg.Lease), d.cfg.BatchSize)
	if err != nil {
		slog.Warn("issue tracker outbox sweep failed", "error", err)
		return
	}
	for _, row := range rows {
		select {
		case d.jobs <- *row:
		case <-d.runner.StopCh():
			return
		}
	}
}

func (d *IssueDispatcher) trim() {
	if d.pause.Active() {
		return
	}
	if _, err := d.store.SweepIssueJobs(context.Background(), d.now().Add(-d.cfg.Retention)); err != nil {
		slog.Warn("issue tracker job retention sweep failed", "error", err)
	}
}

func (d *IssueDispatcher) refreshGauge() {
	if d.metrics == nil {
		return
	}
	counts, err := d.store.CountIssueJobsByState(context.Background())
	if err != nil {
		slog.Warn("counting issue tracker jobs failed", "error", err)
		return
	}
	for _, state := range []string{domain.IssueJobPending, domain.IssueJobDelivered, domain.IssueJobFailed} {
		d.metrics.SetIssueTrackerJobs(state, counts[state])
	}
}

func (d *IssueDispatcher) deliver(job domain.IssueTrackerJob) {
	ctx := context.Background()
	if job.Attempt > d.cfg.MaxAttempts {
		d.fail(ctx, job, fmt.Sprintf("gave up after %d attempts: %s", d.cfg.MaxAttempts, job.LastError))
		return
	}
	client, err := d.clients.JobClient(ctx, job.ProjectID, job.TrackerID)
	if err != nil {
		if errors.Is(err, errIssueJobPermanent) {
			d.fail(ctx, job, err.Error())
		} else {
			d.retry(ctx, job, err, 0)
		}
		return
	}
	if wait := d.throttle.reserve(job.TrackerID, d.now()); wait > 0 {
		if wait > d.cfg.MaxThrottleWait {
			d.postpone(ctx, job, d.now().Add(wait))
			return
		}
		timer := time.NewTimer(wait)
		select {
		case <-d.runner.StopCh():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, d.cfg.CallTimeout)
	err = performIssueJob(callCtx, client, job)
	cancel()
	switch {
	case err == nil:
		d.metrics.ObserveIssueTrackerRequest(client.Kind(), job.Action, issueRequestOK)
		d.settle(ctx, job, func() (bool, error) { return d.store.MarkIssueJobDelivered(ctx, job.ID, job.Attempt, d.now()) })
	case retryableIssueError(err) && job.Attempt < d.cfg.MaxAttempts:
		d.metrics.ObserveIssueTrackerRequest(client.Kind(), job.Action, issueRequestRetry)
		after, _ := issuetracker.RetryAfter(err)
		if after > 0 {
			d.throttle.hold(job.TrackerID, d.now().Add(after))
		}
		d.retry(ctx, job, err, after)
	default:
		d.metrics.ObserveIssueTrackerRequest(client.Kind(), job.Action, issueRequestFailed)
		d.fail(ctx, job, err.Error())
	}
}

func (d *IssueDispatcher) retry(ctx context.Context, job domain.IssueTrackerJob, cause error, after time.Duration) {
	next := d.now().Add(max(d.backoff(job.Attempt), after))
	slog.Warn("issue tracker job failed; scheduled a retry", "job_id", job.ID, "action", job.Action,
		"issue", job.IssueKey, "attempt", job.Attempt, "next_retry_at", next, "error", cause)
	d.settle(ctx, job, func() (bool, error) {
		return d.store.ScheduleIssueJobRetry(ctx, job.ID, job.Attempt, jobErrorText(cause.Error()), next)
	})
}

func (d *IssueDispatcher) postpone(ctx context.Context, job domain.IssueTrackerJob, next time.Time) {
	d.settle(ctx, job, func() (bool, error) { return d.store.DeferIssueJob(ctx, job.ID, job.Attempt, next) })
}

func (d *IssueDispatcher) fail(ctx context.Context, job domain.IssueTrackerJob, reason string) {
	slog.Warn("issue tracker job failed", "job_id", job.ID, "action", job.Action, "issue", job.IssueKey, "attempt", job.Attempt, "error", reason)
	d.settle(ctx, job, func() (bool, error) {
		return d.store.MarkIssueJobFailed(ctx, job.ID, job.Attempt, jobErrorText(reason))
	})
}

// settle records an outcome. When the job changed while it was in flight (a
// newer link payload re-armed it), the outcome is dropped and the job is
// made due at once so the new content is sent.
func (d *IssueDispatcher) settle(ctx context.Context, job domain.IssueTrackerJob, write func() (bool, error)) {
	updated, err := write()
	if err != nil {
		slog.Warn("issue tracker job state update failed", "job_id", job.ID, "error", err)
		return
	}
	if updated {
		return
	}
	if err := d.store.ReleaseIssueJob(ctx, job.ID, d.now()); err != nil {
		slog.Warn("issue tracker job release failed", "job_id", job.ID, "error", err)
		return
	}
	d.Kick()
}

func (d *IssueDispatcher) backoff(attempt int64) time.Duration {
	delay := d.cfg.InitialBackoff
	for i := int64(1); i < attempt && delay < d.cfg.MaxBackoff; i++ {
		delay *= time.Duration(d.cfg.BackoffFactor)
	}
	return min(delay, d.cfg.MaxBackoff)
}

func performIssueJob(ctx context.Context, client issuetracker.Tracker, job domain.IssueTrackerJob) error {
	var p issueJobPayload
	if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
		return fmt.Errorf("%w: invalid payload: %v", errIssueJobPermanent, err)
	}
	switch job.Action {
	case domain.IssueJobLink:
		linker, ok := client.(issuetracker.Linker)
		if !ok {
			return unsupportedIssueJob(client, "links")
		}
		return linker.Link(ctx, job.IssueKey, issuetracker.Link{ID: p.LinkID, URL: p.URL, Title: p.Title, Summary: p.Summary, Resolved: p.Resolved})
	case domain.IssueJobUnlink:
		unlinker, ok := client.(issuetracker.Unlinker)
		if !ok {
			return unsupportedIssueJob(client, "removing links")
		}
		return unlinker.Unlink(ctx, job.IssueKey, p.LinkID)
	case domain.IssueJobComment:
		commenter, ok := client.(issuetracker.Commenter)
		if !ok {
			return unsupportedIssueJob(client, "comments")
		}
		return commenter.Comment(ctx, job.IssueKey, p.Text)
	case domain.IssueJobTransition:
		transitioner, ok := client.(issuetracker.Transitioner)
		if !ok {
			return unsupportedIssueJob(client, "transitions")
		}
		if p.SkipDone {
			issue, err := client.Issue(ctx, job.IssueKey)
			if err != nil {
				return err
			}
			if issue.StatusCategory == issuetracker.CategoryDone && !strings.EqualFold(issue.Status, p.Status) {
				return nil
			}
		}
		return transitioner.TransitionTo(ctx, job.IssueKey, p.Status)
	}
	return fmt.Errorf("%w: unknown action %q", errIssueJobPermanent, job.Action)
}

func unsupportedIssueJob(client issuetracker.Tracker, what string) error {
	return fmt.Errorf("%w: %s trackers do not support %s", errIssueJobPermanent, client.Kind(), what)
}

// retryableIssueError: rate limits, outages and timeouts are retried; not
// found, refused credentials, missing transitions and blocked egress fail.
func retryableIssueError(err error) bool {
	return issuetracker.Retryable(err) || errors.Is(err, context.DeadlineExceeded)
}

func jobErrorText(s string) string {
	if len(s) <= maxIssueJobErrorBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxIssueJobErrorBytes], "") + "..."
}

// issueThrottle spaces the calls to each connection and holds a connection
// back after it asked for a pause.
type issueThrottle struct {
	mu       sync.Mutex
	interval time.Duration
	next     map[snow.ID]time.Time
}

func newIssueThrottle(interval time.Duration) *issueThrottle {
	return &issueThrottle{interval: interval, next: map[snow.ID]time.Time{}}
}

// reserve books the next slot of a connection and returns how long to wait
// for it.
func (t *issueThrottle) reserve(id snow.ID, now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	at := t.next[id]
	if at.Before(now) {
		at = now
	}
	t.next[id] = at.Add(t.interval)
	return at.Sub(now)
}

func (t *issueThrottle) hold(id snow.ID, until time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if until.After(t.next[id]) {
		t.next[id] = until
	}
}

func (cfg IssueDispatchConfig) withDefaults() IssueDispatchConfig {
	if cfg.Workers <= 0 {
		cfg.Workers = defaultIssueWorkers
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultIssuePollInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultIssueBatchSize
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultIssueMaxAttempts
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = defaultIssueInitialBackoff
	}
	if cfg.BackoffFactor < 2 {
		cfg.BackoffFactor = defaultIssueBackoffFactor
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = defaultIssueMaxBackoff
	}
	if cfg.Lease <= 0 {
		cfg.Lease = defaultIssueLease
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = defaultIssueCallTimeout
	}
	if cfg.Retention <= 0 {
		cfg.Retention = defaultIssueRetention
	}
	if cfg.RetentionSweep <= 0 {
		cfg.RetentionSweep = defaultIssueRetentionSweep
	}
	if cfg.GaugeInterval <= 0 {
		cfg.GaugeInterval = defaultIssueGaugeInterval
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = defaultIssueMinInterval
	}
	if cfg.MaxThrottleWait <= 0 {
		cfg.MaxThrottleWait = defaultIssueMaxThrottle
	}
	return cfg
}
