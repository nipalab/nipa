package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/webhook"
)

// hookPushFileLimit caps how many file entries a push payload carries.
const hookPushFileLimit = 500

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=webhook_emit_mock_test.go -package=usecase
type hookActiveRepository interface {
	ListActiveByProject(ctx context.Context, projectID snow.ID) ([]*domain.Webhook, error)
}

type hookProjectLookup interface {
	Get(ctx context.Context, id snow.ID) (*domain.Project, error)
}

type hookOrgLookup interface {
	GetByID(ctx context.Context, id snow.ID) (*domain.Organization, error)
}

type hookDefaultBranchLookup interface {
	GetDefaultBranch(ctx context.Context, projectID snow.ID) (*domain.Branch, error)
}

type hookSender interface {
	Enqueue(ctx context.Context, hook domain.Webhook, event string, payload []byte) (*domain.WebhookDelivery, error)
}

// PushEvent describes a landed push for the webhook payload.
type PushEvent struct {
	ProjectID snow.ID
	Actor     snow.ID
	Branch    *domain.Branch
	Before    *domain.Commit
	AfterID   snow.ID
	Message   string
	Files     []*domain.PushFile
	Removed   []string
	// HeadBinary marks the touched paths that already existed in the old head;
	// its value is their binary flag.
	HeadBinary map[string]bool
}

// HookEmitter fans events out to the project webhooks that subscribe to them.
// It resolves the payload context (org/project slugs, actor name) only when at
// least one hook matches.
type HookEmitter struct {
	hooks    hookActiveRepository
	projects hookProjectLookup
	orgs     hookOrgLookup
	branches hookDefaultBranchLookup
	users    userLookup
	sender   hookSender
	now      func() time.Time
}

func NewHookEmitter(hooks hookActiveRepository, projects hookProjectLookup, orgs hookOrgLookup, branches hookDefaultBranchLookup, users userLookup, sender hookSender) *HookEmitter {
	return &HookEmitter{
		hooks:    hooks,
		projects: projects,
		orgs:     orgs,
		branches: branches,
		users:    users,
		sender:   sender,
		now:      time.Now,
	}
}

func (e *HookEmitter) EmitPush(ctx context.Context, event PushEvent) error {
	return e.emit(ctx, domain.WebhookEventPush, event.ProjectID, event.Actor, pushEventPaths(event), func(env webhook.Envelope) any {
		return webhook.PushPayload{Envelope: env, Changes: []webhook.PushChange{newPushChange(event)}}
	})
}

func (e *HookEmitter) EmitMergeRequest(ctx context.Context, event string, projectID snow.ID, mr *domain.MergeRequest, actor snow.ID) error {
	return e.emit(ctx, event, projectID, actor, nil, func(env webhook.Envelope) any {
		return webhook.MergeRequestPayload{Envelope: env, MergeRequest: newMergeRequestInfo(mr)}
	})
}

func (e *HookEmitter) EmitBranch(ctx context.Context, event string, projectID snow.ID, branch *domain.Branch, actor snow.ID) error {
	return e.emit(ctx, event, projectID, actor, nil, func(env webhook.Envelope) any {
		return webhook.BranchPayload{Envelope: env, Branch: newBranchInfo(branch)}
	})
}

func (e *HookEmitter) emit(ctx context.Context, event string, projectID, actor snow.ID, paths []string, build func(webhook.Envelope) any) error {
	hooks, err := e.hooks.ListActiveByProject(ctx, projectID)
	if err != nil {
		return err
	}
	matching := make([]*domain.Webhook, 0, len(hooks))
	for _, hook := range hooks {
		if hookWantsEvent(hook, event, paths) {
			matching = append(matching, hook)
		}
	}
	if len(matching) == 0 {
		return nil
	}

	env, err := e.envelope(ctx, event, projectID, actor)
	if err != nil {
		return err
	}
	var errs []error
	for _, hook := range matching {
		env.WebhookID = hook.ID.Base36()
		payload, err := json.Marshal(build(env))
		if err != nil {
			errs = append(errs, fmt.Errorf("encode %s payload for webhook %s: %w", event, hook.ID, err))
			continue
		}
		if _, err := e.sender.Enqueue(ctx, *hook, event, payload); err != nil {
			errs = append(errs, fmt.Errorf("queue %s for webhook %s: %w", event, hook.ID, err))
		}
	}
	return errors.Join(errs...)
}

func (e *HookEmitter) envelope(ctx context.Context, event string, projectID, actor snow.ID) (webhook.Envelope, error) {
	project, err := e.projects.Get(ctx, projectID)
	if err != nil {
		return webhook.Envelope{}, err
	}
	org, err := e.orgs.GetByID(ctx, project.OrgID)
	if err != nil {
		return webhook.Envelope{}, err
	}
	env := webhook.Envelope{
		Event:        event,
		Timestamp:    e.now().UTC(),
		Actor:        webhook.Actor{ID: actor.Base36()},
		Organization: webhook.Organization{Slug: org.Slug},
		Project:      webhook.Project{Slug: project.Slug},
	}
	if branch, err := e.branches.GetDefaultBranch(ctx, projectID); err == nil && branch != nil {
		env.Project.DefaultBranch = branch.Name
	}
	if user, err := e.users.GetByID(ctx, actor); err == nil && user != nil {
		env.Actor.Username = user.Name
	}
	return env, nil
}

func hookWantsEvent(hook *domain.Webhook, event string, paths []string) bool {
	if !hook.IsActive || !hookSubscribes(hook, event) {
		return false
	}
	if event != domain.WebhookEventPush || hook.PathPrefix == "" || len(paths) == 0 {
		return true
	}
	for _, path := range paths {
		if domain.PrefixCovers(hook.PathPrefix, path) {
			return true
		}
	}
	return false
}

func hookSubscribes(hook *domain.Webhook, event string) bool {
	for _, subscribed := range hook.Events {
		if subscribed == event {
			return true
		}
	}
	return false
}

func pushEventPaths(event PushEvent) []string {
	return touchedPushPaths(event.Files, event.Removed)
}

func newPushChange(event PushEvent) webhook.PushChange {
	change := webhook.PushChange{
		Branch:   event.Branch.Name,
		BranchID: event.Branch.ID.Base36(),
		Created:  event.Before == nil,
		After:    event.AfterID.Base36(),
		Message:  event.Message,
	}
	if event.Before != nil {
		change.Before = event.Before.ID.Base36()
	}
	files := make([]webhook.PushFile, 0, len(event.Files)+len(event.Removed))
	for _, file := range event.Files {
		operation := webhook.PushFileAdded
		binary := file.IsBinary
		if headBinary, tracked := event.HeadBinary[file.Path]; tracked {
			operation = webhook.PushFileModified
			binary = binary || headBinary
		}
		files = append(files, webhook.PushFile{
			Path:      file.Path,
			Operation: operation,
			Binary:    binary,
			SizeBytes: file.SizeBytes,
		})
	}
	for _, path := range event.Removed {
		files = append(files, webhook.PushFile{
			Path:      path,
			Operation: webhook.PushFileDeleted,
			Binary:    event.HeadBinary[path],
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	if len(files) > hookPushFileLimit {
		files = files[:hookPushFileLimit]
		change.FilesTruncated = true
	}
	change.Files = files
	return change
}

func newMergeRequestInfo(mr *domain.MergeRequest) webhook.MergeRequestInfo {
	info := webhook.MergeRequestInfo{
		ID:           snow.ID(mr.ID).Base36(),
		Number:       mr.Number,
		Title:        mr.Title,
		Description:  mr.Description,
		State:        mr.Status,
		SourceBranch: mr.SourceBranch,
		TargetBranch: mr.TargetBranch,
		AuthorID:     mr.CreatedBy.Base36(),
		CreatedAt:    mr.CreatedAt,
		UpdatedAt:    mr.UpdatedAt,
	}
	if mr.MergeCommitID != nil {
		info.MergeCommitID = mr.MergeCommitID.Base36()
	}
	return info
}

func newBranchInfo(branch *domain.Branch) webhook.BranchInfo {
	info := webhook.BranchInfo{
		ID:      branch.ID.Base36(),
		Name:    branch.Name,
		Default: branch.IsDefault,
	}
	if branch.CommitID != nil {
		info.CommitID = branch.CommitID.Base36()
	}
	return info
}
