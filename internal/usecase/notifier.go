package usecase

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/mail"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/webhook"
)

// notificationRecipientLimit caps how many recipients one event fans out to.
const notificationRecipientLimit = 200

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=notifier_mock_test.go -package=usecase
type notifierOutbox interface {
	Enqueue(ctx context.Context, deliveries []*domain.EmailDelivery) error
}

type notifierMRRepository interface {
	ListAssignees(ctx context.Context, projectID snow.ID) (map[int64][]domain.ReviewActor, error)
}

type notifierReviewRepository interface {
	ListReviewRequests(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestReviewRequest, error)
	ListComments(ctx context.Context, mergeRequestID int64) ([]*domain.MergeRequestComment, error)
}

// NotifyEvent carries the resolved event context to the notifier.
type NotifyEvent struct {
	Event     string
	ProjectID snow.ID
	MR        *domain.MergeRequest
	Actor     snow.ID
	Envelope  webhook.Envelope
	Check     *domain.MergeRequestCheck
}

// EmailNotifier renders merge request notifications and queues them in the
// durable outbox. A nil notifier disables email; wiring is optional through
// HookEmitter.WithNotifier.
type EmailNotifier struct {
	outbox  notifierOutbox
	users   userLookup
	mrs     notifierMRRepository
	reviews notifierReviewRepository
	baseURL string
	kick    dispatcherKicker
	now     func() time.Time
	node    snow.Node
}

type dispatcherKicker interface {
	Kick()
}

func NewEmailNotifier(outbox notifierOutbox, users userLookup, mrs notifierMRRepository,
	reviews notifierReviewRepository, baseURL string, node snow.Node,
) *EmailNotifier {
	return &EmailNotifier{
		outbox:  outbox,
		users:   users,
		mrs:     mrs,
		reviews: reviews,
		baseURL: baseURL,
		now:     time.Now,
		node:    node,
	}
}

// WithKicker asks the outbox dispatcher to poll immediately after an enqueue.
func (n *EmailNotifier) WithKicker(kicker dispatcherKicker) *EmailNotifier {
	n.kick = kicker
	return n
}

// notifierEvents is the set of events that produce email.
var notifierEvents = map[string]bool{
	domain.WebhookEventMRCreated:         true,
	domain.WebhookEventMRReady:           true,
	domain.WebhookEventMRReviewRequested: true,
	domain.WebhookEventMRReviewSubmitted: true,
	domain.WebhookEventMRCommentCreated:  true,
	domain.WebhookEventMRSynchronized:    true,
	domain.WebhookEventMRMerged:          true,
	domain.WebhookEventMRClosed:          true,
	domain.WebhookEventMRReopened:        true,
	domain.WebhookEventMRCheckReported:   true,
}

// Wants reports whether the event produces email at all.
func (n *EmailNotifier) Wants(event string) bool {
	return notifierEvents[event]
}

// Notify resolves the participants of the event, renders one message per
// recipient and queues the deliveries in the outbox.
func (n *EmailNotifier) Notify(ctx context.Context, event NotifyEvent) error {
	if !n.Wants(event.Event) || event.MR == nil {
		return nil
	}
	if event.Event == domain.WebhookEventMRCheckReported &&
		(event.Check == nil || event.Check.State != domain.MergeRequestCheckFailed) {
		return nil
	}
	recipients, err := n.recipients(ctx, event)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return nil
	}
	rendered := n.render(event)
	deliveries := make([]*domain.EmailDelivery, 0, len(recipients))
	for _, recipient := range recipients {
		body, err := mail.EncodeMessage(mail.Message{
			To:         []string{recipient.Email},
			Subject:    rendered.subject,
			Text:       rendered.text,
			HTML:       rendered.html,
			MessageID:  n.messageID(event, recipient.ID),
			InReplyTo:  n.threadRoot(event),
			References: []string{n.threadRoot(event)},
		})
		if err != nil {
			return err
		}
		now := n.now()
		deliveries = append(deliveries, &domain.EmailDelivery{
			ID:            n.node.Generate(),
			Event:         event.Event,
			ProjectID:     event.ProjectID,
			UserID:        recipient.ID,
			Email:         recipient.Email,
			Subject:       rendered.subject,
			Body:          body,
			State:         domain.EmailDeliveryPending,
			NextAttemptAt: &now,
		})
	}
	if err := n.outbox.Enqueue(ctx, deliveries); err != nil {
		return err
	}
	if n.kick != nil {
		n.kick.Kick()
	}
	return nil
}

// recipients resolves the participant set of the event to deliverable users:
// the actor, users without an email, deleted users and users who opted out are
// skipped, duplicates collapse and the fan-out is capped.
func (n *EmailNotifier) recipients(ctx context.Context, event NotifyEvent) ([]*domain.User, error) {
	ids, err := n.recipientIDs(ctx, event)
	if err != nil {
		return nil, err
	}
	seen := make(map[snow.ID]struct{}, len(ids))
	recipients := make([]*domain.User, 0, len(ids))
	for _, id := range ids {
		if id == 0 || id == event.Actor {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		user, err := n.users.GetByID(ctx, id)
		if err != nil {
			if domain.IsErrorNotFound(err) {
				continue
			}
			return nil, err
		}
		if user == nil || user.Deleted || !user.NotifyEmail || strings.TrimSpace(user.Email) == "" {
			continue
		}
		recipients = append(recipients, user)
		if len(recipients) >= notificationRecipientLimit {
			break
		}
	}
	return recipients, nil
}

func (n *EmailNotifier) recipientIDs(ctx context.Context, event NotifyEvent) ([]snow.ID, error) {
	mr := event.MR
	var ids []snow.ID
	switch event.Event {
	case domain.WebhookEventMRCreated, domain.WebhookEventMRReady, domain.WebhookEventMRReviewRequested:
		ids = append(ids, mr.CreatedBy)
		reviewers, err := n.reviewerIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, reviewers...)
		assignees, err := n.assigneeIDs(ctx, event.ProjectID, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, assignees...)
	case domain.WebhookEventMRReviewSubmitted:
		ids = append(ids, mr.CreatedBy)
		assignees, err := n.assigneeIDs(ctx, event.ProjectID, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, assignees...)
		commenters, err := n.commenterIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, commenters...)
	case domain.WebhookEventMRCommentCreated:
		ids = append(ids, mr.CreatedBy)
		reviewers, err := n.reviewerIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, reviewers...)
		assignees, err := n.assigneeIDs(ctx, event.ProjectID, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, assignees...)
		commenters, err := n.commenterIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, commenters...)
	case domain.WebhookEventMRSynchronized:
		ids = append(ids, mr.CreatedBy)
		reviewers, err := n.reviewerIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, reviewers...)
		assignees, err := n.assigneeIDs(ctx, event.ProjectID, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, assignees...)
	case domain.WebhookEventMRMerged, domain.WebhookEventMRClosed, domain.WebhookEventMRReopened:
		ids = append(ids, mr.CreatedBy)
		reviewers, err := n.reviewerIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, reviewers...)
		assignees, err := n.assigneeIDs(ctx, event.ProjectID, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, assignees...)
		commenters, err := n.commenterIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, commenters...)
	case domain.WebhookEventMRCheckReported:
		ids = append(ids, mr.CreatedBy)
		reviewers, err := n.reviewerIDs(ctx, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, reviewers...)
		assignees, err := n.assigneeIDs(ctx, event.ProjectID, mr)
		if err != nil {
			return nil, err
		}
		ids = append(ids, assignees...)
	}
	return ids, nil
}

func (n *EmailNotifier) reviewerIDs(ctx context.Context, mr *domain.MergeRequest) ([]snow.ID, error) {
	requests, err := n.reviews.ListReviewRequests(ctx, mr.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]snow.ID, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.Reviewer.UserID)
	}
	return ids, nil
}

func (n *EmailNotifier) assigneeIDs(ctx context.Context, projectID snow.ID, mr *domain.MergeRequest) ([]snow.ID, error) {
	assignees, err := n.mrs.ListAssignees(ctx, projectID)
	if err != nil {
		return nil, err
	}
	actors := assignees[mr.Number]
	ids := make([]snow.ID, 0, len(actors))
	for _, actor := range actors {
		ids = append(ids, actor.UserID)
	}
	return ids, nil
}

func (n *EmailNotifier) commenterIDs(ctx context.Context, mr *domain.MergeRequest) ([]snow.ID, error) {
	comments, err := n.reviews.ListComments(ctx, mr.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]snow.ID, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.User.UserID)
	}
	return ids, nil
}

type renderedNotification struct {
	subject string
	text    string
	html    string
}

var notifierCopy = map[string]struct{ subject, action string }{
	domain.WebhookEventMRCreated:         {subject: "Merge request opened", action: "opened"},
	domain.WebhookEventMRReady:           {subject: "Merge request ready for review", action: "marked ready for review"},
	domain.WebhookEventMRReviewRequested: {subject: "Review requested", action: "requested a review on"},
	domain.WebhookEventMRReviewSubmitted: {subject: "Review submitted", action: "reviewed"},
	domain.WebhookEventMRCommentCreated:  {subject: "New comment", action: "commented on"},
	domain.WebhookEventMRSynchronized:    {subject: "New pushes", action: "pushed to"},
	domain.WebhookEventMRMerged:          {subject: "Merge request merged", action: "merged"},
	domain.WebhookEventMRClosed:          {subject: "Merge request closed", action: "closed"},
	domain.WebhookEventMRReopened:        {subject: "Merge request reopened", action: "reopened"},
	domain.WebhookEventMRCheckReported:   {subject: "Status check failed", action: "reported a failed status check on"},
}

func (n *EmailNotifier) render(event NotifyEvent) renderedNotification {
	text := notifierCopy[event.Event]
	actor := strings.TrimSpace(event.Envelope.Actor.Username)
	if actor == "" {
		actor = "someone"
	}
	org := event.Envelope.Organization.Slug
	project := event.Envelope.Project.Slug
	subject := fmt.Sprintf("[%s] %s !%d: %s", project, text.subject, event.MR.Number, event.MR.Title)
	summary := fmt.Sprintf("%s %s merge request !%d \"%s\" in %s/%s.",
		actor, text.action, event.MR.Number, event.MR.Title, org, project)
	link := n.mergeRequestURL(event)

	plain := summary + "\n"
	if link != "" {
		plain += "\nView it: " + link + "\n"
	}
	var htmlBody strings.Builder
	fmt.Fprintf(&htmlBody, "<p>%s</p>", html.EscapeString(summary))
	if link != "" {
		fmt.Fprintf(&htmlBody, `<p><a href="%s">View merge request</a></p>`, html.EscapeString(link))
	}
	return renderedNotification{subject: subject, text: plain, html: htmlBody.String()}
}

func (n *EmailNotifier) mergeRequestURL(event NotifyEvent) string {
	base := strings.TrimRight(strings.TrimSpace(n.baseURL), "/")
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/%s/merges/%d", base,
		event.Envelope.Organization.Slug, event.Envelope.Project.Slug, event.MR.Number)
}

// messageHost is the domain used in generated Message-IDs.
func (n *EmailNotifier) messageHost() string {
	if parsed, err := url.Parse(n.baseURL); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return "nipa.local"
}

// threadRoot is the stable Message-ID every notification about one merge
// request references, so mail clients group the conversation.
func (n *EmailNotifier) threadRoot(event NotifyEvent) string {
	return fmt.Sprintf("<nipa-%s-%s-mr%d@%s>", event.Envelope.Organization.Slug,
		event.Envelope.Project.Slug, event.MR.Number, n.messageHost())
}

func (n *EmailNotifier) messageID(event NotifyEvent, recipient snow.ID) string {
	return fmt.Sprintf("<nipa-%s-%s-mr%d-%s@%s>", event.Envelope.Organization.Slug,
		event.Envelope.Project.Slug, event.MR.Number, recipient.Base36(), n.messageHost())
}
