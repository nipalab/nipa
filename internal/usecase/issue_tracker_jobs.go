package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	defaultIssueJobPageSize = 50
	maxIssueJobPageSize     = 200
)

// errIssueJobPermanent marks a job that can never be delivered as stored.
var errIssueJobPermanent = errors.New("issue tracker job cannot be delivered")

// issueTrackerStore is everything the issue tracker usecases read and write.
type issueTrackerStore interface {
	issueTrackerRepository
	issueJobQueue
	EnqueueIssueJobs(ctx context.Context, jobs []domain.IssueTrackerJob) (int64, error)
}

type IssueJobListOptions struct {
	State string
	After *snow.ID
	Limit int64
}

// WithKicker asks the outbox dispatcher to poll right after a retry.
func (u *IssueTracker) WithKicker(kick dispatcherKicker) *IssueTracker {
	u.kick = kick
	return u
}

// ListJobs pages the project's outbox jobs, newest first, with the cursor of
// the next page when one exists (project admin).
func (u *IssueTracker) ListJobs(ctx context.Context, projectID snow.ID, opts IssueJobListOptions) ([]*domain.IssueTrackerJob, string, error) {
	if err := u.requireProjectAdmin(ctx, projectID); err != nil {
		return nil, "", err
	}
	state := strings.TrimSpace(opts.State)
	if state != "" && !domain.IsValidIssueJobState(state) {
		return nil, "", domain.NewErrorUser("state must be pending, delivered or failed")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultIssueJobPageSize
	}
	limit = min(limit, maxIssueJobPageSize)
	jobs, err := u.repo.ListIssueJobs(ctx, projectID, state, opts.After, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if int64(len(jobs)) > limit {
		jobs = jobs[:limit]
		next = jobs[len(jobs)-1].ID.Base36()
	}
	if jobs == nil {
		jobs = []*domain.IssueTrackerJob{}
	}
	return jobs, next, nil
}

// RetryJob queues a delivered or failed job again with a fresh attempt
// count (project admin).
func (u *IssueTracker) RetryJob(ctx context.Context, projectID, id snow.ID) (*domain.IssueTrackerJob, error) {
	if err := u.requireProjectAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	job, err := u.repo.GetIssueJob(ctx, id)
	if domain.IsErrorNotFound(err) || (err == nil && job.ProjectID != projectID) {
		return nil, domain.NewErrorNotFound("issue tracker job not found")
	}
	if err != nil {
		return nil, err
	}
	if job.State == domain.IssueJobPending {
		return nil, domain.NewErrorConflict("the job is pending; only delivered or failed jobs can be retried")
	}
	retried, err := u.repo.RetryIssueJob(ctx, projectID, id, u.now().UTC())
	if err != nil {
		return nil, err
	}
	if !retried {
		return nil, domain.NewErrorConflict("the job can no longer be retried")
	}
	if u.kick != nil {
		u.kick.Kick()
	}
	return u.repo.GetIssueJob(ctx, id)
}

// JobClient returns the tracker client a queued job is sent with. A project
// that no longer uses the job's connection, or a connection whose client
// cannot be built, is a permanent failure.
func (u *IssueTracker) JobClient(ctx context.Context, projectID, trackerID snow.ID) (issuetracker.Tracker, error) {
	inactive := fmt.Errorf("%w: the issue tracker is no longer active for this project", errIssueJobPermanent)
	settings, err := u.repo.GetProject(ctx, projectID)
	if domain.IsErrorNotFound(err) {
		return nil, inactive
	}
	if err != nil {
		return nil, err
	}
	if !settings.IsActive || settings.TrackerID != trackerID {
		return nil, inactive
	}
	tracker, err := u.repo.GetByID(ctx, trackerID)
	if domain.IsErrorNotFound(err) {
		return nil, inactive
	}
	if err != nil {
		return nil, err
	}
	client, err := u.clients.get(tracker, u.buildClient)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errIssueJobPermanent, err)
	}
	return client, nil
}

// IssueJobDetail is the part of a job's payload shown to admins: the link
// title and URL, or the target status. Comment texts stay out of lists.
type IssueJobDetail struct {
	Title  string
	URL    string
	Status string
}

func DescribeIssueJob(job *domain.IssueTrackerJob) IssueJobDetail {
	var p issueJobPayload
	if json.Unmarshal([]byte(job.Payload), &p) != nil {
		return IssueJobDetail{}
	}
	return IssueJobDetail{Title: p.Title, URL: p.URL, Status: p.Status}
}
