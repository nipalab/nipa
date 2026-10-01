package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/webhook"
)

type hookEmitDeps struct {
	emitter  *HookEmitter
	hooks    *MockhookActiveRepository
	projects *MockhookProjectLookup
	orgs     *MockhookOrgLookup
	branches *MockhookDefaultBranchLookup
	users    *MockuserLookup
	sender   *MockhookSender
}

func newHookEmitDeps(t *testing.T) *hookEmitDeps {
	t.Helper()
	ctrl := gomock.NewController(t)
	deps := &hookEmitDeps{
		hooks:    NewMockhookActiveRepository(ctrl),
		projects: NewMockhookProjectLookup(ctrl),
		orgs:     NewMockhookOrgLookup(ctrl),
		branches: NewMockhookDefaultBranchLookup(ctrl),
		users:    NewMockuserLookup(ctrl),
		sender:   NewMockhookSender(ctrl),
	}
	deps.emitter = NewHookEmitter(deps.hooks, deps.projects, deps.orgs, deps.branches, deps.users, deps.sender)
	deps.emitter.now = func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }
	return deps
}

func (d *hookEmitDeps) expectPayloadContext(projectID snow.ID) {
	d.projects.EXPECT().Get(gomock.Any(), projectID).
		Return(&domain.Project{ID: projectID, OrgID: 3, Slug: "game"}, nil)
	d.orgs.EXPECT().GetByID(gomock.Any(), snow.ID(3)).
		Return(&domain.Organization{ID: 3, Slug: "acme"}, nil)
	d.branches.EXPECT().GetDefaultBranch(gomock.Any(), projectID).
		Return(&domain.Branch{ID: 2, ProjectID: projectID, Name: "main", IsDefault: true}, nil)
	d.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).
		Return(&domain.User{ID: 7, Name: "alice"}, nil)
}

func testHook(id int64, events []string, prefix string) *domain.Webhook {
	return &domain.Webhook{
		ID: snow.ID(id), ProjectID: 1, Name: "hook", URL: "https://example.com/hook",
		Secret: "s", Events: events, PathPrefix: prefix, IsActive: true,
	}
}

func samplePushEvent() PushEvent {
	return PushEvent{
		ProjectID: 1,
		Actor:     7,
		Branch:    &domain.Branch{ID: 5, ProjectID: 1, Name: "main"},
		Before:    &domain.Commit{ID: snow.ID(9)},
		AfterID:   11,
		Message:   "msg",
		Files: []*domain.PushFile{
			{Path: "assets/new.bin", SizeBytes: 10, IsBinary: true},
			{Path: "assets/old.bin", SizeBytes: 20, IsBinary: true},
			{Path: "docs/note.md", SizeBytes: 30},
		},
		Removed:    []string{"docs/old.md"},
		HeadBinary: map[string]bool{"assets/old.bin": true, "docs/note.md": false},
	}
}

func TestHookEmitter_EmitPush(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventPush}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	deps.expectPayloadContext(snow.ID(1))

	var captured webhook.PushPayload
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventPush, gomock.Any()).
		DoAndReturn(func(_ context.Context, delivered domain.Webhook, event string, payload []byte) (*domain.WebhookDelivery, error) {
			require.Equal(t, hook.ID, delivered.ID)
			require.Equal(t, domain.WebhookEventPush, event)
			require.NoError(t, json.Unmarshal(payload, &captured))
			return &domain.WebhookDelivery{ID: snow.ID(5001)}, nil
		})

	require.NoError(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()))

	require.Equal(t, domain.WebhookEventPush, captured.Event)
	require.Equal(t, hook.ID.Base36(), captured.WebhookID)
	require.Equal(t, time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), captured.Timestamp)
	require.Equal(t, "acme", captured.Organization.Slug)
	require.Equal(t, "game", captured.Project.Slug)
	require.Equal(t, "main", captured.Project.DefaultBranch)
	require.Equal(t, snow.ID(7).Base36(), captured.Actor.ID)
	require.Equal(t, "alice", captured.Actor.Username)

	require.Len(t, captured.Changes, 1)
	change := captured.Changes[0]
	require.Equal(t, "main", change.Branch)
	require.Equal(t, snow.ID(5).Base36(), change.BranchID)
	require.False(t, change.Created)
	require.False(t, change.Deleted)
	require.False(t, change.Forced)
	require.Equal(t, snow.ID(9).Base36(), change.Before)
	require.Equal(t, snow.ID(11).Base36(), change.After)
	require.Equal(t, "msg", change.Message)
	require.False(t, change.FilesTruncated)
	require.Equal(t, []webhook.PushFile{
		{Path: "assets/new.bin", Operation: webhook.PushFileAdded, Binary: true, SizeBytes: 10},
		{Path: "assets/old.bin", Operation: webhook.PushFileModified, Binary: true, SizeBytes: 20},
		{Path: "docs/note.md", Operation: webhook.PushFileModified, Binary: false, SizeBytes: 30},
		{Path: "docs/old.md", Operation: webhook.PushFileDeleted, Binary: false},
	}, change.Files)
}

func TestHookEmitter_EmitPushOffEmptyBranch(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventPush}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	deps.expectPayloadContext(snow.ID(1))

	var captured webhook.PushPayload
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventPush, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.Webhook, _ string, payload []byte) (*domain.WebhookDelivery, error) {
			require.NoError(t, json.Unmarshal(payload, &captured))
			return &domain.WebhookDelivery{}, nil
		})

	event := samplePushEvent()
	event.Before = nil
	event.HeadBinary = nil
	require.NoError(t, deps.emitter.EmitPush(context.Background(), event))

	change := captured.Changes[0]
	require.True(t, change.Created, "the first push to an empty branch marks it created")
	require.Empty(t, change.Before)
	require.Equal(t, webhook.PushFileAdded, change.Files[0].Operation)
}

func TestHookEmitter_SkipsUnsubscribedEvents(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventMRCreated}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	// no project/org/user expectations: a filtered event resolves nothing

	require.NoError(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()))
}

func TestHookEmitter_PathPrefixFiltersPush(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventPush}, "assets")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).
		Return([]*domain.Webhook{hook}, nil).Times(2)

	event := samplePushEvent()
	event.Files = []*domain.PushFile{{Path: "docs/note.md", SizeBytes: 1}}
	event.Removed = nil
	event.HeadBinary = nil
	require.NoError(t, deps.emitter.EmitPush(context.Background(), event), "no touched path is covered")

	deps.expectPayloadContext(snow.ID(1))
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventPush, gomock.Any()).
		Return(&domain.WebhookDelivery{}, nil)
	require.NoError(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()))
}

func TestHookEmitter_FansOutPerHook(t *testing.T) {
	deps := newHookEmitDeps(t)
	first := testHook(1001, []string{domain.WebhookEventPush}, "")
	second := testHook(1002, []string{domain.WebhookEventPush}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).
		Return([]*domain.Webhook{first, second}, nil)
	deps.expectPayloadContext(snow.ID(1))

	webhookIDs := map[string]bool{}
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventPush, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.Webhook, _ string, payload []byte) (*domain.WebhookDelivery, error) {
			var captured webhook.PushPayload
			require.NoError(t, json.Unmarshal(payload, &captured))
			webhookIDs[captured.WebhookID] = true
			return &domain.WebhookDelivery{}, nil
		}).Times(2)

	require.NoError(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()))
	require.Equal(t, map[string]bool{first.ID.Base36(): true, second.ID.Base36(): true}, webhookIDs)
}

func TestHookEmitter_TruncatesPushFiles(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventPush}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	deps.expectPayloadContext(snow.ID(1))

	var captured webhook.PushPayload
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventPush, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.Webhook, _ string, payload []byte) (*domain.WebhookDelivery, error) {
			require.NoError(t, json.Unmarshal(payload, &captured))
			return &domain.WebhookDelivery{}, nil
		})

	event := samplePushEvent()
	event.Files = make([]*domain.PushFile, hookPushFileLimit+1)
	event.Removed = nil
	event.HeadBinary = nil
	for i := range event.Files {
		event.Files[i] = &domain.PushFile{Path: fmt.Sprintf("assets/%04d.bin", i), SizeBytes: 1}
	}
	require.NoError(t, deps.emitter.EmitPush(context.Background(), event))

	change := captured.Changes[0]
	require.Len(t, change.Files, hookPushFileLimit)
	require.True(t, change.FilesTruncated)
}

func TestHookEmitter_EmitMergeRequest(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventMRCreated}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	deps.expectPayloadContext(snow.ID(1))

	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mr := &domain.MergeRequest{
		ID: 42, Number: 7, ProjectID: 1, SourceBranchID: 3, TargetBranchID: 2,
		SourceBranch: "feature", TargetBranch: "main", Title: "Add asset",
		Description: "body", Status: domain.MergeRequestOpen, CreatedBy: 8,
		CreatedAt: created, UpdatedAt: created,
	}

	var captured webhook.MergeRequestPayload
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventMRCreated, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.Webhook, _ string, payload []byte) (*domain.WebhookDelivery, error) {
			require.NoError(t, json.Unmarshal(payload, &captured))
			return &domain.WebhookDelivery{}, nil
		})

	require.NoError(t, deps.emitter.EmitMergeRequest(context.Background(), domain.WebhookEventMRCreated, snow.ID(1), mr, snow.ID(7)))

	require.Equal(t, domain.WebhookEventMRCreated, captured.Event)
	require.Equal(t, snow.ID(42).Base36(), captured.MergeRequest.ID)
	require.Equal(t, int64(7), captured.MergeRequest.Number)
	require.Equal(t, "Add asset", captured.MergeRequest.Title)
	require.Equal(t, "body", captured.MergeRequest.Description)
	require.Equal(t, domain.MergeRequestOpen, captured.MergeRequest.State)
	require.Equal(t, "feature", captured.MergeRequest.SourceBranch)
	require.Equal(t, "main", captured.MergeRequest.TargetBranch)
	require.Equal(t, snow.ID(8).Base36(), captured.MergeRequest.AuthorID)
	require.Empty(t, captured.MergeRequest.MergeCommitID)
	require.Equal(t, created, captured.MergeRequest.CreatedAt)
}

func TestHookEmitter_EmitBranch(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventBranchDeleted}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	deps.expectPayloadContext(snow.ID(1))

	head := snow.ID(12)
	branch := &domain.Branch{ID: 5, ProjectID: 1, Name: "release", CommitID: &head}

	var captured webhook.BranchPayload
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventBranchDeleted, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.Webhook, _ string, payload []byte) (*domain.WebhookDelivery, error) {
			require.NoError(t, json.Unmarshal(payload, &captured))
			return &domain.WebhookDelivery{}, nil
		})

	require.NoError(t, deps.emitter.EmitBranch(context.Background(), domain.WebhookEventBranchDeleted, snow.ID(1), branch, snow.ID(7)))

	require.Equal(t, domain.WebhookEventBranchDeleted, captured.Event)
	require.Equal(t, snow.ID(5).Base36(), captured.Branch.ID)
	require.Equal(t, "release", captured.Branch.Name)
	require.Equal(t, head.Base36(), captured.Branch.CommitID)
	require.False(t, captured.Branch.Default)
}

func TestHookEmitter_EmitTag(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventTagCreated}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	deps.expectPayloadContext(snow.ID(1))

	tag := &domain.Tag{ID: 5, ProjectID: 1, Name: "v1.0.0", CommitID: 12, Message: "first release", UserID: 8}

	var captured webhook.TagPayload
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventTagCreated, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.Webhook, _ string, payload []byte) (*domain.WebhookDelivery, error) {
			require.NoError(t, json.Unmarshal(payload, &captured))
			return &domain.WebhookDelivery{}, nil
		})

	require.NoError(t, deps.emitter.EmitTag(context.Background(), domain.WebhookEventTagCreated, snow.ID(1), tag, snow.ID(7)))

	require.Equal(t, domain.WebhookEventTagCreated, captured.Event)
	require.Equal(t, snow.ID(5).Base36(), captured.Tag.ID)
	require.Equal(t, "v1.0.0", captured.Tag.Name)
	require.Equal(t, snow.ID(12).Base36(), captured.Tag.CommitID)
	require.Equal(t, "first release", captured.Tag.Message)
	require.Equal(t, snow.ID(8).Base36(), captured.Tag.CreatedBy)
}

func TestHookEmitter_NoHooksSkipsLookups(t *testing.T) {
	deps := newHookEmitDeps(t)
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return(nil, nil)

	require.NoError(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()))
}

func TestHookEmitter_Errors(t *testing.T) {
	t.Run("hook listing", func(t *testing.T) {
		deps := newHookEmitDeps(t)
		deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return(nil, errors.New("db down"))

		require.ErrorContains(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()), "db down")
	})

	t.Run("payload context", func(t *testing.T) {
		deps := newHookEmitDeps(t)
		hook := testHook(1001, []string{domain.WebhookEventPush}, "")
		deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
		deps.projects.EXPECT().Get(gomock.Any(), snow.ID(1)).Return(nil, errors.New("project missing"))

		require.ErrorContains(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()), "project missing")
	})

	t.Run("sender", func(t *testing.T) {
		deps := newHookEmitDeps(t)
		hook := testHook(1001, []string{domain.WebhookEventPush}, "")
		deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
		deps.expectPayloadContext(snow.ID(1))
		deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventPush, gomock.Any()).
			Return(nil, errors.New("queue full"))

		require.ErrorContains(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()), "queue full")
	})
}

func TestHookEmitter_ToleratesOptionalLookupFailures(t *testing.T) {
	deps := newHookEmitDeps(t)
	hook := testHook(1001, []string{domain.WebhookEventPush}, "")
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return([]*domain.Webhook{hook}, nil)
	deps.projects.EXPECT().Get(gomock.Any(), snow.ID(1)).
		Return(&domain.Project{ID: 1, OrgID: 3, Slug: "game"}, nil)
	deps.orgs.EXPECT().GetByID(gomock.Any(), snow.ID(3)).
		Return(&domain.Organization{ID: 3, Slug: "acme"}, nil)
	deps.branches.EXPECT().GetDefaultBranch(gomock.Any(), snow.ID(1)).Return(nil, errors.New("no default"))
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(7)).Return(nil, domain.NewErrorRecordNotFound())

	var captured webhook.PushPayload
	deps.sender.EXPECT().Enqueue(gomock.Any(), gomock.Any(), domain.WebhookEventPush, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ domain.Webhook, _ string, payload []byte) (*domain.WebhookDelivery, error) {
			require.NoError(t, json.Unmarshal(payload, &captured))
			return &domain.WebhookDelivery{}, nil
		})

	require.NoError(t, deps.emitter.EmitPush(context.Background(), samplePushEvent()))
	require.Empty(t, captured.Project.DefaultBranch)
	require.Empty(t, captured.Actor.Username)
}
