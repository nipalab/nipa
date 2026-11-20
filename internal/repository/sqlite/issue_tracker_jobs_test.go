package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func TestIssueTrackerRepositorySQLite_Jobs(t *testing.T) {
	ctx := context.Background()
	db, q := newMaintenanceTestDB(t)
	repo := NewIssueTrackerRepository(db)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	orgID := seedOrg(t, q, "Studio", "studio")
	game := seedProject(t, q, orgID.Int64(), "game")
	art := seedProject(t, q, orgID.Int64(), "art")
	_, err := repo.Create(ctx, domain.IssueTracker{ID: 1, OrgID: orgID, Kind: "jira", Name: "Jira", BaseURL: "https://x", AuthKind: "bearer", SecretSealed: []byte{1}, CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)

	job := func(id int64, action, dedupe, payload string) domain.IssueTrackerJob {
		return domain.IssueTrackerJob{ID: snow.ID(id), TrackerID: 1, ProjectID: game, IssueKey: "GAME-1", Action: action, Payload: payload, DedupeKey: dedupe, NextRetryAt: &now, CreatedAt: now}
	}
	link := job(1, domain.IssueJobLink, "link:GAME-1:mr", `{"title":"a"}`)
	comment := job(2, domain.IssueJobComment, "comment:GAME-1:mr", `{"text":"x"}`)
	transition := job(3, domain.IssueJobTransition, "transition:GAME-1:mr", `{"status":"Done"}`)
	n, err := repo.EnqueueIssueJobs(ctx, []domain.IssueTrackerJob{link, comment, transition})
	require.NoError(t, err)
	require.Equal(t, int64(3), n)
	again := []domain.IssueTrackerJob{job(4, domain.IssueJobLink, "link:GAME-1:mr", `{"title":"a"}`), job(5, domain.IssueJobComment, "comment:GAME-1:mr", `{"text":"y"}`)}
	n, err = repo.EnqueueIssueJobs(ctx, again)
	require.NoError(t, err)
	require.Zero(t, n, "same dedupe keys queue nothing")

	lease := now.Add(10 * time.Minute)
	claimed, err := repo.ClaimIssueJobs(ctx, now, lease, 2)
	require.NoError(t, err)
	require.Len(t, claimed, 2)
	require.Equal(t, snow.ID(1), claimed[0].ID)
	require.Equal(t, int64(1), claimed[0].Attempt)
	require.True(t, claimed[0].NextRetryAt.Equal(lease))
	claimed, err = repo.ClaimIssueJobs(ctx, now, lease, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, snow.ID(3), claimed[0].ID)
	claimed, err = repo.ClaimIssueJobs(ctx, now, lease, 10)
	require.NoError(t, err)
	require.Empty(t, claimed, "leased jobs are hidden")

	n, err = repo.EnqueueIssueJobs(ctx, []domain.IssueTrackerJob{job(6, domain.IssueJobLink, "link:GAME-1:mr", `{"title":"b"}`)})
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "a changed link re-arms its job")
	rearmed, err := repo.GetIssueJob(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, `{"title":"b"}`, rearmed.Payload)
	require.Zero(t, rearmed.Attempt)
	require.True(t, rearmed.NextRetryAt.Equal(lease), "an in-flight lease is kept")
	ok, err := repo.MarkIssueJobDelivered(ctx, 1, 1, now)
	require.NoError(t, err)
	require.False(t, ok, "the stale attempt cannot settle the re-armed job")
	require.NoError(t, repo.ReleaseIssueJob(ctx, 1, now))
	claimed, err = repo.ClaimIssueJobs(ctx, now, lease, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, `{"title":"b"}`, claimed[0].Payload)

	ok, err = repo.MarkIssueJobDelivered(ctx, 1, 1, now)
	require.NoError(t, err)
	require.True(t, ok)
	delivered, err := repo.GetIssueJob(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, domain.IssueJobDelivered, delivered.State)
	require.Nil(t, delivered.NextRetryAt)
	require.True(t, delivered.DeliveredAt.Equal(now))

	ok, err = repo.ScheduleIssueJobRetry(ctx, 2, 1, "rate limited", now.Add(time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
	claimed, err = repo.ClaimIssueJobs(ctx, now, lease, 10)
	require.NoError(t, err)
	require.Empty(t, claimed)
	claimed, err = repo.ClaimIssueJobs(ctx, now.Add(2*time.Minute), lease, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, int64(2), claimed[0].Attempt)
	require.Equal(t, "rate limited", claimed[0].LastError)
	ok, err = repo.DeferIssueJob(ctx, 2, 2, now)
	require.NoError(t, err)
	require.True(t, ok)
	deferred, err := repo.GetIssueJob(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, int64(1), deferred.Attempt)

	ok, err = repo.MarkIssueJobFailed(ctx, 3, 1, "not found")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.RetryIssueJob(ctx, art, 3, now)
	require.NoError(t, err)
	require.False(t, ok)
	counts, err := repo.CountIssueJobsByState(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]int64{"pending": 1, "delivered": 1, "failed": 1}, counts)

	list, err := repo.ListIssueJobs(ctx, game, "", nil, 10)
	require.NoError(t, err)
	require.Len(t, list, 3)
	require.Equal(t, snow.ID(3), list[0].ID)
	after := snow.ID(3)
	list, err = repo.ListIssueJobs(ctx, game, domain.IssueJobDelivered, &after, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, snow.ID(1), list[0].ID)
	list, err = repo.ListIssueJobs(ctx, art, "", nil, 10)
	require.NoError(t, err)
	require.Empty(t, list)

	ok, err = repo.RetryIssueJob(ctx, game, 3, now)
	require.NoError(t, err)
	require.True(t, ok)
	retried, err := repo.GetIssueJob(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, domain.IssueJobPending, retried.State)
	require.Zero(t, retried.Attempt)
	require.Empty(t, retried.LastError)
	ok, err = repo.RetryIssueJob(ctx, game, 3, now)
	require.NoError(t, err)
	require.False(t, ok, "pending jobs cannot be retried")

	swept, err := repo.SweepIssueJobs(ctx, now.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, int64(1), swept)
	_, err = repo.GetIssueJob(ctx, 1)
	requireRecordNotFound(t, err)

	_, err = repo.EnqueueIssueJobs(ctx, []domain.IssueTrackerJob{job(7, "bogus", "x", "{}")})
	require.Error(t, err)
	_, err = repo.Delete(ctx, orgID, 1)
	require.NoError(t, err)
	require.Zero(t, countRows(t, db, "issue_tracker_jobs"), "deleting the connection drops its jobs")
}

func TestIssueTrackerRepositorySQLite_UnlinkSupersedesLink(t *testing.T) {
	ctx := context.Background()
	db, q := newMaintenanceTestDB(t)
	repo := NewIssueTrackerRepository(db)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	orgID := seedOrg(t, q, "Studio", "studio")
	game := seedProject(t, q, orgID.Int64(), "game")
	_, err := repo.Create(ctx, domain.IssueTracker{ID: 1, OrgID: orgID, Kind: "jira", Name: "Jira", BaseURL: "https://x", AuthKind: "bearer", SecretSealed: []byte{1}, CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)
	job := func(id int64, action, payload string) domain.IssueTrackerJob {
		return domain.IssueTrackerJob{ID: snow.ID(id), TrackerID: 1, ProjectID: game, IssueKey: "GAME-1", Action: action, Payload: payload, DedupeKey: "link:GAME-1:mr", NextRetryAt: &now, CreatedAt: now}
	}

	for i, step := range []struct{ action, payload string }{
		{domain.IssueJobLink, `{"link_id":"mr","title":"a"}`},
		{domain.IssueJobUnlink, `{"link_id":"mr"}`},
		{domain.IssueJobLink, `{"link_id":"mr","title":"a"}`},
	} {
		n, err := repo.EnqueueIssueJobs(ctx, []domain.IssueTrackerJob{job(int64(i+1), step.action, step.payload)})
		require.NoError(t, err)
		require.Equal(t, int64(1), n)
		got, err := repo.GetIssueJob(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, step.action, got.Action)
		require.Equal(t, domain.IssueJobPending, got.State)
	}
	claimed, err := repo.ClaimIssueJobs(ctx, now, now.Add(time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "one row per remote link")
	ok, err := repo.MarkIssueJobDelivered(ctx, 1, 1, now)
	require.NoError(t, err)
	require.True(t, ok)
	n, err := repo.EnqueueIssueJobs(ctx, []domain.IssueTrackerJob{job(9, domain.IssueJobUnlink, `{"link_id":"mr"}`)})
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "a delivered link is re-armed as an unlink")
}
