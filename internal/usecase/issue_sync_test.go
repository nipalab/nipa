package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type txRecorder struct{ calls int }

func (r *txRecorder) WithinTx(ctx context.Context, fn func(context.Context) error) error {
	r.calls++
	return fn(ctx)
}

func newIssueSyncFixture(t *testing.T) (*IssueSync, *MockissueLinkStore, *txRecorder) {
	ctrl := gomock.NewController(t)
	store := NewMockissueLinkStore(ctrl)
	tx := &txRecorder{}
	sync := NewIssueSync(store, tx, newTestBranchNode(t))
	sync.now = func() time.Time { return time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC) }
	return sync, store, tx
}

func activeSettings() *domain.ProjectIssueTracker {
	return &domain.ProjectIssueTracker{ProjectID: 1, TrackerID: 9, Keys: []string{"GAME"}, IsActive: true}
}

type linkMatcher struct {
	key, kind, relation string
	subject             snow.ID
}

func (m linkMatcher) Matches(x any) bool {
	l, ok := x.(domain.IssueLink)
	return ok && l.IssueKey == m.key && l.SubjectKind == m.kind && l.Relation == m.relation &&
		l.SubjectID == m.subject && l.ProjectID == 1 && l.TrackerID == 9 && l.ID != 0
}

func (m linkMatcher) String() string { return m.kind + ":" + m.key + ":" + m.relation }

func TestIssueSync_Wants(t *testing.T) {
	sync, _, _ := newIssueSyncFixture(t)
	for _, event := range []string{domain.WebhookEventPush, domain.WebhookEventBranchCreated, domain.WebhookEventMRCreated, domain.WebhookEventMRUpdated, domain.WebhookEventMRMerged, domain.WebhookEventMRClosed, domain.WebhookEventMRReopened} {
		require.True(t, sync.Wants(event), event)
	}
	require.False(t, sync.Wants(domain.WebhookEventTagCreated))
	require.False(t, sync.Wants(domain.WebhookEventMRCommentCreated))
}

func TestIssueSync_PushLinksCommitAndBranch(t *testing.T) {
	sync, store, _ := newIssueSyncFixture(t)
	ctx := context.Background()
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil)
	store.EXPECT().UpsertIssueLink(ctx, linkMatcher{"GAME-1", domain.IssueSubjectCommit, "closes", 11}).Return(nil)
	store.EXPECT().UpsertIssueLink(ctx, linkMatcher{"GAME-2", domain.IssueSubjectCommit, "mentions", 11}).Return(nil)
	store.EXPECT().UpsertIssueLink(ctx, linkMatcher{"GAME-7", domain.IssueSubjectBranch, "mentions", 5}).Return(nil)
	push := &PushEvent{ProjectID: 1, AfterID: 11, Message: "Fixes GAME-1, see GAME-2 and UTF-8", Branch: &domain.Branch{ID: 5, Name: "feature/GAME-7-rig"}}
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: push}))
}

func TestIssueSync_PushSkipsImportedAndNil(t *testing.T) {
	sync, store, _ := newIssueSyncFixture(t)
	ctx := context.Background()
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil).Times(2)
	imported := &PushEvent{ProjectID: 1, AfterID: 11, Message: "Fixes GAME-1", Branch: &domain.Branch{ID: 5, Name: "GAME-1"}, Imported: true}
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: imported}))
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1}))
}

func TestIssueSync_SkipsInactiveOrUnconfigured(t *testing.T) {
	sync, store, _ := newIssueSyncFixture(t)
	ctx := context.Background()
	ev := IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: &PushEvent{Message: "GAME-1"}}
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(nil, domain.NewErrorNotFound("record not found"))
	require.NoError(t, sync.Handle(ctx, ev))
	inactive := activeSettings()
	inactive.IsActive = false
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(inactive, nil)
	require.NoError(t, sync.Handle(ctx, ev))
	boom := errors.New("db down")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(nil, boom)
	require.ErrorIs(t, sync.Handle(ctx, ev), boom)
}

func TestIssueSync_PushUpsertError(t *testing.T) {
	sync, store, _ := newIssueSyncFixture(t)
	ctx := context.Background()
	boom := errors.New("insert failed")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(boom)
	require.ErrorIs(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventPush, ProjectID: 1, Push: &PushEvent{AfterID: 2, Message: "GAME-3"}}), boom)
}

func TestIssueSync_BranchCreated(t *testing.T) {
	sync, store, _ := newIssueSyncFixture(t)
	ctx := context.Background()
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil).Times(3)
	store.EXPECT().UpsertIssueLink(ctx, linkMatcher{"GAME-4", domain.IssueSubjectBranch, "mentions", 8}).Return(nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventBranchCreated, ProjectID: 1, Branch: &domain.Branch{ID: 8, Name: "game-4-ui"}}))
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventBranchCreated, ProjectID: 1, Branch: &domain.Branch{ID: 8, Name: "main"}}))
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventBranchCreated, ProjectID: 1}))
}

func TestIssueSync_MergeRequestReplacesLinks(t *testing.T) {
	sync, store, tx := newIssueSyncFixture(t)
	ctx := context.Background()
	mr := &domain.MergeRequest{ID: 40, Title: "Rig for GAME-2", Description: "Closes GAME-3", SourceBranch: "GAME-5-rig"}
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil)
	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return([]domain.IssueLink{
		{IssueKey: "GAME-1", Relation: "mentions"},
		{IssueKey: "GAME-2", Relation: "mentions"},
		{IssueKey: "GAME-3", Relation: "mentions"},
	}, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), []string{"GAME-1"}).Return(int64(1), nil)
	store.EXPECT().UpsertIssueLink(ctx, linkMatcher{"GAME-3", domain.IssueSubjectMergeRequest, "closes", 40}).Return(nil)
	store.EXPECT().UpsertIssueLink(ctx, linkMatcher{"GAME-5", domain.IssueSubjectMergeRequest, "mentions", 40}).Return(nil)
	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRUpdated, ProjectID: 1, MR: mr}))
	require.Equal(t, 1, tx.calls)
}

func TestIssueSync_MergeRequestErrors(t *testing.T) {
	sync, store, _ := newIssueSyncFixture(t)
	sync.tx = nil
	ctx := context.Background()
	mr := &domain.MergeRequest{ID: 40, Title: "GAME-2"}
	boom := errors.New("boom")
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil).Times(4)

	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(nil, boom)
	require.ErrorIs(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: mr}), boom)

	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return([]domain.IssueLink{{IssueKey: "GAME-9"}}, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), []string{"GAME-9"}).Return(int64(0), boom)
	require.ErrorIs(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: mr}), boom)

	store.EXPECT().ListIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40)).Return(nil, nil)
	store.EXPECT().DeleteIssueLinks(ctx, snow.ID(1), domain.IssueSubjectMergeRequest, snow.ID(40), nil).Return(int64(0), nil)
	store.EXPECT().UpsertIssueLink(ctx, gomock.Any()).Return(boom)
	require.ErrorIs(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: mr}), boom)

	require.NoError(t, sync.Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1}))
}

func TestIssueSync_MergeRequestStateAndOtherEvents(t *testing.T) {
	sync, store, _ := newIssueSyncFixture(t)
	ctx := context.Background()
	store.EXPECT().GetProject(ctx, snow.ID(1)).Return(activeSettings(), nil).Times(4)
	for _, event := range []string{domain.WebhookEventMRMerged, domain.WebhookEventMRClosed, domain.WebhookEventMRReopened, domain.WebhookEventTagCreated} {
		require.NoError(t, sync.Handle(ctx, IssueEvent{Event: event, ProjectID: 1, MR: &domain.MergeRequest{Title: "GAME-1"}}))
	}
}

func TestHookEmitter_IssueSyncRunsWithoutHooks(t *testing.T) {
	deps := newHookEmitDeps(t)
	sink := NewMockissueSyncSink(gomock.NewController(t))
	deps.emitter.WithIssueSync(sink)
	ctx := context.Background()
	deps.hooks.EXPECT().ListActiveByProject(gomock.Any(), snow.ID(1)).Return(nil, nil).Times(4)

	sink.EXPECT().Wants(domain.WebhookEventPush).Return(true)
	sink.EXPECT().Handle(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, ev IssueEvent) error {
		require.Equal(t, snow.ID(11), ev.Push.AfterID)
		return errors.New("logged only")
	})
	require.NoError(t, deps.emitter.EmitPush(ctx, samplePushEvent()))

	mr := &domain.MergeRequest{ID: 3, Title: "GAME-1"}
	sink.EXPECT().Wants(domain.WebhookEventMRCreated).Return(true)
	sink.EXPECT().Handle(ctx, IssueEvent{Event: domain.WebhookEventMRCreated, ProjectID: 1, MR: mr}).Return(nil)
	require.NoError(t, deps.emitter.EmitMergeRequest(ctx, domain.WebhookEventMRCreated, 1, mr, 7))

	branch := &domain.Branch{ID: 4, Name: "GAME-1"}
	sink.EXPECT().Wants(domain.WebhookEventBranchCreated).Return(true)
	sink.EXPECT().Handle(ctx, IssueEvent{Event: domain.WebhookEventBranchCreated, ProjectID: 1, Branch: branch}).Return(nil)
	require.NoError(t, deps.emitter.EmitBranch(ctx, domain.WebhookEventBranchCreated, 1, branch, 7))

	sink.EXPECT().Wants(domain.WebhookEventMRCommentCreated).Return(false)
	require.NoError(t, deps.emitter.EmitMergeRequest(ctx, domain.WebhookEventMRCommentCreated, 1, mr, 7))
}
