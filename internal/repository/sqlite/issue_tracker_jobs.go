package sqlite

import (
	"context"
	"database/sql"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// EnqueueIssueJobs inserts jobs, skipping dedupe keys already queued. A
// remote link has one row whose action is link or unlink: a job that changes
// its action or payload re-arms it. It returns how many rows were written.
func (r *IssueTrackerRepository) EnqueueIssueJobs(ctx context.Context, jobs []domain.IssueTrackerJob) (int64, error) {
	var written int64
	for _, job := range jobs {
		params := sqlcSqlite.IssueJobInsertParams{
			ID:          job.ID.Int64(),
			TrackerID:   job.TrackerID.Int64(),
			ProjectID:   job.ProjectID.Int64(),
			IssueKey:    job.IssueKey,
			Action:      job.Action,
			Payload:     job.Payload,
			DedupeKey:   job.DedupeKey,
			NextRetryAt: timePtrToNullTime(job.NextRetryAt),
			CreatedAt:   job.CreatedAt,
		}
		var n int64
		var err error
		if job.Action == domain.IssueJobLink || job.Action == domain.IssueJobUnlink {
			n, err = r.queries.IssueJobUpsertLink(ctx, sqlcSqlite.IssueJobUpsertLinkParams(params))
		} else {
			n, err = r.queries.IssueJobInsert(ctx, params)
		}
		if err != nil {
			return written, handleError(err)
		}
		written += n
	}
	return written, nil
}

func (r *IssueTrackerRepository) ClaimIssueJobs(ctx context.Context, now, leaseUntil time.Time, limit int64) ([]*domain.IssueTrackerJob, error) {
	rows, err := r.queries.IssueJobClaimDue(ctx, sqlcSqlite.IssueJobClaimDueParams{
		LeaseUntil: sql.NullTime{Time: leaseUntil, Valid: true},
		Now:        sql.NullTime{Time: now, Valid: true},
		RowLimit:   limit,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return issueJobsToDomain(rows), nil
}

func (r *IssueTrackerRepository) MarkIssueJobDelivered(ctx context.Context, id snow.ID, attempt int64, at time.Time) (bool, error) {
	n, err := r.queries.IssueJobMarkDelivered(ctx, sqlcSqlite.IssueJobMarkDeliveredParams{
		DeliveredAt: sql.NullTime{Time: at, Valid: true}, ID: id.Int64(), Attempt: attempt,
	})
	return n > 0, handleError(err)
}

func (r *IssueTrackerRepository) ScheduleIssueJobRetry(ctx context.Context, id snow.ID, attempt int64, lastError string, next time.Time) (bool, error) {
	n, err := r.queries.IssueJobScheduleRetry(ctx, sqlcSqlite.IssueJobScheduleRetryParams{
		LastError: lastError, NextRetryAt: sql.NullTime{Time: next, Valid: true}, ID: id.Int64(), Attempt: attempt,
	})
	return n > 0, handleError(err)
}

func (r *IssueTrackerRepository) MarkIssueJobFailed(ctx context.Context, id snow.ID, attempt int64, lastError string) (bool, error) {
	n, err := r.queries.IssueJobMarkFailed(ctx, sqlcSqlite.IssueJobMarkFailedParams{LastError: lastError, ID: id.Int64(), Attempt: attempt})
	return n > 0, handleError(err)
}

// DeferIssueJob postpones a claimed job without counting the attempt.
func (r *IssueTrackerRepository) DeferIssueJob(ctx context.Context, id snow.ID, attempt int64, next time.Time) (bool, error) {
	n, err := r.queries.IssueJobDefer(ctx, sqlcSqlite.IssueJobDeferParams{
		NextRetryAt: sql.NullTime{Time: next, Valid: true}, ID: id.Int64(), Attempt: attempt,
	})
	return n > 0, handleError(err)
}

// ReleaseIssueJob makes a job that was re-armed while in flight due now.
func (r *IssueTrackerRepository) ReleaseIssueJob(ctx context.Context, id snow.ID, now time.Time) error {
	return handleError(r.queries.IssueJobRelease(ctx, sqlcSqlite.IssueJobReleaseParams{Now: sql.NullTime{Time: now, Valid: true}, ID: id.Int64()}))
}

func (r *IssueTrackerRepository) SweepIssueJobs(ctx context.Context, before time.Time) (int64, error) {
	n, err := r.queries.IssueJobSweep(ctx, before)
	return n, handleError(err)
}

func (r *IssueTrackerRepository) CountIssueJobsByState(ctx context.Context) (map[string]int64, error) {
	rows, err := r.queries.IssueJobCountByState(ctx)
	if err != nil {
		return nil, handleError(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.State] = row.Jobs
	}
	return out, nil
}

func (r *IssueTrackerRepository) ListIssueJobs(ctx context.Context, projectID snow.ID, state string, after *snow.ID, limit int64) ([]*domain.IssueTrackerJob, error) {
	params := sqlcSqlite.IssueJobListParams{ProjectID: projectID.Int64(), RowLimit: limit}
	if state != "" {
		params.State = state
	}
	if after != nil {
		params.After = after.Int64()
	}
	rows, err := r.queries.IssueJobList(ctx, params)
	if err != nil {
		return nil, handleError(err)
	}
	return issueJobsToDomain(rows), nil
}

func (r *IssueTrackerRepository) GetIssueJob(ctx context.Context, id snow.ID) (*domain.IssueTrackerJob, error) {
	row, err := r.queries.IssueJobGet(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return issueJobToDomain(row), nil
}

func (r *IssueTrackerRepository) RetryIssueJob(ctx context.Context, projectID, id snow.ID, now time.Time) (bool, error) {
	n, err := r.queries.IssueJobRetry(ctx, sqlcSqlite.IssueJobRetryParams{
		Now: sql.NullTime{Time: now, Valid: true}, ID: id.Int64(), ProjectID: projectID.Int64(),
	})
	return n > 0, handleError(err)
}

func issueJobsToDomain(rows []sqlcSqlite.IssueTrackerJob) []*domain.IssueTrackerJob {
	out := make([]*domain.IssueTrackerJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, issueJobToDomain(row))
	}
	return out
}

func issueJobToDomain(row sqlcSqlite.IssueTrackerJob) *domain.IssueTrackerJob {
	return &domain.IssueTrackerJob{
		ID:          snow.ID(row.ID),
		TrackerID:   snow.ID(row.TrackerID),
		ProjectID:   snow.ID(row.ProjectID),
		IssueKey:    row.IssueKey,
		Action:      row.Action,
		Payload:     row.Payload,
		DedupeKey:   row.DedupeKey,
		State:       row.State,
		Attempt:     row.Attempt,
		LastError:   row.LastError,
		NextRetryAt: nullTimePtr(row.NextRetryAt),
		DeliveredAt: nullTimePtr(row.DeliveredAt),
		CreatedAt:   row.CreatedAt,
	}
}
