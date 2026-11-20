package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=issue_sync_mock_test.go -package=usecase

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/issuetracker"
	"github.com/nipalab/nipa/internal/snow"
)

// IssueEvent is what the emitter hands the issue sync sink: Push for push
// events, Branch for branch events, MR for merge request events.
type IssueEvent struct {
	Event     string
	ProjectID snow.ID
	Push      *PushEvent
	Branch    *domain.Branch
	MR        *domain.MergeRequest
}

// issueSyncSink is the optional issue tracker seam on the emitter.
type issueSyncSink interface {
	Wants(event string) bool
	Handle(ctx context.Context, event IssueEvent) error
}

type issueLinkStore interface {
	GetProject(ctx context.Context, projectID snow.ID) (*domain.ProjectIssueTracker, error)
	UpsertIssueLink(ctx context.Context, link domain.IssueLink) error
	ListIssueLinks(ctx context.Context, projectID snow.ID, kind string, subjectID snow.ID) ([]domain.IssueLink, error)
	DeleteIssueLinks(ctx context.Context, projectID snow.ID, kind string, subjectID snow.ID, keys []string) (int64, error)
}

var issueSyncEvents = map[string]bool{
	domain.WebhookEventPush:          true,
	domain.WebhookEventBranchCreated: true,
	domain.WebhookEventMRCreated:     true,
	domain.WebhookEventMRUpdated:     true,
	domain.WebhookEventMRMerged:      true,
	domain.WebhookEventMRClosed:      true,
	domain.WebhookEventMRReopened:    true,
}

// IssueSync records which commits, branches and merge requests mention
// which issues of a project's active issue tracker settings. It writes link
// rows only; calls to the tracker happen elsewhere.
type IssueSync struct {
	links issueLinkStore
	tx    transactor
	node  snow.Node
	now   func() time.Time
}

func NewIssueSync(links issueLinkStore, tx transactor, node snow.Node) *IssueSync {
	return &IssueSync{links: links, tx: tx, node: node, now: time.Now}
}

func (s *IssueSync) Wants(event string) bool {
	return issueSyncEvents[event]
}

func (s *IssueSync) Handle(ctx context.Context, ev IssueEvent) error {
	settings, err := s.links.GetProject(ctx, ev.ProjectID)
	if domain.IsErrorNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !settings.IsActive || len(settings.Keys) == 0 {
		return nil
	}
	switch ev.Event {
	case domain.WebhookEventPush:
		return s.push(ctx, settings, ev.Push)
	case domain.WebhookEventBranchCreated:
		return s.branch(ctx, settings, ev.Branch)
	case domain.WebhookEventMRCreated, domain.WebhookEventMRUpdated:
		return s.mergeRequest(ctx, settings, ev.MR)
	case domain.WebhookEventMRMerged, domain.WebhookEventMRClosed, domain.WebhookEventMRReopened:
		return s.mergeRequestState(ctx, settings, ev)
	}
	return nil
}

// push links the pushed commit to the keys of its message and the branch to
// its leading key. A push lands exactly one commit; imported commits are
// never linked.
func (s *IssueSync) push(ctx context.Context, settings *domain.ProjectIssueTracker, push *PushEvent) error {
	if push == nil || push.Imported {
		return nil
	}
	for _, ref := range issuetracker.Extract(push.Message, settings.Keys) {
		if err := s.upsert(ctx, settings, domain.IssueSubjectCommit, push.AfterID, ref); err != nil {
			return err
		}
	}
	return s.branch(ctx, settings, push.Branch)
}

func (s *IssueSync) branch(ctx context.Context, settings *domain.ProjectIssueTracker, branch *domain.Branch) error {
	if branch == nil {
		return nil
	}
	key, ok := issuetracker.BranchKey(branch.Name, settings.Keys)
	if !ok {
		return nil
	}
	return s.upsert(ctx, settings, domain.IssueSubjectBranch, branch.ID, issuetracker.Ref{Key: key, Relation: issuetracker.RelationMentions})
}

// mergeRequest replaces the request's links with the keys its title,
// description and source branch name carry now, so an edit that drops a
// key drops its link.
func (s *IssueSync) mergeRequest(ctx context.Context, settings *domain.ProjectIssueTracker, mr *domain.MergeRequest) error {
	if mr == nil {
		return nil
	}
	want := mergeRequestRefs(mr, settings.Keys)
	subject := snow.ID(mr.ID)
	return s.within(ctx, func(ctx context.Context) error {
		existing, err := s.links.ListIssueLinks(ctx, settings.ProjectID, domain.IssueSubjectMergeRequest, subject)
		if err != nil {
			return err
		}
		have := make(map[string]string, len(existing))
		var stale []string
		for _, link := range existing {
			have[link.IssueKey] = link.Relation
			if _, keep := want[link.IssueKey]; !keep {
				stale = append(stale, link.IssueKey)
			}
		}
		if _, err := s.links.DeleteIssueLinks(ctx, settings.ProjectID, domain.IssueSubjectMergeRequest, subject, stale); err != nil {
			return err
		}
		for _, ref := range orderedRefs(want) {
			if have[ref.Key] == ref.Relation {
				continue
			}
			if err := s.upsert(ctx, settings, domain.IssueSubjectMergeRequest, subject, ref); err != nil {
				return err
			}
		}
		return nil
	})
}

// mergeRequestState is where merged, closed and reopened requests will
// queue tracker jobs; they change no links.
func (s *IssueSync) mergeRequestState(context.Context, *domain.ProjectIssueTracker, IssueEvent) error {
	return nil
}

func mergeRequestRefs(mr *domain.MergeRequest, keys []string) map[string]issuetracker.Ref {
	want := map[string]issuetracker.Ref{}
	for _, ref := range issuetracker.Extract(mr.Title+"\n"+mr.Description, keys) {
		want[ref.Key] = ref
	}
	if key, ok := issuetracker.BranchKey(mr.SourceBranch, keys); ok {
		if _, seen := want[key]; !seen {
			want[key] = issuetracker.Ref{Key: key, Relation: issuetracker.RelationMentions}
		}
	}
	return want
}

func orderedRefs(refs map[string]issuetracker.Ref) []issuetracker.Ref {
	out := make([]issuetracker.Ref, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref)
	}
	slices.SortFunc(out, func(a, b issuetracker.Ref) int { return strings.Compare(a.Key, b.Key) })
	return out
}

func (s *IssueSync) upsert(ctx context.Context, settings *domain.ProjectIssueTracker, kind string, subject snow.ID, ref issuetracker.Ref) error {
	return s.links.UpsertIssueLink(ctx, domain.IssueLink{
		ID:          s.node.Generate(),
		ProjectID:   settings.ProjectID,
		TrackerID:   settings.TrackerID,
		IssueKey:    ref.Key,
		SubjectKind: kind,
		SubjectID:   subject,
		Relation:    ref.Relation,
		CreatedAt:   s.now().UTC(),
	})
}

func (s *IssueSync) within(ctx context.Context, fn func(context.Context) error) error {
	if s.tx == nil {
		return fn(ctx)
	}
	return s.tx.WithinTx(ctx, fn)
}
