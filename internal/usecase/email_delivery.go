package usecase

import (
	"context"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	defaultEmailDeliveryPageSize = 50
	maxEmailDeliveryPageSize     = 200
)

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=email_delivery_mock_test.go -package=usecase
type emailDeliveryRepository interface {
	ListDeliveries(ctx context.Context, projectID snow.ID, state string, after *snow.ID, limit int64) ([]*domain.EmailDelivery, error)
	Get(ctx context.Context, id snow.ID) (*domain.EmailDelivery, error)
	Redeliver(ctx context.Context, projectID, id snow.ID, at time.Time) error
}

// EmailDelivery lists and re-queues outbox deliveries for project admins.
type EmailDelivery struct {
	repo emailDeliveryRepository
	perm permissionUsecase
	kick dispatcherKicker
	now  func() time.Time
}

func NewEmailDelivery(repo emailDeliveryRepository, perm permissionUsecase) *EmailDelivery {
	return &EmailDelivery{repo: repo, perm: perm, now: time.Now}
}

// WithKicker asks the outbox dispatcher to poll immediately after a redelivery.
func (e *EmailDelivery) WithKicker(kicker dispatcherKicker) *EmailDelivery {
	e.kick = kicker
	return e
}

type EmailDeliveryListOptions struct {
	State string
	After *snow.ID
	Limit int64
}

// List returns a page of the project's deliveries, newest first, with the
// keyset cursor of the next page when one exists.
func (e *EmailDelivery) List(ctx context.Context, projectID snow.ID, opts EmailDeliveryListOptions) ([]*domain.EmailDelivery, string, error) {
	if err := e.requireAdmin(ctx, projectID); err != nil {
		return nil, "", err
	}
	state := strings.TrimSpace(opts.State)
	if state != "" && !domain.IsValidEmailDeliveryState(state) {
		return nil, "", domain.NewErrorUser("invalid delivery state")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultEmailDeliveryPageSize
	}
	if limit > maxEmailDeliveryPageSize {
		limit = maxEmailDeliveryPageSize
	}
	deliveries, err := e.repo.ListDeliveries(ctx, projectID, state, opts.After, limit+1)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if int64(len(deliveries)) > limit {
		deliveries = deliveries[:limit]
		next = deliveries[len(deliveries)-1].ID.Base36()
	}
	if deliveries == nil {
		deliveries = []*domain.EmailDelivery{}
	}
	return deliveries, next, nil
}

// Redeliver re-queues a failed or delivered notification. Pending and
// in-flight deliveries cannot be redelivered.
func (e *EmailDelivery) Redeliver(ctx context.Context, projectID, id snow.ID) (*domain.EmailDelivery, error) {
	if err := e.requireAdmin(ctx, projectID); err != nil {
		return nil, err
	}
	delivery, err := e.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if delivery.ProjectID != projectID {
		return nil, domain.NewErrorRecordNotFound()
	}
	switch delivery.State {
	case domain.EmailDeliveryFailed, domain.EmailDeliveryDelivered:
	default:
		return nil, domain.NewErrorConflict("delivery is " + delivery.State + "; only delivered or failed deliveries can be redelivered")
	}
	if err := e.repo.Redeliver(ctx, projectID, id, e.now()); err != nil {
		return nil, err
	}
	if e.kick != nil {
		e.kick.Kick()
	}
	return e.repo.Get(ctx, id)
}

func (e *EmailDelivery) requireAdmin(ctx context.Context, projectID snow.ID) error {
	if !e.perm.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	return nil
}
