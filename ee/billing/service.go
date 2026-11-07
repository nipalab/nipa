package billing

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// Subscription is the provider-neutral subscription billing applies.
type Subscription = domain.BillingSubscription

type repository interface {
	LockOwner(ctx context.Context, owner snow.ID) error
	CountSeats(ctx context.Context, owner snow.ID) (int64, error)
	IsSeat(ctx context.Context, owner, user snow.ID) (bool, error)
	LiveSubscription(ctx context.Context, user snow.ID) (*domain.BillingSubscription, error)
	UpsertSubscription(ctx context.Context, sub domain.BillingSubscription) (bool, error)
	Account(ctx context.Context, user snow.ID) (*domain.BillingAccount, error)
	AccountByCustomer(ctx context.Context, provider, customerID string) (*domain.BillingAccount, error)
	InsertAccount(ctx context.Context, account domain.BillingAccount) error
	User(ctx context.Context, id snow.ID) (*domain.BillingUser, error)
	InsertEvent(ctx context.Context, event domain.BillingEvent) (bool, error)
	Event(ctx context.Context, eventID string) (*domain.BillingEvent, error)
	MarkEventProcessed(ctx context.Context, eventID string) error
	MarkEventFailed(ctx context.Context, eventID, reason string) error
}

// ledger is the slice of the storage ledger billing drives;
// *storageusage.Ledger satisfies it.
type ledger interface {
	BillingOwner(ctx context.Context, orgID snow.ID) (snow.ID, error)
	SetComputedQuota(ctx context.Context, userID snow.ID, quota int64) error
	OwnerStorage(ctx context.Context, userID snow.ID) (*storageusage.OwnerStorage, error)
}

// transactor runs fn in one transaction; *dbtx.Transactor satisfies it.
type transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service applies subscriptions to entitlements and gates new seats.
type Service struct {
	repo     repository
	ledger   ledger
	tx       transactor
	cfg      Config
	provider Provider
}

func New(repo repository, ledger ledger, tx transactor, cfg Config) *Service {
	return &Service{repo: repo, ledger: ledger, tx: tx, cfg: cfg}
}

// WithProvider attaches the billing provider behind checkout, changes, the
// portal, webhooks and sync.
func (s *Service) WithProvider(provider Provider) *Service {
	s.provider = provider
	return s
}

// Apply stores the subscription unless a newer provider update is already
// stored, then recomputes the user's quota from their live subscription. The
// admin override is never touched.
func (s *Service) Apply(ctx context.Context, sub Subscription) error {
	if strings.TrimSpace(sub.ProviderSubscriptionID) == "" || sub.UserID == 0 || sub.ProviderUpdatedAt.IsZero() {
		return domain.NewErrorUser("subscription id, user and provider update time are required")
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockOwner(ctx, sub.UserID); err != nil {
			return err
		}
		if _, err := s.repo.UpsertSubscription(ctx, sub); err != nil {
			return err
		}
		entitlement, err := s.Entitlement(ctx, sub.UserID)
		if err != nil {
			return err
		}
		return s.ledger.SetComputedQuota(ctx, sub.UserID, entitlement.QuotaBytes)
	})
}

// Entitlement resolves a user's entitlement from their live subscription.
func (s *Service) Entitlement(ctx context.Context, userID snow.ID) (Entitlement, error) {
	sub, err := s.repo.LiveSubscription(ctx, userID)
	if err != nil {
		return Entitlement{}, err
	}
	return s.cfg.Entitlement(sub), nil
}
