package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/webhook"
)

type notifierDeps struct {
	notifier *EmailNotifier
	outbox   *MocknotifierOutbox
	users    *MockuserLookup
	mrs      *MocknotifierMRRepository
	reviews  *MocknotifierReviewRepository
	kicker   *fakeKicker
}

type fakeKicker struct {
	kicks int
}

func (k *fakeKicker) Kick() { k.kicks++ }

func newNotifierDeps(t *testing.T) *notifierDeps {
	t.Helper()
	ctrl := gomock.NewController(t)
	deps := &notifierDeps{
		outbox:  NewMocknotifierOutbox(ctrl),
		users:   NewMockuserLookup(ctrl),
		mrs:     NewMocknotifierMRRepository(ctrl),
		reviews: NewMocknotifierReviewRepository(ctrl),
		kicker:  &fakeKicker{},
	}
	deps.notifier = NewEmailNotifier(deps.outbox, deps.users, deps.mrs, deps.reviews,
		"https://nipa.example.com", newTestBranchNode(t)).WithKicker(deps.kicker)
	deps.notifier.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	return deps
}

func testNotifyEvent(event string) NotifyEvent {
	return NotifyEvent{
		Event:     event,
		ProjectID: 1,
		MR: &domain.MergeRequest{
			ID:           5,
			Number:       42,
			Title:        "Fix textures",
			Status:       domain.MergeRequestOpen,
			SourceBranch: "feature",
			TargetBranch: "main",
			CreatedBy:    10,
		},
		Actor: 99,
		Envelope: webhook.Envelope{
			Event:        event,
			Organization: webhook.Organization{Slug: "acme"},
			Project:      webhook.Project{Slug: "game"},
			Actor:        webhook.Actor{ID: snow.ID(99).Base36(), Username: "alice"},
		},
	}
}

func testNotifierUser(id snow.ID, email string) *domain.User {
	return &domain.User{ID: id, Name: "user", Email: email, NotifyEmail: true}
}

func TestEmailNotifier_Wants(t *testing.T) {
	deps := newNotifierDeps(t)
	for _, event := range []string{
		domain.WebhookEventMRCreated,
		domain.WebhookEventMRReady,
		domain.WebhookEventMRReviewRequested,
		domain.WebhookEventMRReviewSubmitted,
		domain.WebhookEventMRCommentCreated,
		domain.WebhookEventMRSynchronized,
		domain.WebhookEventMRMerged,
		domain.WebhookEventMRClosed,
		domain.WebhookEventMRReopened,
		domain.WebhookEventMRCheckReported,
	} {
		require.True(t, deps.notifier.Wants(event), event)
	}
	require.False(t, deps.notifier.Wants(domain.WebhookEventPush))
	require.False(t, deps.notifier.Wants(domain.WebhookEventTagCreated))
	require.False(t, deps.notifier.Wants(domain.WebhookEventMRUpdated))
}

func TestEmailNotifier_CommentRecipients(t *testing.T) {
	deps := newNotifierDeps(t)
	event := testNotifyEvent(domain.WebhookEventMRCommentCreated)

	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).
		Return([]*domain.MergeRequestReviewRequest{{Reviewer: domain.ReviewActor{UserID: 20}}}, nil)
	deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).
		Return([]domain.ReviewActor{{UserID: 30}}, nil)
	deps.reviews.EXPECT().ListComments(gomock.Any(), int64(5)).
		Return([]*domain.MergeRequestComment{
			{User: domain.ReviewActor{UserID: 40}},
			{User: domain.ReviewActor{UserID: 10}},
			{User: domain.ReviewActor{UserID: 40}},
		}, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(10)).
		Return(testNotifierUser(10, "author@example.com"), nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(20)).
		Return(testNotifierUser(20, "reviewer@example.com"), nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(30)).
		Return(testNotifierUser(30, "assignee@example.com"), nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(40)).
		Return(testNotifierUser(40, "commenter@example.com"), nil)

	var captured []*domain.EmailDelivery
	deps.outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(1), "acme/game/mr42").Return(nil, nil)
	deps.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, deliveries []*domain.EmailDelivery) error {
			captured = deliveries
			return nil
		})

	require.NoError(t, deps.notifier.Notify(context.Background(), event))
	require.Len(t, captured, 4, "author, reviewer, assignee and deduplicated commenter")

	byEmail := map[string]*domain.EmailDelivery{}
	for _, delivery := range captured {
		byEmail[delivery.Email] = delivery
		require.Equal(t, domain.WebhookEventMRCommentCreated, delivery.Event)
		require.Equal(t, snow.ID(1), delivery.ProjectID)
		require.Equal(t, domain.EmailDeliveryPending, delivery.State)
		require.NotNil(t, delivery.NextAttemptAt)
		require.Equal(t, time.Unix(1000, 0).UTC(), *delivery.NextAttemptAt)
		require.Contains(t, delivery.Subject, "[game] New comment !42: Fix textures")
		require.NotEqual(t, snow.ID(0), delivery.ID)
	}
	require.Equal(t, snow.ID(10), byEmail["author@example.com"].UserID)
	require.Equal(t, snow.ID(20), byEmail["reviewer@example.com"].UserID)
	require.Equal(t, snow.ID(30), byEmail["assignee@example.com"].UserID)
	require.Equal(t, snow.ID(40), byEmail["commenter@example.com"].UserID)
	require.Equal(t, 1, deps.kicker.kicks)

	body, err := mail.DecodeMessage(byEmail["author@example.com"].Body)
	require.NoError(t, err)
	require.Equal(t, []string{"author@example.com"}, body.To)
	require.Contains(t, body.Text, "alice commented on merge request !42 \"Fix textures\" in acme/game.")
	require.Contains(t, body.Text, "https://nipa.example.com/acme/game/merges/42")
	require.Contains(t, body.HTML, `href="https://nipa.example.com/acme/game/merges/42"`)
	require.Equal(t, "<nipa-acme-game-mr42@nipa.example.com>", body.MessageID, "the first contact carries the thread root")
	require.Empty(t, body.InReplyTo)
	require.Empty(t, body.References)
	for _, delivery := range captured {
		require.Equal(t, "acme/game/mr42", delivery.ThreadKey)
	}
}

func TestEmailNotifier_SkipsActorAndUndeliverableUsers(t *testing.T) {
	deps := newNotifierDeps(t)
	event := testNotifyEvent(domain.WebhookEventMRCommentCreated)
	event.Actor = 10 // the author is the actor now

	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).
		Return([]*domain.MergeRequestReviewRequest{{Reviewer: domain.ReviewActor{UserID: 20}}}, nil)
	deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).
		Return([]domain.ReviewActor{{UserID: 30}}, nil)
	deps.reviews.EXPECT().ListComments(gomock.Any(), int64(5)).
		Return([]*domain.MergeRequestComment{
			{User: domain.ReviewActor{UserID: 40}},
			{User: domain.ReviewActor{UserID: 50}},
		}, nil)
	// The actor is never looked up.
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(20)).
		Return(&domain.User{ID: 20, Email: "reviewer@example.com", NotifyEmail: false}, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(30)).
		Return(&domain.User{ID: 30, Email: "", NotifyEmail: true}, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(40)).
		Return(&domain.User{ID: 40, Email: "gone@example.com", NotifyEmail: true, Deleted: true}, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(50)).
		Return(nil, domain.NewErrorRecordNotFound())

	require.NoError(t, deps.notifier.Notify(context.Background(), event))
	require.Zero(t, deps.kicker.kicks, "no deliveries means no kick")
}

func TestEmailNotifier_CheckEvent(t *testing.T) {
	deps := newNotifierDeps(t)

	success := testNotifyEvent(domain.WebhookEventMRCheckReported)
	success.Check = &domain.MergeRequestCheck{Name: "build", State: domain.MergeRequestCheckSuccess}
	require.NoError(t, deps.notifier.Notify(context.Background(), success), "successful checks do not email")

	missing := testNotifyEvent(domain.WebhookEventMRCheckReported)
	require.NoError(t, deps.notifier.Notify(context.Background(), missing), "a check event without a check does not email")

	failed := testNotifyEvent(domain.WebhookEventMRCheckReported)
	failed.Check = &domain.MergeRequestCheck{Name: "build", State: domain.MergeRequestCheckFailed}
	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).Return(nil, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(10)).
		Return(testNotifierUser(10, "author@example.com"), nil)
	deps.outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(1), "acme/game/mr42").Return(nil, nil)
	deps.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, deliveries []*domain.EmailDelivery) error {
			require.Len(t, deliveries, 1)
			require.Contains(t, deliveries[0].Subject, "Status check failed !42")
			return nil
		})
	require.NoError(t, deps.notifier.Notify(context.Background(), failed))
}

func TestEmailNotifier_RecipientLimit(t *testing.T) {
	deps := newNotifierDeps(t)
	event := testNotifyEvent(domain.WebhookEventMRCreated)
	event.MR.CreatedBy = 0

	requests := make([]*domain.MergeRequestReviewRequest, 0, notificationRecipientLimit+50)
	for i := 0; i < notificationRecipientLimit+50; i++ {
		requests = append(requests, &domain.MergeRequestReviewRequest{
			Reviewer: domain.ReviewActor{UserID: snow.ID(1000 + i)},
		})
	}
	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(requests, nil)
	deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).Return(nil, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, id snow.ID) (*domain.User, error) {
			return testNotifierUser(id, fmt.Sprintf("user%d@example.com", id)), nil
		}).AnyTimes()

	deps.outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(1), "acme/game/mr42").Return(nil, nil)
	deps.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, deliveries []*domain.EmailDelivery) error {
			require.Len(t, deliveries, notificationRecipientLimit)
			return nil
		})
	require.NoError(t, deps.notifier.Notify(context.Background(), event))
}

func TestEmailNotifier_EnqueueError(t *testing.T) {
	deps := newNotifierDeps(t)
	event := testNotifyEvent(domain.WebhookEventMRReviewRequested)
	event.MR.CreatedBy = 0

	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).
		Return([]*domain.MergeRequestReviewRequest{{Reviewer: domain.ReviewActor{UserID: 20}}}, nil)
	deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).Return(nil, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(20)).
		Return(testNotifierUser(20, "reviewer@example.com"), nil)
	deps.outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(1), "acme/game/mr42").Return(nil, nil)
	deps.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).Return(errors.New("db down"))

	err := deps.notifier.Notify(context.Background(), event)
	require.ErrorContains(t, err, "db down")
	require.Zero(t, deps.kicker.kicks, "no kick after a failed enqueue")
}

func TestEmailNotifier_NoBaseURL(t *testing.T) {
	deps := newNotifierDeps(t)
	deps.notifier.baseURL = ""
	event := testNotifyEvent(domain.WebhookEventMRMerged)
	event.MR.CreatedBy = 0

	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).Return(nil, nil)
	deps.reviews.EXPECT().ListComments(gomock.Any(), int64(5)).
		Return([]*domain.MergeRequestComment{{User: domain.ReviewActor{UserID: 20}}}, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(20)).
		Return(testNotifierUser(20, "reviewer@example.com"), nil)

	var captured []*domain.EmailDelivery
	deps.outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(1), "acme/game/mr42").Return(nil, nil)
	deps.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, deliveries []*domain.EmailDelivery) error {
			captured = deliveries
			return nil
		})
	require.NoError(t, deps.notifier.Notify(context.Background(), event))
	require.Len(t, captured, 1)

	body, err := mail.DecodeMessage(captured[0].Body)
	require.NoError(t, err)
	require.NotContains(t, body.Text, "http")
	require.NotContains(t, body.HTML, "href")
	require.Equal(t, "<nipa-acme-game-mr42@nipa.local>", body.MessageID)
	require.Empty(t, body.InReplyTo)
}

func TestEmailNotifier_ThreadsReplies(t *testing.T) {
	deps := newNotifierDeps(t)
	event := testNotifyEvent(domain.WebhookEventMRCommentCreated)

	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).Return(nil, nil)
	deps.reviews.EXPECT().ListComments(gomock.Any(), int64(5)).
		Return([]*domain.MergeRequestComment{{User: domain.ReviewActor{UserID: 20}}}, nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(10)).
		Return(testNotifierUser(10, "author@example.com"), nil)
	deps.users.EXPECT().GetByID(gomock.Any(), snow.ID(20)).
		Return(testNotifierUser(20, "reviewer@example.com"), nil)
	deps.outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(1), "acme/game/mr42").
		Return(map[snow.ID]struct{}{20: {}}, nil)

	var captured []*domain.EmailDelivery
	deps.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, deliveries []*domain.EmailDelivery) error {
			captured = deliveries
			return nil
		})

	require.NoError(t, deps.notifier.Notify(context.Background(), event))
	require.Len(t, captured, 2)

	root := "<nipa-acme-game-mr42@nipa.example.com>"
	byEmail := map[string]*domain.EmailDelivery{}
	for _, delivery := range captured {
		byEmail[delivery.Email] = delivery
	}
	first, err := mail.DecodeMessage(byEmail["author@example.com"].Body)
	require.NoError(t, err)
	require.Equal(t, root, first.MessageID, "a recipient without prior mail gets the root")
	require.Empty(t, first.InReplyTo)

	reply, err := mail.DecodeMessage(byEmail["reviewer@example.com"].Body)
	require.NoError(t, err)
	require.NotEqual(t, root, reply.MessageID, "a reply gets a unique message id")
	require.Equal(t, root, reply.InReplyTo)
	require.Equal(t, []string{root}, reply.References)
}

func TestEmailNotifier_RecipientSets(t *testing.T) {
	cases := []struct {
		event      string
		reviewers  bool
		assignees  bool
		commenters bool
	}{
		{domain.WebhookEventMRCreated, true, true, false},
		{domain.WebhookEventMRReady, true, true, false},
		{domain.WebhookEventMRReviewRequested, true, true, false},
		{domain.WebhookEventMRReviewSubmitted, false, true, true},
		{domain.WebhookEventMRSynchronized, true, true, false},
		{domain.WebhookEventMRMerged, true, true, true},
		{domain.WebhookEventMRClosed, true, true, true},
		{domain.WebhookEventMRReopened, true, true, true},
		{domain.WebhookEventMRCheckReported, true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			deps := newNotifierDeps(t)
			event := testNotifyEvent(tc.event)
			if tc.event == domain.WebhookEventMRCheckReported {
				event.Check = &domain.MergeRequestCheck{Name: "build", State: domain.MergeRequestCheckFailed}
			}
			if tc.reviewers {
				deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).
					Return([]*domain.MergeRequestReviewRequest{{Reviewer: domain.ReviewActor{UserID: 20}}}, nil)
			}
			if tc.assignees {
				deps.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).
					Return([]domain.ReviewActor{{UserID: 30}}, nil)
			}
			if tc.commenters {
				deps.reviews.EXPECT().ListComments(gomock.Any(), int64(5)).
					Return([]*domain.MergeRequestComment{{User: domain.ReviewActor{UserID: 40}}}, nil)
			}
			deps.users.EXPECT().GetByID(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, id snow.ID) (*domain.User, error) {
					return testNotifierUser(id, fmt.Sprintf("user%d@example.com", id)), nil
				}).AnyTimes()

			var captured []*domain.EmailDelivery
			deps.outbox.EXPECT().ThreadRecipients(gomock.Any(), snow.ID(1), "acme/game/mr42").Return(nil, nil)
			deps.outbox.EXPECT().Enqueue(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, deliveries []*domain.EmailDelivery) error {
					captured = deliveries
					return nil
				})
			require.NoError(t, deps.notifier.Notify(context.Background(), event))

			recipients := map[snow.ID]bool{}
			for _, delivery := range captured {
				recipients[delivery.UserID] = true
			}
			require.True(t, recipients[snow.ID(10)], "the author is a recipient")
			require.Equal(t, tc.reviewers, recipients[snow.ID(20)], "reviewer recipient")
			require.Equal(t, tc.assignees, recipients[snow.ID(30)], "assignee recipient")
			require.Equal(t, tc.commenters, recipients[snow.ID(40)], "commenter recipient")
		})
	}
}

func TestEmailNotifier_RecipientSourceErrors(t *testing.T) {
	synchronized := newNotifierDeps(t)
	event := testNotifyEvent(domain.WebhookEventMRSynchronized)
	synchronized.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	synchronized.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).Return(nil, errors.New("assignees down"))
	require.ErrorContains(t, synchronized.notifier.Notify(context.Background(), event), "assignees down")

	merged := newNotifierDeps(t)
	event = testNotifyEvent(domain.WebhookEventMRMerged)
	merged.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, nil)
	merged.mrs.EXPECT().ListAssigneesByMergeRequest(gomock.Any(), int64(5)).Return(nil, nil)
	merged.reviews.EXPECT().ListComments(gomock.Any(), int64(5)).Return(nil, errors.New("comments down"))
	require.ErrorContains(t, merged.notifier.Notify(context.Background(), event), "comments down")
}

func TestEmailNotifier_IgnoresUnwantedAndMissingRequests(t *testing.T) {
	deps := newNotifierDeps(t)

	push := testNotifyEvent(domain.WebhookEventPush)
	require.NoError(t, deps.notifier.Notify(context.Background(), push))

	noMR := testNotifyEvent(domain.WebhookEventMRCreated)
	noMR.MR = nil
	require.NoError(t, deps.notifier.Notify(context.Background(), noMR))
	require.Zero(t, deps.kicker.kicks)
}

func TestEmailNotifier_RepoError(t *testing.T) {
	deps := newNotifierDeps(t)
	event := testNotifyEvent(domain.WebhookEventMRReviewRequested)

	deps.reviews.EXPECT().ListReviewRequests(gomock.Any(), int64(5)).Return(nil, errors.New("db down"))
	err := deps.notifier.Notify(context.Background(), event)
	require.ErrorContains(t, err, "db down")
}
