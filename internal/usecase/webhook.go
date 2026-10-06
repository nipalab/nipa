package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/webhook"
)

const (
	webhookSecretBytes = 32
	webhookMaxURLLen   = 2048
	webhookMaxNameLen  = 100

	defaultDeliveryPageSize = 50
	maxDeliveryPageSize     = 200
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=webhook_mock_test.go -package=usecase
type webhookRepository interface {
	Create(ctx context.Context, webhook domain.Webhook) (*domain.Webhook, error)
	Get(ctx context.Context, projectID, id snow.ID) (*domain.Webhook, error)
	ListByProject(ctx context.Context, projectID snow.ID) ([]*domain.Webhook, error)
	Update(ctx context.Context, webhook domain.Webhook) (*domain.Webhook, error)
	RotateSecret(ctx context.Context, projectID, id snow.ID, secret string) (*domain.Webhook, error)
	Delete(ctx context.Context, projectID, id snow.ID) error
	GetDelivery(ctx context.Context, webhookID, id snow.ID) (*domain.WebhookDelivery, error)
	ListDeliveries(ctx context.Context, webhookID snow.ID, limit, offset int64) ([]*domain.WebhookDelivery, error)
	Requeue(ctx context.Context, webhookID, id snow.ID, at time.Time) (*domain.WebhookDelivery, error)
}

// webhookDispatcher is the slice of the delivery dispatcher the usecase drives.
type webhookDispatcher interface {
	Enqueue(ctx context.Context, hook domain.Webhook, event string, payload []byte) (*domain.WebhookDelivery, error)
	Schedule(hook domain.Webhook, delivery domain.WebhookDelivery)
}

var webhookEventTypes = map[string]struct{}{
	domain.WebhookEventPush:           {},
	domain.WebhookEventBranchCreated:  {},
	domain.WebhookEventBranchDeleted:  {},
	domain.WebhookEventTagCreated:     {},
	domain.WebhookEventTagDeleted:     {},
	domain.WebhookEventMRCreated:      {},
	domain.WebhookEventMRUpdated:      {},
	domain.WebhookEventMRSynchronized: {},
	domain.WebhookEventMRMerged:       {},
	domain.WebhookEventMRClosed:       {},
	domain.WebhookEventMRReopened:     {},

	domain.WebhookEventMRReviewSubmitted:   {},
	domain.WebhookEventMRReviewDismissed:   {},
	domain.WebhookEventMRReviewRequested:   {},
	domain.WebhookEventMRReviewUnrequested: {},
	domain.WebhookEventMRCommentCreated:    {},
}

// WebhookInput describes a new webhook endpoint.
type WebhookInput struct {
	Name        string
	URL         string
	Events      []string
	PathPrefix  string
	IsActive    bool
	InsecureTLS bool
}

// WebhookUpdate patches an existing webhook; nil fields are left untouched.
type WebhookUpdate struct {
	Name        *string
	URL         *string
	Events      *[]string
	PathPrefix  *string
	IsActive    *bool
	InsecureTLS *bool
}

// Webhook manages project-scoped webhook endpoints. Managing them requires
// project admin rights; delivery is handled by the webhook dispatcher.
type Webhook struct {
	repo       webhookRepository
	perm       permissionUsecase
	users      userLookup
	dispatcher webhookDispatcher
	snowNode   snow.Node
}

func NewWebhook(repo webhookRepository, perm permissionUsecase, users userLookup, dispatcher webhookDispatcher, snowNode snow.Node) *Webhook {
	return &Webhook{
		repo:       repo,
		perm:       perm,
		users:      users,
		dispatcher: dispatcher,
		snowNode:   snowNode,
	}
}

func (w *Webhook) Create(ctx context.Context, projectID snow.ID, input WebhookInput) (*domain.Webhook, error) {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	normalizedURL, err := normalizeWebhookURL(input.URL)
	if err != nil {
		return nil, err
	}
	name, err := normalizeWebhookName(input.Name)
	if err != nil {
		return nil, err
	}
	events, err := normalizeWebhookEvents(input.Events)
	if err != nil {
		return nil, err
	}
	prefix, err := domain.NormalizePathPrefix(input.PathPrefix)
	if err != nil {
		return nil, domain.NewErrorUser(err.Error())
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		return nil, domain.NewErrorInternalServer("generate webhook secret: " + err.Error())
	}
	created, err := w.repo.Create(ctx, domain.Webhook{
		ID:          w.snowNode.Generate(),
		ProjectID:   projectID,
		Name:        name,
		URL:         normalizedURL,
		Secret:      secret,
		Events:      events,
		PathPrefix:  prefix,
		IsActive:    input.IsActive,
		InsecureTLS: input.InsecureTLS,
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (w *Webhook) List(ctx context.Context, projectID snow.ID) ([]*domain.Webhook, error) {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	webhooks, err := w.repo.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if webhooks == nil {
		webhooks = []*domain.Webhook{}
	}
	return webhooks, nil
}

func (w *Webhook) Get(ctx context.Context, projectID, id snow.ID) (*domain.Webhook, error) {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	return w.repo.Get(ctx, projectID, id)
}

func (w *Webhook) Update(ctx context.Context, projectID, id snow.ID, patch WebhookUpdate) (*domain.Webhook, error) {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	current, err := w.repo.Get(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	if patch.Name != nil {
		name, err := normalizeWebhookName(*patch.Name)
		if err != nil {
			return nil, err
		}
		current.Name = name
	}
	if patch.URL != nil {
		normalizedURL, err := normalizeWebhookURL(*patch.URL)
		if err != nil {
			return nil, err
		}
		current.URL = normalizedURL
	}
	if patch.Events != nil {
		events, err := normalizeWebhookEvents(*patch.Events)
		if err != nil {
			return nil, err
		}
		current.Events = events
	}
	if patch.PathPrefix != nil {
		prefix, err := domain.NormalizePathPrefix(*patch.PathPrefix)
		if err != nil {
			return nil, domain.NewErrorUser(err.Error())
		}
		current.PathPrefix = prefix
	}
	if patch.IsActive != nil {
		current.IsActive = *patch.IsActive
	}
	if patch.InsecureTLS != nil {
		current.InsecureTLS = *patch.InsecureTLS
	}
	return w.repo.Update(ctx, *current)
}

func (w *Webhook) Delete(ctx context.Context, projectID, id snow.ID) error {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return err
	}
	return w.repo.Delete(ctx, projectID, id)
}

// RotateSecret replaces the signing secret and returns the webhook with the
// new secret populated. The secret is only ever shown on create and rotate.
func (w *Webhook) RotateSecret(ctx context.Context, projectID, id snow.ID) (*domain.Webhook, error) {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	secret, err := generateWebhookSecret()
	if err != nil {
		return nil, domain.NewErrorInternalServer("generate webhook secret: " + err.Error())
	}
	return w.repo.RotateSecret(ctx, projectID, id, secret)
}

func (w *Webhook) requireAdmin(ctx context.Context, projectID snow.ID) error {
	if !w.perm.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	return nil
}

// Test sends a ping delivery to the hook so an admin can verify the endpoint
// and its signature handling. The hook is exercised even when inactive.
func (w *Webhook) Test(ctx context.Context, org *domain.Organization, project *domain.Project, id snow.ID) (*domain.WebhookDelivery, error) {
	if err := w.requireAdmin(ctx, project.ID); err != nil {
		return nil, err
	}
	hook, err := w.repo.Get(ctx, project.ID, id)
	if err != nil {
		return nil, err
	}
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	actor := webhook.Actor{ID: claim.UserID.Base36()}
	if user, err := w.users.GetByID(ctx, claim.UserID); err == nil {
		actor.Username = user.Name
	}
	payload, err := json.Marshal(webhook.Envelope{
		Event:        domain.WebhookEventPing,
		Timestamp:    time.Now().UTC(),
		Actor:        actor,
		Organization: webhook.Organization{Slug: org.Slug},
		Project:      webhook.Project{Slug: project.Slug},
		WebhookID:    hook.ID.Base36(),
	})
	if err != nil {
		return nil, domain.NewErrorInternalServer("encode webhook payload: " + err.Error())
	}
	return w.dispatcher.Enqueue(ctx, *hook, domain.WebhookEventPing, payload)
}

// Deliveries lists recent delivery attempts, newest first.
func (w *Webhook) Deliveries(ctx context.Context, projectID, id snow.ID, limit, offset int64) ([]*domain.WebhookDelivery, error) {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	if _, err := w.repo.Get(ctx, projectID, id); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = defaultDeliveryPageSize
	}
	if limit > maxDeliveryPageSize {
		limit = maxDeliveryPageSize
	}
	if offset < 0 {
		offset = 0
	}
	deliveries, err := w.repo.ListDeliveries(ctx, id, limit, offset)
	if err != nil {
		return nil, err
	}
	if deliveries == nil {
		deliveries = []*domain.WebhookDelivery{}
	}
	return deliveries, nil
}

// Redeliver re-queues a stored delivery, keeping its attempt history.
func (w *Webhook) Redeliver(ctx context.Context, projectID, id, deliveryID snow.ID) (*domain.WebhookDelivery, error) {
	if err := w.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	hook, err := w.repo.Get(ctx, projectID, id)
	if err != nil {
		return nil, err
	}
	if _, err := w.repo.GetDelivery(ctx, hook.ID, deliveryID); err != nil {
		return nil, err
	}
	requeued, err := w.repo.Requeue(ctx, hook.ID, deliveryID, time.Now())
	if err != nil {
		return nil, err
	}
	w.dispatcher.Schedule(*hook, *requeued)
	return requeued, nil
}

func normalizeWebhookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > webhookMaxURLLen {
		return "", domain.NewErrorUser("webhook url is too long")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", domain.NewErrorUser("webhook url must be an absolute http(s) url")
	}
	if parsed.Fragment != "" {
		return "", domain.NewErrorUser("webhook url must not contain a fragment")
	}
	if parsed.User != nil {
		return "", domain.NewErrorUser("webhook url must not contain credentials")
	}
	return raw, nil
}

func normalizeWebhookName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if len(name) > webhookMaxNameLen {
		return "", domain.NewErrorUser("webhook name is too long")
	}
	return name, nil
}

func normalizeWebhookEvents(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, domain.NewErrorUser("at least one webhook event is required")
	}
	seen := make(map[string]struct{}, len(raw))
	events := make([]string, 0, len(raw))
	for _, entry := range raw {
		event := strings.TrimSpace(entry)
		if _, ok := webhookEventTypes[event]; !ok {
			return nil, domain.NewErrorUser(fmt.Sprintf("unknown webhook event %q", event))
		}
		if _, ok := seen[event]; ok {
			continue
		}
		seen[event] = struct{}{}
		events = append(events, event)
	}
	sort.Strings(events)
	return events, nil
}

func generateWebhookSecret() (string, error) {
	buf := make([]byte, webhookSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
