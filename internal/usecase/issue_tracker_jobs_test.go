package usecase

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type kickCounter struct{ kicks int }

func (k *kickCounter) Kick() { k.kicks++ }

func (f *issueTrackerFixture) admin(ok bool) {
	f.perm.EXPECT().AdminHasProject(gomock.Any(), readProject).Return(ok)
}

func TestIssueTracker_ListJobs(t *testing.T) {
	f := newIssueTrackerFixture(t)

	f.admin(false)
	_, _, err := f.uc.ListJobs(f.ctx, readProject, IssueJobListOptions{})
	require.True(t, domain.IsErrorNoPermission(err))

	f.admin(true)
	_, _, err = f.uc.ListJobs(f.ctx, readProject, IssueJobListOptions{State: "sending"})
	require.Error(t, err)

	f.admin(true)
	after := snow.ID(90)
	f.repo.EXPECT().ListIssueJobs(gomock.Any(), readProject, domain.IssueJobFailed, &after, int64(3)).
		Return([]*domain.IssueTrackerJob{{ID: 80}, {ID: 70}, {ID: 60}}, nil)
	jobs, next, err := f.uc.ListJobs(f.ctx, readProject, IssueJobListOptions{State: " failed ", After: &after, Limit: 2})
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	require.Equal(t, snow.ID(70).Base36(), next)

	f.admin(true)
	f.repo.EXPECT().ListIssueJobs(gomock.Any(), readProject, "", nil, int64(maxIssueJobPageSize+1)).Return(nil, nil)
	jobs, next, err = f.uc.ListJobs(f.ctx, readProject, IssueJobListOptions{Limit: 5000})
	require.NoError(t, err)
	require.NotNil(t, jobs)
	require.Empty(t, next)

	f.admin(true)
	f.repo.EXPECT().ListIssueJobs(gomock.Any(), readProject, "", nil, int64(defaultIssueJobPageSize+1)).Return(nil, errors.New("db down"))
	_, _, err = f.uc.ListJobs(f.ctx, readProject, IssueJobListOptions{})
	require.Error(t, err)

	_, _, err = NewIssueTracker(nil, nil, nil, nil, nil).ListJobs(f.ctx, readProject, IssueJobListOptions{})
	require.Equal(t, 503, err.(*domain.Error).Code)
}

func TestIssueTracker_RetryJob(t *testing.T) {
	f := newIssueTrackerFixture(t)
	kick := &kickCounter{}
	f.uc.WithKicker(kick)

	f.admin(false)
	_, err := f.uc.RetryJob(f.ctx, readProject, 1)
	require.True(t, domain.IsErrorNoPermission(err))

	f.admin(true)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(nil, domain.NewErrorNotFound("record not found"))
	_, err = f.uc.RetryJob(f.ctx, readProject, 1)
	require.True(t, domain.IsErrorNotFound(err))

	f.admin(true)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(&domain.IssueTrackerJob{ID: 1, ProjectID: 99, State: domain.IssueJobFailed}, nil)
	_, err = f.uc.RetryJob(f.ctx, readProject, 1)
	require.True(t, domain.IsErrorNotFound(err))

	f.admin(true)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(nil, errors.New("db down"))
	_, err = f.uc.RetryJob(f.ctx, readProject, 1)
	require.EqualError(t, err, "db down")

	f.admin(true)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(&domain.IssueTrackerJob{ID: 1, ProjectID: readProject, State: domain.IssueJobPending}, nil)
	_, err = f.uc.RetryJob(f.ctx, readProject, 1)
	require.Equal(t, 409, err.(*domain.Error).Code)

	f.admin(true)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(&domain.IssueTrackerJob{ID: 1, ProjectID: readProject, State: domain.IssueJobFailed}, nil)
	f.repo.EXPECT().RetryIssueJob(gomock.Any(), readProject, snow.ID(1), f.now).Return(false, nil)
	_, err = f.uc.RetryJob(f.ctx, readProject, 1)
	require.Equal(t, 409, err.(*domain.Error).Code)

	f.admin(true)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(&domain.IssueTrackerJob{ID: 1, ProjectID: readProject, State: domain.IssueJobDelivered}, nil)
	f.repo.EXPECT().RetryIssueJob(gomock.Any(), readProject, snow.ID(1), f.now).Return(false, errors.New("db down"))
	_, err = f.uc.RetryJob(f.ctx, readProject, 1)
	require.Error(t, err)
	require.Zero(t, kick.kicks)

	f.admin(true)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(&domain.IssueTrackerJob{ID: 1, ProjectID: readProject, State: domain.IssueJobFailed}, nil)
	f.repo.EXPECT().RetryIssueJob(gomock.Any(), readProject, snow.ID(1), f.now).Return(true, nil)
	f.repo.EXPECT().GetIssueJob(gomock.Any(), snow.ID(1)).Return(&domain.IssueTrackerJob{ID: 1, ProjectID: readProject, State: domain.IssueJobPending}, nil)
	job, err := f.uc.RetryJob(f.ctx, readProject, 1)
	require.NoError(t, err)
	require.Equal(t, domain.IssueJobPending, job.State)
	require.Equal(t, 1, kick.kicks)
}

func TestIssueTracker_JobClient(t *testing.T) {
	f := newIssueTrackerFixture(t)
	settings := func(active bool, tracker snow.ID) {
		f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(&domain.ProjectIssueTracker{ProjectID: readProject, TrackerID: tracker, Keys: []string{"GAME"}, IsActive: active}, nil)
	}

	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(nil, domain.NewErrorNotFound("record not found"))
	_, err := f.uc.JobClient(f.ctx, readProject, 9)
	require.ErrorIs(t, err, errIssueJobPermanent)

	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(nil, errors.New("db down"))
	_, err = f.uc.JobClient(f.ctx, readProject, 9)
	require.NotErrorIs(t, err, errIssueJobPermanent)

	settings(false, 9)
	_, err = f.uc.JobClient(f.ctx, readProject, 9)
	require.ErrorIs(t, err, errIssueJobPermanent)

	settings(true, 8)
	_, err = f.uc.JobClient(f.ctx, readProject, 9)
	require.ErrorIs(t, err, errIssueJobPermanent)

	settings(true, 9)
	f.repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(nil, domain.NewErrorNotFound("record not found"))
	_, err = f.uc.JobClient(f.ctx, readProject, 9)
	require.ErrorIs(t, err, errIssueJobPermanent)

	settings(true, 9)
	f.repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(nil, errors.New("db down"))
	_, err = f.uc.JobClient(f.ctx, readProject, 9)
	require.NotErrorIs(t, err, errIssueJobPermanent)

	broken := f.stored(t, 9, "jira-test-token")
	broken.SecretSealed = []byte("garbage")
	settings(true, 9)
	f.repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(broken, nil)
	_, err = f.uc.JobClient(f.ctx, readProject, 9)
	require.ErrorIs(t, err, errIssueJobPermanent)

	f.project(t, true)
	client, err := f.uc.JobClient(f.ctx, readProject, 9)
	require.NoError(t, err)
	require.Equal(t, "jira", client.Kind())
}

func TestDescribeIssueJob(t *testing.T) {
	require.Equal(t, IssueJobDetail{Title: "#1 Rig", URL: "https://x/1"},
		DescribeIssueJob(&domain.IssueTrackerJob{Payload: `{"title":"#1 Rig","url":"https://x/1"}`}))
	require.Equal(t, IssueJobDetail{Status: "Done"}, DescribeIssueJob(&domain.IssueTrackerJob{Payload: `{"status":"Done","text":"secret"}`}))
	require.Equal(t, IssueJobDetail{}, DescribeIssueJob(&domain.IssueTrackerJob{Payload: `{`}))
}
