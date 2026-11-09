package billing

import (
	"context"
	"maps"
	"strings"

	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
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
	QuotaOwners(ctx context.Context, after snow.ID, limit int64) ([]snow.ID, error)
}

// ledger is the slice of the storage ledger billing drives;
// *storageusage.Ledger satisfies it.
type ledger interface {
	BillingOwner(ctx context.Context, orgID snow.ID) (snow.ID, error)
	SetComputedQuota(ctx context.Context, userID snow.ID, quota int64) error
	OwnerStorage(ctx context.Context, userID snow.ID) (*storageusage.OwnerStorage, error)
	ComputedQuota(ctx context.Context, userID snow.ID) (*int64, error)
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
	audit    usecase.AuditRecorder
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

// WithAudit records billing changes, subscription updates and seat refusals.
func (s *Service) WithAudit(rec usecase.AuditRecorder) *Service {
	s.audit = rec
	return s
}

// Apply stores the subscription unless a newer provider update is already
// stored, then recomputes the user's quota from their live subscription. The
// admin override is never touched.
func (s *Service) Apply(ctx context.Context, sub Subscription) error {
	return s.apply(ctx, sub, nil)
}

func (s *Service) apply(ctx context.Context, sub Subscription, event *Event) error {
	if strings.TrimSpace(sub.ProviderSubscriptionID) == "" || sub.UserID == 0 || sub.ProviderUpdatedAt.IsZero() {
		return domain.NewErrorUser("subscription id, user and provider update time are required")
	}
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockOwner(ctx, sub.UserID); err != nil {
			return err
		}
		before, err := s.auditedState(ctx, sub.UserID)
		if err != nil {
			return err
		}
		written, err := s.repo.UpsertSubscription(ctx, sub)
		if err != nil {
			return err
		}
		entitlement, err := s.Entitlement(ctx, sub.UserID)
		if err != nil {
			return err
		}
		if err := s.ledger.SetComputedQuota(ctx, sub.UserID, entitlement.QuotaBytes); err != nil {
			return err
		}
		if !written || before == nil {
			return nil
		}
		return s.audit.Record(ctx, s.appliedAudit(ctx, sub, before, entitlement, event))
	})
}

type billingState struct {
	live  *Subscription
	quota *int64
}

// auditedState reads the live subscription and stored computed quota before a
// change; nil when auditing is off.
func (s *Service) auditedState(ctx context.Context, userID snow.ID) (*billingState, error) {
	if s.audit == nil {
		return nil, nil
	}
	live, err := s.repo.LiveSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}
	quota, err := s.ledger.ComputedQuota(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &billingState{live: live, quota: quota}, nil
}

// appliedAudit attributes the change to the authenticated caller when there
// is one (plan change, admin sync or replay), otherwise to the provider.
func (s *Service) appliedAudit(ctx context.Context, sub Subscription, before *billingState, entitlement Entitlement, event *Event) domain.AuditEntry {
	old := map[string]any{"quota_bytes": nil}
	if before.quota != nil {
		old["quota_bytes"] = *before.quota
	}
	if before.live != nil {
		maps.Copy(old, subscriptionFields(*before.live))
	}
	updated := subscriptionFields(sub)
	updated["quota_bytes"] = entitlement.QuotaBytes
	metadata := map[string]any{"owner_id": sub.UserID.Base36(), "old": old, "new": updated}
	if event != nil {
		metadata["event_id"] = event.ID
		metadata["event_type"] = event.Type
	}
	entry := domain.AuditEntry{
		Action:     domain.AuditBillingSubscriptionApplied,
		TargetType: domain.AuditTargetSubscription,
		TargetID:   sub.ProviderSubscriptionID,
		Metadata:   metadata,
	}
	if _, ok := domain.ClaimFromContext(ctx); !ok {
		entry.ActorType = domain.AuditActorProvider
		if s.provider != nil {
			entry.ActorLabel = s.provider.Name()
		}
	}
	return entry
}

func subscriptionFields(sub Subscription) map[string]any {
	return map[string]any{
		"subscription_id": sub.ProviderSubscriptionID,
		"status":          sub.Status,
		"seats":           sub.Seats,
		"storage_packs":   sub.StoragePacks,
		"interval":        sub.Interval,
	}
}

// record stores an entry; a nil recorder records nothing.
func (s *Service) record(ctx context.Context, entry domain.AuditEntry) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, entry)
}

// Entitlement resolves a user's entitlement from their live subscription.
func (s *Service) Entitlement(ctx context.Context, userID snow.ID) (Entitlement, error) {
	sub, err := s.repo.LiveSubscription(ctx, userID)
	if err != nil {
		return Entitlement{}, err
	}
	return s.cfg.Entitlement(sub), nil
}
