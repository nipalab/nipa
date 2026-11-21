package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubProjects struct{ err error }

func (s stubProjects) Get(context.Context, snow.ID) (*domain.Project, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Project{ID: 1, OrgID: 2, Slug: "game"}, nil
}

type stubOrgs struct{ err error }

func (s stubOrgs) GetByID(context.Context, snow.ID) (*domain.Organization, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &domain.Organization{ID: 2, Slug: "studio"}, nil
}

type stubUsers struct{}

func (stubUsers) GetByID(_ context.Context, id snow.ID) (*domain.User, error) {
	if id == 7 {
		return &domain.User{ID: 7, Name: "Ana"}, nil
	}
	return nil, domain.NewErrorNotFound("user not found")
}

type stubCommitLog struct {
	commits []*domain.CommitLogEntry
	err     error
	start   snow.ID
	stop    snow.ID
}

func (s *stubCommitLog) CommitLogUntil(_ context.Context, _ snow.ID, start, stop snow.ID, _ int) ([]*domain.CommitLogEntry, error) {
	s.start, s.stop = start, stop
	return s.commits, s.err
}

type queuedJob struct {
	action, key, dedupe string
	payload             issueJobPayload
}

func newJobSyncFixture(t *testing.T, baseURL string) (*IssueSync, *MockissueLinkStore, *kickCounter, *stubCommitLog) {
	sync, store, _ := newIssueSyncFixture(t)
	commits := &stubCommitLog{}
	kick := &kickCounter{}
	sync.WithJobs(stubProjects{}, stubOrgs{}, stubUsers{}, commits, baseURL).WithKicker(kick)
	return sync, store, kick, commits
}

func expectJobs(store *MockissueLinkStore, out *[]queuedJob) {
	store.EXPECT().EnqueueIssueJobs(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, jobs []domain.IssueTrackerJob) (int64, error) {
		for _, job := range jobs {
			var p issueJobPayload
			if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
				return 0, err
			}
			if job.TrackerID != 9 || job.ProjectID != 1 || job.State != domain.IssueJobPending || job.NextRetryAt == nil || job.ID == 0 {
				return 0, errors.New("bad job row")
			}
			*out = append(*out, queuedJob{job.Action, job.IssueKey, job.DedupeKey, p})
		}
		return int64(len(jobs)), nil
	})
}

func settingsWith(mod func(*domain.ProjectIssueTracker)) *domain.ProjectIssueTracker {
	s := activeSettings()
	s.LinkMergeRequests = true
	s.CommentOnMerge = true
	mod(s)
	return s
}

func TestIssueSync_PushQueuesCommitLinks(t *testing.T) {
	ctx := context.Background()
	push := &PushEvent{ProjectID: 1, AfterID: 11, Message: "Fixes GAME-1\n\nbody", Branch: &domain.Branch{ID: 5, Name: "main", IsDefault: true}}
	ev := IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: push}

	sync, store, kick, _ := newJobSyncFixture(t, "https://nipa.example/")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkCommits = domain.LinkCommitsDefaultBranch }), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	var jobs []queuedJob
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, ev))
	require.Equal(t, []queuedJob{{
		action: "link", key: "GAME-1", dedupe: "link:GAME-1:nipa:studio/game:commit:b",
		payload: issueJobPayload{
			LinkID: "nipa:studio/game:commit:b", URL: "https://nipa.example/studio/game/commits/b",
			Title: "Fixes GAME-1", Summary: "studio/game · main",
		},
	}}, jobs)
	require.Equal(t, 1, kick.kicks)

	feature := *push
	feature.Branch = &domain.Branch{ID: 6, Name: "feature"}
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkCommits = domain.LinkCommitsDefaultBranch }), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: &feature}))

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkCommits = domain.LinkCommitsOff }), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	require.NoError(t, sync.Handle(ctx, ev))

	jobs = nil
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkCommits = domain.LinkCommitsAll }), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	expectJobs(store, &jobs)
	untitled := feature
	untitled.Message = "GAME-1"
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: &untitled}))
	require.Len(t, jobs, 1)
	require.Equal(t, "GAME-1", jobs[0].payload.Title)
	require.Equal(t, 2, kick.kicks)

	imported := *push
	imported.Imported = true
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkCommits = domain.LinkCommitsAll }), nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: &imported}))
}

func TestIssueSync_PushWithoutBaseURLOrScope(t *testing.T) {
	ctx := context.Background()
	push := &PushEvent{ProjectID: 1, AfterID: 11, Message: "GAME-1", Branch: &domain.Branch{ID: 5, Name: "main", IsDefault: true}}
	all := settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkCommits = domain.LinkCommitsAll })

	sync, store, kick, _ := newJobSyncFixture(t, "")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(all, nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: push}))
	require.Zero(t, kick.kicks)

	sync.WithJobs(stubProjects{err: errors.New("db down")}, stubOrgs{}, stubUsers{}, nil, "https://nipa.example")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(all, nil)
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: push}))

	sync.WithJobs(stubProjects{}, stubOrgs{err: errors.New("db down")}, stubUsers{}, nil, "https://nipa.example")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(all, nil)
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: push}))

	sync.WithJobs(stubProjects{}, stubOrgs{}, stubUsers{}, nil, "https://nipa.example")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(all, nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	store.EXPECT().EnqueueIssueJobs(ctx, gomock.Any()).Return(int64(0), errors.New("insert failed"))
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: push}))
	require.Zero(t, kick.kicks)

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(all, nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(errors.New("insert failed"))
	branchPush := *push
	branchPush.Branch = &domain.Branch{ID: 6, Name: "GAME-2-x"}
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: &branchPush}))
}

func TestIssueSync_MergeRequestQueuesLinksAndOpenedTransitions(t *testing.T) {
	ctx := context.Background()
	sync, store, kick, _ := newJobSyncFixture(t, "https://nipa.example")
	settings := settingsWith(func(s *domain.ProjectIssueTracker) { s.OpenedStatus = "In Review" })
	mr := &domain.MergeRequest{ID: 40, Number: 12, Title: "Rig for GAME-2", Description: "Closes GAME-3", SourceBranch: "GAME-5-rig", Status: domain.MergeRequestOpen}

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(nil, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), nil).Return(int64(0), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil).Times(3)
	var jobs []queuedJob
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: mr}))

	link := issueJobPayload{LinkID: "nipa:studio/game:mr:12", URL: "https://nipa.example/studio/game/merges/12", Title: "#12 Rig for GAME-2", Summary: "Merge request in studio/game · open"}
	require.Equal(t, []queuedJob{
		{"link", "GAME-2", "link:GAME-2:nipa:studio/game:mr:12", link},
		{"link", "GAME-3", "link:GAME-3:nipa:studio/game:mr:12", link},
		{"link", "GAME-5", "link:GAME-5:nipa:studio/game:mr:12", link},
		{"transition", "GAME-3", "transition:GAME-3:nipa:studio/game:mr:12:opened:In Review", issueJobPayload{Status: "In Review", SkipDone: true}},
		{"transition", "GAME-5", "transition:GAME-5:nipa:studio/game:mr:12:opened:In Review", issueJobPayload{Status: "In Review", SkipDone: true}},
	}, jobs)
	require.Equal(t, 1, kick.kicks)

	jobs = nil
	edited := *mr
	edited.Description = "Closes GAME-3, fixes GAME-6"
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return([]domain.IssueLink{
		{IssueKey: "GAME-2", Relation: "mentions"}, {IssueKey: "GAME-3", Relation: "closes"}, {IssueKey: "GAME-5", Relation: "mentions"},
	}, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), nil).Return(int64(0), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil)
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRUpdated, ProjectID: 1, MR: &edited}))
	require.Len(t, jobs, 5)
	require.Equal(t, queuedJob{"transition", "GAME-6", "transition:GAME-6:nipa:studio/game:mr:12:opened:In Review", issueJobPayload{Status: "In Review", SkipDone: true}}, jobs[4])

	jobs = nil
	quiet := settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkMergeRequests = false; s.OpenedStatus = "In Review" })
	closed := *mr
	closed.Status = domain.MergeRequestClosed
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(quiet, nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(nil, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), nil).Return(int64(0), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil).Times(3)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: &closed}))
	require.Empty(t, jobs)

	sync.WithJobs(stubProjects{err: errors.New("db down")}, stubOrgs{}, stubUsers{}, nil, "https://nipa.example")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: mr}))
}

func TestIssueSync_MergedQueuesLinksCommentAndTransitions(t *testing.T) {
	ctx := context.Background()
	sync, store, kick, commits := newJobSyncFixture(t, "https://nipa.example")
	settings := settingsWith(func(s *domain.ProjectIssueTracker) { s.MergedStatus = "Done" })
	source, base := snow.ID(300), snow.ID(200)
	mr := &domain.MergeRequest{
		ID: 40, Number: 12, Title: "Rig for GAME-2", Description: "Closes GAME-3", SourceBranch: "GAME-5-rig",
		Status: domain.MergeRequestMerged, MergedSourceCommitID: &source, MergeBaseCommitID: &base,
	}
	commits.commits = []*domain.CommitLogEntry{
		{Commit: domain.Commit{Message: "Fixes GAME-7"}},
		{Commit: domain.Commit{Message: "Fixes GAME-8", Origin: "git:default:abc"}},
		{Commit: domain.Commit{Message: "fixes OTHER-1, GAME-3"}},
	}
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return([]domain.IssueLink{
		{IssueKey: "GAME-2"}, {IssueKey: "GAME-3"}, {IssueKey: "GAME-5"}, {IssueKey: "OLD-1"},
	}, nil)
	var jobs []queuedJob
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRMerged, ProjectID: 1, Actor: 7, MR: mr}))
	require.Equal(t, source, commits.start)
	require.Equal(t, base, commits.stop)

	link := issueJobPayload{LinkID: "nipa:studio/game:mr:12", URL: "https://nipa.example/studio/game/merges/12", Title: "#12 Rig for GAME-2", Summary: "Merge request in studio/game · merged", Resolved: true}
	comment := issueJobPayload{Text: "Ana merged merge request #12 \"Rig for GAME-2\" in studio/game.\nhttps://nipa.example/studio/game/merges/12"}
	byAction := map[string][]string{}
	for _, job := range jobs {
		byAction[job.action] = append(byAction[job.action], job.key)
		switch job.action {
		case "link":
			require.Equal(t, link, job.payload)
		case "comment":
			require.Equal(t, comment, job.payload)
			require.Equal(t, "comment:"+job.key+":nipa:studio/game:mr:12:merged", job.dedupe)
		case "transition":
			require.Equal(t, issueJobPayload{Status: "Done"}, job.payload)
			require.Equal(t, "transition:"+job.key+":nipa:studio/game:mr:12:merged:Done", job.dedupe)
		}
	}
	require.Equal(t, []string{"GAME-2", "GAME-3", "GAME-5", "GAME-7"}, byAction["link"])
	require.Equal(t, []string{"GAME-2", "GAME-3", "GAME-5", "GAME-7"}, byAction["comment"])
	require.Equal(t, []string{"GAME-3", "GAME-7"}, byAction["transition"])
	require.Equal(t, 1, kick.kicks)

	jobs = nil
	sync.WithJobs(stubProjects{}, stubOrgs{}, stubUsers{}, &stubCommitLog{err: errors.New("db down")}, "")
	noLinks := settingsWith(func(s *domain.ProjectIssueTracker) { s.MergedStatus = "Done" })
	anonymous := *mr
	anonymous.MergeBaseCommitID = nil
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(noLinks, nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(nil, nil)
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRMerged, ProjectID: 1, Actor: 99, MR: &anonymous}))
	require.Equal(t, []queuedJob{
		{"comment", "GAME-3", "comment:GAME-3:nipa:studio/game:mr:12:merged", issueJobPayload{Text: "Someone merged merge request #12 \"Rig for GAME-2\" in studio/game."}},
		{"transition", "GAME-3", "transition:GAME-3:nipa:studio/game:mr:12:merged:Done", issueJobPayload{Status: "Done"}},
	}, jobs)
}

func TestIssueSync_ClosedAndReopenedRefreshLinks(t *testing.T) {
	ctx := context.Background()
	sync, store, kick, _ := newJobSyncFixture(t, "https://nipa.example")
	mr := &domain.MergeRequest{ID: 40, Number: 12, Title: "Fixes GAME-2", Status: domain.MergeRequestClosed}

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(*domain.ProjectIssueTracker) {}), nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return([]domain.IssueLink{{IssueKey: "GAME-2"}}, nil)
	var jobs []queuedJob
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRClosed, ProjectID: 1, MR: mr}))
	require.Len(t, jobs, 1)
	require.True(t, jobs[0].payload.Resolved)

	reopened := *mr
	reopened.Status = domain.MergeRequestOpen
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(*domain.ProjectIssueTracker) {}), nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return([]domain.IssueLink{{IssueKey: "GAME-2"}}, nil)
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRReopened, ProjectID: 1, MR: &reopened}))
	require.False(t, jobs[1].payload.Resolved)
	require.Equal(t, 2, kick.kicks)

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkMergeRequests = false }), nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return([]domain.IssueLink{{IssueKey: "GAME-2"}}, nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRReopened, ProjectID: 1, MR: &reopened}))

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(nil, errors.New("db down"))
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRClosed, ProjectID: 1, MR: mr}))

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRClosed, ProjectID: 1}))

	sync.WithJobs(stubProjects{err: errors.New("db down")}, stubOrgs{}, stubUsers{}, nil, "https://nipa.example")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil)
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRClosed, ProjectID: 1, MR: mr}))
}

func TestTruncateLinkTitle(t *testing.T) {
	require.Equal(t, "Commit x", firstLine("  Commit x \nbody"))
	require.Empty(t, firstLine(""))
}

func TestIssueSync_EditRemovingKeyQueuesUnlink(t *testing.T) {
	ctx := context.Background()
	sync, store, kick, _ := newJobSyncFixture(t, "https://nipa.example")
	mr := &domain.MergeRequest{ID: 40, Number: 12, Title: "Rig for GAME-2", Status: domain.MergeRequestOpen}
	linked := []domain.IssueLink{{IssueKey: "GAME-2", Relation: "mentions"}, {IssueKey: "GAME-3", Relation: "mentions"}}

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(*domain.ProjectIssueTracker) {}), nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(linked, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), []string{"GAME-3"}).Return(int64(1), nil)
	var jobs []queuedJob
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRUpdated, ProjectID: 1, MR: mr}))
	require.Len(t, jobs, 2)
	require.Equal(t, "link:GAME-2:nipa:studio/game:mr:12", jobs[0].dedupe)
	require.Equal(t, queuedJob{"unlink", "GAME-3", "link:GAME-3:nipa:studio/game:mr:12", issueJobPayload{LinkID: "nipa:studio/game:mr:12"}}, jobs[1])
	require.Equal(t, 1, kick.kicks)

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkMergeRequests = false }), nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(linked, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), []string{"GAME-3"}).Return(int64(1), nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRUpdated, ProjectID: 1, MR: mr}))
	require.Equal(t, 1, kick.kicks, "no unlink without link_merge_requests")
}

func TestIssueSync_DraftWaitsForReady(t *testing.T) {
	ctx := context.Background()
	sync, store, kick, _ := newJobSyncFixture(t, "https://nipa.example")
	settings := settingsWith(func(s *domain.ProjectIssueTracker) { s.LinkMergeRequests = false; s.OpenedStatus = "In Review" })
	draft := &domain.MergeRequest{ID: 40, Number: 12, Title: "Rig, see GAME-2", Description: "Fixes GAME-3", SourceBranch: "GAME-5-rig", Status: domain.MergeRequestOpen, Draft: true}

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(nil, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), nil).Return(int64(0), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(nil).Times(3)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: draft}))
	require.Zero(t, kick.kicks, "a draft moves no issue")

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRReady, ProjectID: 1, MR: draft}))

	ready := *draft
	ready.Draft = false
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	var jobs []queuedJob
	expectJobs(store, &jobs)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRReady, ProjectID: 1, MR: &ready}))
	require.Equal(t, []queuedJob{
		{"transition", "GAME-3", "transition:GAME-3:nipa:studio/game:mr:12:opened:In Review", issueJobPayload{Status: "In Review", SkipDone: true}},
		{"transition", "GAME-5", "transition:GAME-5:nipa:studio/game:mr:12:opened:In Review", issueJobPayload{Status: "In Review", SkipDone: true}},
	}, jobs)
	require.Equal(t, 1, kick.kicks)

	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settingsWith(func(*domain.ProjectIssueTracker) {}), nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRReady, ProjectID: 1, MR: &ready}))
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRReady, ProjectID: 1}))

	sync.WithJobs(stubProjects{err: errors.New("db down")}, stubOrgs{}, stubUsers{}, nil, "https://nipa.example")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(settings, nil)
	require.Error(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRReady, ProjectID: 1, MR: &ready}))
}
