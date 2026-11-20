package usecase

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/issuetracker/jira/jiratest"
	"github.com/nipalab/nipa/internal/snow"
)

const readProject = snow.ID(5)

func (f *issueTrackerFixture) project(t *testing.T, active bool) *domain.IssueTracker {
	tracker := f.stored(t, 9, "jira-test-token")
	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(&domain.ProjectIssueTracker{
		ProjectID: readProject, TrackerID: 9, Keys: []string{"ART", "GAME"}, IsActive: active,
	}, nil)
	if active {
		f.repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(tracker, nil)
	}
	return tracker
}

func (f *issueTrackerFixture) reader(ok bool) {
	f.perm.EXPECT().HasProjectAccess(gomock.Any(), readProject, domain.PermissionRead).Return(ok)
}

func TestIssueTracker_SubjectIssueKeys(t *testing.T) {
	f := newIssueTrackerFixture(t)
	f.project(t, true)
	f.repo.EXPECT().ListIssueLinksBySubjects(gomock.Any(), readProject, domain.IssueSubjectCommit, []snow.ID{1, 2}).Return([]domain.IssueLink{
		{SubjectID: 1, IssueKey: "GAME-1"},
		{SubjectID: 1, IssueKey: "OLD-4"},
		{SubjectID: 2, IssueKey: "ART-2"},
	}, nil)
	keys := f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectCommit, []snow.ID{1, 2})
	require.Equal(t, []IssueKeyRef{{Key: "GAME-1", URL: f.jira.URL + "/browse/GAME-1"}}, keys[1])
	require.Equal(t, []IssueKeyRef{{Key: "ART-2", URL: f.jira.URL + "/browse/ART-2"}}, keys[2])
	require.Empty(t, f.jira.Requests())
}

func TestIssueTracker_SubjectIssueKeysSkips(t *testing.T) {
	var nilUC *IssueTracker
	require.Nil(t, nilUC.SubjectIssueKeys(context.Background(), 1, domain.IssueSubjectCommit, []snow.ID{1}))
	require.Nil(t, NewIssueTracker(nil, nil, nil, nil, nil).SubjectIssueKeys(context.Background(), 1, domain.IssueSubjectCommit, []snow.ID{1}))

	f := newIssueTrackerFixture(t)
	require.Nil(t, f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectCommit, nil))

	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(nil, domain.NewErrorNotFound("record not found"))
	require.Nil(t, f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectCommit, []snow.ID{1}))

	f.project(t, false)
	require.Nil(t, f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectCommit, []snow.ID{1}))

	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(nil, errors.New("db down"))
	require.Nil(t, f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectCommit, []snow.ID{1}))

	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(&domain.ProjectIssueTracker{TrackerID: 9, Keys: []string{"GAME"}, IsActive: true}, nil)
	f.repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(nil, domain.NewErrorNotFound("record not found"))
	require.Nil(t, f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectCommit, []snow.ID{1}))

	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(&domain.ProjectIssueTracker{TrackerID: 9, Keys: []string{"GAME"}, IsActive: true}, nil)
	f.repo.EXPECT().GetByID(gomock.Any(), snow.ID(9)).Return(nil, errors.New("db down"))
	require.Nil(t, f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectCommit, []snow.ID{1}))

	f.project(t, true)
	f.repo.EXPECT().ListIssueLinksBySubjects(gomock.Any(), readProject, domain.IssueSubjectBranch, []snow.ID{1}).Return(nil, errors.New("db down"))
	require.Nil(t, f.uc.SubjectIssueKeys(f.ctx, readProject, domain.IssueSubjectBranch, []snow.ID{1}))
}

func TestIssueTracker_ClientRebuiltOnUpdate(t *testing.T) {
	f := newIssueTrackerFixture(t)
	builds := 0
	f.uc.WithFactory(func(kind string, cfg issuetracker.Config) (issuetracker.Tracker, error) {
		builds++
		cfg.Transport = http.DefaultTransport
		return issuetracker.New(kind, cfg)
	})
	tracker := f.stored(t, 9, "jira-test-token")
	for range 2 {
		_, err := f.uc.clients.get(tracker, f.uc.buildClient)
		require.NoError(t, err)
	}
	require.Equal(t, 1, builds)
	updated := *tracker
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Second)
	_, err := f.uc.clients.get(&updated, f.uc.buildClient)
	require.NoError(t, err)
	require.Equal(t, 2, builds)

	broken := updated
	broken.ID = 10
	_, err = f.uc.clients.get(&broken, f.uc.buildClient)
	require.Error(t, err)
}

func TestIssueTracker_ResolveIssues(t *testing.T) {
	f := newIssueTrackerFixture(t)
	f.jira.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "New rig", Status: "In Progress", Assignee: "Ana"})
	f.reader(true)
	f.project(t, true)
	got, err := f.uc.ResolveIssues(f.ctx, readProject, []string{"game-1", "GAME-1", "GAME-2", "OTHER-1", "junk"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "GAME-1", got[0].Key)
	require.Equal(t, IssueStateOK, got[0].State)
	require.Equal(t, "New rig", got[0].Issue.Title)
	require.Equal(t, "In Progress", got[0].Issue.Status)
	require.Equal(t, issuetracker.CategoryInProgress, got[0].Issue.StatusCategory)
	require.Equal(t, f.jira.URL+"/browse/GAME-1", got[0].URL)
	require.Equal(t, IssueStateNotFound, got[1].State)
	require.Nil(t, got[1].Issue)

	requests := len(f.jira.Requests())
	f.reader(true)
	f.project(t, true)
	again, err := f.uc.ResolveIssues(f.ctx, readProject, []string{"GAME-1", "GAME-2"})
	require.NoError(t, err)
	require.Equal(t, IssueStateOK, again[0].State)
	require.Equal(t, IssueStateNotFound, again[1].State)
	require.Len(t, f.jira.Requests(), requests)

	f.now = f.now.Add(2 * issueCacheTTL)
	f.jira.Fail(jiratest.Failure{Status: 503}, jiratest.Failure{Status: 503})
	f.reader(true)
	f.project(t, true)
	down, err := f.uc.ResolveIssues(f.ctx, readProject, []string{"GAME-1", "GAME-2"})
	require.NoError(t, err)
	require.Equal(t, IssueStateUnavailable, down[0].State)
	require.Equal(t, f.jira.URL+"/browse/GAME-1", down[0].URL)
}

func TestIssueTracker_ResolveIssuesRefusals(t *testing.T) {
	f := newIssueTrackerFixture(t)
	_, err := f.uc.ResolveIssues(f.ctx, readProject, make([]string, MaxResolveIssueKeys+1))
	requireDomainCode(t, err, 400)

	f.reader(false)
	_, err = f.uc.ResolveIssues(f.ctx, readProject, []string{"GAME-1"})
	requireDomainCode(t, err, 403)

	f.reader(true)
	f.project(t, false)
	_, err = f.uc.ResolveIssues(f.ctx, readProject, []string{"GAME-1"})
	requireDomainCode(t, err, 404)

	f.reader(true)
	f.repo.EXPECT().GetProject(gomock.Any(), readProject).Return(nil, errors.New("db down"))
	_, err = f.uc.ResolveIssues(f.ctx, readProject, []string{"GAME-1"})
	require.Error(t, err)

	_, err = NewIssueTracker(nil, nil, nil, nil, nil).ResolveIssues(f.ctx, readProject, []string{"GAME-1"})
	requireDomainCode(t, err, 503)
}

func TestIssueTracker_IssueLinks(t *testing.T) {
	f := newIssueTrackerFixture(t)
	f.reader(true)
	f.project(t, true)
	f.repo.EXPECT().ListIssueSubjects(gomock.Any(), readProject, "GAME-1", snow.ID(0), 3).Return([]domain.IssueLinkSubject{
		{IssueLink: domain.IssueLink{ID: 30, SubjectKind: domain.IssueSubjectCommit}, CommitMessage: "Fix GAME-1"},
		{IssueLink: domain.IssueLink{ID: 20, SubjectKind: domain.IssueSubjectBranch}, Missing: true},
		{IssueLink: domain.IssueLink{ID: 10, SubjectKind: domain.IssueSubjectMergeRequest}, MRNumber: 3},
	}, nil)
	page, err := f.uc.IssueLinks(f.ctx, readProject, "game-1", 0, 2)
	require.NoError(t, err)
	require.Equal(t, "GAME-1", page.Key)
	require.Equal(t, f.jira.URL+"/browse/GAME-1", page.URL)
	require.Len(t, page.Links, 1)
	require.Equal(t, snow.ID(20), page.Next)

	f.reader(true)
	f.project(t, true)
	f.repo.EXPECT().ListIssueSubjects(gomock.Any(), readProject, "GAME-1", snow.ID(20), DefaultIssueLinkLimit+1).Return(nil, nil)
	page, err = f.uc.IssueLinks(f.ctx, readProject, "GAME-1", 20, 0)
	require.NoError(t, err)
	require.Empty(t, page.Links)
	require.Zero(t, page.Next)

	f.reader(true)
	f.project(t, true)
	f.repo.EXPECT().ListIssueSubjects(gomock.Any(), readProject, "GAME-1", snow.ID(0), MaxIssueLinkLimit+1).Return(nil, errors.New("db down"))
	_, err = f.uc.IssueLinks(f.ctx, readProject, "GAME-1", 0, 1000)
	require.Error(t, err)

	f.reader(true)
	f.project(t, true)
	_, err = f.uc.IssueLinks(f.ctx, readProject, "OTHER-1", 0, 0)
	requireDomainCode(t, err, 400)

	f.reader(false)
	_, err = f.uc.IssueLinks(f.ctx, readProject, "GAME-1", 0, 0)
	requireDomainCode(t, err, 403)
}

type plainTracker struct{ issuetracker.Tracker }

func TestIssueTracker_SearchIssues(t *testing.T) {
	f := newIssueTrackerFixture(t)
	f.jira.AddIssue(jiratest.Issue{Key: "GAME-1", Summary: "New rig"})
	f.jira.AddIssue(jiratest.Issue{Key: "OTHER-2", Summary: "New rig elsewhere"})
	f.reader(true)
	f.project(t, true)
	res, err := f.uc.SearchIssues(f.ctx, readProject, " rig ", 0)
	require.NoError(t, err)
	require.True(t, res.Supported)
	require.False(t, res.Unavailable)
	require.Len(t, res.Issues, 1)
	require.Equal(t, "GAME-1", res.Issues[0].Key)
	require.Equal(t, "New rig", res.Issues[0].Issue.Title)

	f.jira.Fail(jiratest.Failure{Status: 500})
	f.reader(true)
	f.project(t, true)
	res, err = f.uc.SearchIssues(f.ctx, readProject, "rig", 500)
	require.NoError(t, err)
	require.True(t, res.Unavailable)
	require.Empty(t, res.Issues)

	_, err = f.uc.SearchIssues(f.ctx, readProject, "  ", 0)
	requireDomainCode(t, err, 400)
	_, err = f.uc.SearchIssues(f.ctx, readProject, strings.Repeat("q", maxIssueSearchQuery+1), 0)
	requireDomainCode(t, err, 400)
	f.reader(false)
	_, err = f.uc.SearchIssues(f.ctx, readProject, "rig", 0)
	requireDomainCode(t, err, 403)

	g := newIssueTrackerFixture(t)
	g.uc.WithFactory(func(kind string, cfg issuetracker.Config) (issuetracker.Tracker, error) {
		cfg.Transport = http.DefaultTransport
		inner, err := issuetracker.New(kind, cfg)
		return plainTracker{inner}, err
	})
	g.reader(true)
	g.project(t, true)
	res, err = g.uc.SearchIssues(g.ctx, readProject, "rig", 0)
	require.NoError(t, err)
	require.False(t, res.Supported)
	require.Empty(t, res.Issues)
}

func TestIssueCache_SingleFlightAndEviction(t *testing.T) {
	cache := newIssueCache(time.Minute, 4)
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) (*issuetracker.Issue, error) {
		calls.Add(1)
		<-release
		return &issuetracker.Issue{Key: "GAME-1"}, nil
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			issue, state := cache.get(context.Background(), "k", fetch)
			require.Equal(t, IssueStateOK, state)
			require.Equal(t, "GAME-1", issue.Key)
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())

	nilIssue := func(context.Context) (*issuetracker.Issue, error) { return nil, nil }
	_, state := cache.get(context.Background(), "nil", nilIssue)
	require.Equal(t, IssueStateNotFound, state)

	for _, k := range []string{"a", "b", "c", "d", "e"} {
		cache.store(k, issueCacheEntry{state: IssueStateOK, expires: time.Now().Add(time.Minute)})
	}
	require.LessOrEqual(t, len(cache.entries), 4)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	block := make(chan struct{})
	_, state = cache.get(ctx, "slow", func(context.Context) (*issuetracker.Issue, error) {
		<-block
		return nil, nil
	})
	close(block)
	require.Equal(t, IssueStateUnavailable, state)
}
