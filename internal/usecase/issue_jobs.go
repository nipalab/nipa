package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	maxIssueLinkTitleRunes = 255
	issueTransitionOpened  = "opened"
	issueTransitionMerged  = "merged"
)

// issueJobPayload is what a queued job hands the tracker: the remote link of
// a link job, the text of a comment, the target status of a transition.
// Only titles and URLs leave Nipa, never file names or diffs.
type issueJobPayload struct {
	LinkID   string `json:"link_id,omitempty"`
	URL      string `json:"url,omitempty"`
	Title    string `json:"title,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Resolved bool   `json:"resolved,omitempty"`
	Text     string `json:"text,omitempty"`
	Status   string `json:"status,omitempty"`
	SkipDone bool   `json:"skip_done,omitempty"`
}

type issueCommitLog interface {
	CommitLogUntil(ctx context.Context, projectID snow.ID, startCommitID, stopCommitID snow.ID, limit int) ([]*domain.CommitLogEntry, error)
}

type issueJobSources struct {
	projects hookProjectLookup
	orgs     hookOrgLookup
	users    userLookup
	commits  issueCommitLog
	baseURL  string
}

// issueScope names a project in link ids and URLs.
type issueScope struct {
	org, project string
}

// WithJobs queues tracker jobs next to the link rows. baseURL is the public
// SPA address the remote links point at; without it no link job is queued.
func (s *IssueSync) WithJobs(projects hookProjectLookup, orgs hookOrgLookup, users userLookup, commits issueCommitLog, baseURL string) *IssueSync {
	s.jobs = &issueJobSources{
		projects: projects,
		orgs:     orgs,
		users:    users,
		commits:  commits,
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
	return s
}

// WithKicker asks the dispatcher to poll right after jobs were queued.
func (s *IssueSync) WithKicker(kick dispatcherKicker) *IssueSync {
	s.kick = kick
	return s
}

func (s *IssueSync) jobScope(ctx context.Context, projectID snow.ID) (*issueScope, error) {
	if s.jobs == nil {
		return nil, nil
	}
	project, err := s.jobs.projects.Get(ctx, projectID)
	if err != nil {
		return nil, err
	}
	org, err := s.jobs.orgs.GetByID(ctx, project.OrgID)
	if err != nil {
		return nil, err
	}
	return &issueScope{org: org.Slug, project: project.Slug}, nil
}

func (s *IssueSync) linksEnabled(scope *issueScope) bool {
	return scope != nil && s.jobs.baseURL != ""
}

func (sc *issueScope) mergeRequestLinkID(number int64) string {
	return fmt.Sprintf("nipa:%s/%s:mr:%d", sc.org, sc.project, number)
}

func (sc *issueScope) commitLinkID(id snow.ID) string {
	return fmt.Sprintf("nipa:%s/%s:commit:%s", sc.org, sc.project, id.Base36())
}

func (s *IssueSync) mergeRequestURL(sc *issueScope, number int64) string {
	return fmt.Sprintf("%s/%s/%s/merges/%d", s.jobs.baseURL, sc.org, sc.project, number)
}

// commitJobs queues one link per key of a pushed commit, as link_commits
// allows: only pushes to the default branch, or every branch.
func (s *IssueSync) commitJobs(ctx context.Context, settings *domain.ProjectIssueTracker, push *PushEvent, refs []issuetracker.Ref) ([]domain.IssueTrackerJob, error) {
	if s.jobs == nil || len(refs) == 0 {
		return nil, nil
	}
	switch settings.LinkCommits {
	case domain.LinkCommitsAll:
	case domain.LinkCommitsDefaultBranch:
		if push.Branch == nil || !push.Branch.IsDefault {
			return nil, nil
		}
	default:
		return nil, nil
	}
	scope, err := s.jobScope(ctx, settings.ProjectID)
	if err != nil || !s.linksEnabled(scope) {
		return nil, err
	}
	title := firstLine(push.Message)
	if title == "" {
		title = "Commit " + push.AfterID.Base36()
	}
	payload := issueJobPayload{
		LinkID: scope.commitLinkID(push.AfterID),
		URL:    fmt.Sprintf("%s/%s/%s/commits/%s", s.jobs.baseURL, scope.org, scope.project, push.AfterID.Base36()),
		Title:  truncateRunes(title, maxIssueLinkTitleRunes),
	}
	if push.Branch != nil {
		payload.Summary = fmt.Sprintf("%s/%s · %s", scope.org, scope.project, push.Branch.Name)
	}
	jobs := make([]domain.IssueTrackerJob, 0, len(refs))
	for _, ref := range refs {
		jobs = append(jobs, s.linkJob(settings, ref.Key, payload))
	}
	return jobs, nil
}

// mergeRequestJobs queues the request's remote link for every key it carries
// (an upsert, re-sent only when its content changed), removes the remote link
// of the keys an edit dropped, and moves the keys it just gained to
// opened_status.
func (s *IssueSync) mergeRequestJobs(settings *domain.ProjectIssueTracker, scope *issueScope, mr *domain.MergeRequest, want, added map[string]issuetracker.Ref, removed []string) []domain.IssueTrackerJob {
	if scope == nil {
		return nil
	}
	var jobs []domain.IssueTrackerJob
	if settings.LinkMergeRequests {
		if s.linksEnabled(scope) {
			payload := s.mergeRequestLink(scope, mr)
			for _, ref := range orderedRefs(want) {
				jobs = append(jobs, s.linkJob(settings, ref.Key, payload))
			}
		}
		linkID := scope.mergeRequestLinkID(mr.Number)
		for _, key := range removed {
			jobs = append(jobs, s.newJob(settings, key, domain.IssueJobUnlink, linkDedupeKey(key, linkID), issueJobPayload{LinkID: linkID}))
		}
	}
	return append(jobs, s.openedTransitions(settings, scope, mr, added)...)
}

// openedTransitions moves the issues an open, non-draft request works on to
// opened_status: closing refs and the source branch key, never a plain
// mention. The dedupe key makes it fire once per request and issue.
func (s *IssueSync) openedTransitions(settings *domain.ProjectIssueTracker, scope *issueScope, mr *domain.MergeRequest, refs map[string]issuetracker.Ref) []domain.IssueTrackerJob {
	if settings.OpenedStatus == "" || mr.Status != domain.MergeRequestOpen || mr.Draft {
		return nil
	}
	branchKey, _ := issuetracker.BranchKey(mr.SourceBranch, settings.Keys)
	var jobs []domain.IssueTrackerJob
	for _, ref := range orderedRefs(refs) {
		if ref.Relation != issuetracker.RelationCloses && ref.Key != branchKey {
			continue
		}
		jobs = append(jobs, s.transitionJob(settings, scope, mr, ref.Key, issueTransitionOpened, settings.OpenedStatus))
	}
	return jobs
}

// mergeRequestReady moves the issues of a draft that was marked ready, since
// edits made while it was a draft moved none.
func (s *IssueSync) mergeRequestReady(ctx context.Context, settings *domain.ProjectIssueTracker, mr *domain.MergeRequest) (int64, error) {
	if mr == nil || s.jobs == nil || settings.OpenedStatus == "" {
		return 0, nil
	}
	scope, err := s.jobScope(ctx, settings.ProjectID)
	if err != nil {
		return 0, err
	}
	jobs := s.openedTransitions(settings, scope, mr, mergeRequestRefs(mr, settings.Keys))
	if len(jobs) == 0 {
		return 0, nil
	}
	var queued int64
	err = s.within(ctx, func(ctx context.Context) error {
		queued, err = s.enqueue(ctx, jobs)
		return err
	})
	return queued, err
}

// mergeRequestState queues the jobs of a merged, closed or reopened request.
// A merge resolves the remote links, comments on the issues and moves the
// issues a closing keyword in the title, description or commits names to
// merged_status; closing and reopening only refresh the links.
func (s *IssueSync) mergeRequestState(ctx context.Context, settings *domain.ProjectIssueTracker, ev IssueEvent) (int64, error) {
	mr := ev.MR
	if mr == nil || s.jobs == nil {
		return 0, nil
	}
	scope, err := s.jobScope(ctx, settings.ProjectID)
	if err != nil {
		return 0, err
	}
	links, err := s.links.ListIssueLinks(ctx, settings.ProjectID, domain.IssueSubjectMergeRequest, snow.ID(mr.ID))
	if err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(links))
	for _, link := range links {
		if key, ok := issuetracker.ParseKey(link.IssueKey, settings.Keys); ok {
			keys = append(keys, key)
		}
	}
	var closes []string
	if ev.Event == domain.WebhookEventMRMerged {
		closes = s.closingKeys(ctx, settings, mr)
		for _, key := range closes {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	slices.Sort(keys)

	var jobs []domain.IssueTrackerJob
	if settings.LinkMergeRequests && s.linksEnabled(scope) {
		payload := s.mergeRequestLink(scope, mr)
		for _, key := range keys {
			jobs = append(jobs, s.linkJob(settings, key, payload))
		}
	}
	if ev.Event == domain.WebhookEventMRMerged {
		if settings.CommentOnMerge {
			text := s.mergeComment(ctx, scope, mr, ev.Actor)
			for _, key := range keys {
				jobs = append(jobs, s.newJob(settings, key, domain.IssueJobComment,
					fmt.Sprintf("comment:%s:%s:%s", key, scope.mergeRequestLinkID(mr.Number), issueTransitionMerged),
					issueJobPayload{Text: text}))
			}
		}
		if settings.MergedStatus != "" {
			for _, key := range closes {
				jobs = append(jobs, s.transitionJob(settings, scope, mr, key, issueTransitionMerged, settings.MergedStatus))
			}
		}
	}
	if len(jobs) == 0 {
		return 0, nil
	}
	var queued int64
	err = s.within(ctx, func(ctx context.Context) error {
		queued, err = s.enqueue(ctx, jobs)
		return err
	})
	return queued, err
}

// closingKeys lists the keys a merged request closes: closing refs in its
// title, description and the messages of the commits it brought in.
func (s *IssueSync) closingKeys(ctx context.Context, settings *domain.ProjectIssueTracker, mr *domain.MergeRequest) []string {
	texts := []string{mr.Title + "\n" + mr.Description}
	if mr.MergedSourceCommitID != nil && s.jobs.commits != nil {
		var stop snow.ID
		if mr.MergeBaseCommitID != nil {
			stop = *mr.MergeBaseCommitID
		}
		commits, err := s.jobs.commits.CommitLogUntil(ctx, settings.ProjectID, *mr.MergedSourceCommitID, stop, mergeRequestCommitLimit)
		if err != nil {
			slog.Warn("reading merge request commits for closing keywords failed", "project", settings.ProjectID, "merge_request", mr.Number, "error", err)
		}
		for _, commit := range commits {
			if commit.Origin == "" {
				texts = append(texts, commit.Message)
			}
		}
	}
	var keys []string
	for _, text := range texts {
		for _, ref := range issuetracker.Extract(text, settings.Keys) {
			if ref.Relation == issuetracker.RelationCloses && !slices.Contains(keys, ref.Key) {
				keys = append(keys, ref.Key)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

func (s *IssueSync) mergeRequestLink(scope *issueScope, mr *domain.MergeRequest) issueJobPayload {
	return issueJobPayload{
		LinkID:   scope.mergeRequestLinkID(mr.Number),
		URL:      s.mergeRequestURL(scope, mr.Number),
		Title:    truncateRunes(fmt.Sprintf("#%d %s", mr.Number, mr.Title), maxIssueLinkTitleRunes),
		Summary:  fmt.Sprintf("Merge request in %s/%s · %s", scope.org, scope.project, mr.Status),
		Resolved: mr.Status == domain.MergeRequestMerged || mr.Status == domain.MergeRequestClosed,
	}
}

func (s *IssueSync) mergeComment(ctx context.Context, scope *issueScope, mr *domain.MergeRequest, actor snow.ID) string {
	name := "Someone"
	if user, err := s.jobs.users.GetByID(ctx, actor); err == nil && user != nil && strings.TrimSpace(user.Name) != "" {
		name = strings.TrimSpace(user.Name)
	}
	text := fmt.Sprintf("%s merged merge request #%d %q in %s/%s.", name, mr.Number, mr.Title, scope.org, scope.project)
	if s.jobs.baseURL != "" {
		text += "\n" + s.mergeRequestURL(scope, mr.Number)
	}
	return text
}

func (s *IssueSync) linkJob(settings *domain.ProjectIssueTracker, key string, payload issueJobPayload) domain.IssueTrackerJob {
	return s.newJob(settings, key, domain.IssueJobLink, linkDedupeKey(key, payload.LinkID), payload)
}

// linkDedupeKey names the one row of a remote link: its link and unlink jobs
// share it, so the latest wanted state supersedes a job that has not run.
func linkDedupeKey(key, linkID string) string {
	return fmt.Sprintf("link:%s:%s", key, linkID)
}

func (s *IssueSync) transitionJob(settings *domain.ProjectIssueTracker, scope *issueScope, mr *domain.MergeRequest, key, phase, status string) domain.IssueTrackerJob {
	return s.newJob(settings, key, domain.IssueJobTransition,
		fmt.Sprintf("transition:%s:%s:%s:%s", key, scope.mergeRequestLinkID(mr.Number), phase, status),
		issueJobPayload{Status: status, SkipDone: phase == issueTransitionOpened})
}

func (s *IssueSync) newJob(settings *domain.ProjectIssueTracker, key, action, dedupe string, payload issueJobPayload) domain.IssueTrackerJob {
	body, _ := json.Marshal(payload)
	now := s.now().UTC()
	return domain.IssueTrackerJob{
		ID:          s.node.Generate(),
		TrackerID:   settings.TrackerID,
		ProjectID:   settings.ProjectID,
		IssueKey:    key,
		Action:      action,
		Payload:     string(body),
		DedupeKey:   dedupe,
		State:       domain.IssueJobPending,
		NextRetryAt: &now,
		CreatedAt:   now,
	}
}

func (s *IssueSync) enqueue(ctx context.Context, jobs []domain.IssueTrackerJob) (int64, error) {
	if len(jobs) == 0 {
		return 0, nil
	}
	return s.links.EnqueueIssueJobs(ctx, jobs)
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}
