package billing

import (
	"context"
	"fmt"

	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

// Checkout starts a first subscription and returns the provider transaction
// the browser completes.
func (s *Service) Checkout(ctx context.Context, userID snow.ID, items Items) (string, error) {
	items, err := items.Validate()
	if err != nil {
		return "", err
	}
	live, err := s.repo.LiveSubscription(ctx, userID)
	if err != nil {
		return "", err
	}
	if live != nil {
		return "", domain.NewErrorConflict("a subscription is already active; change it instead of checking out again")
	}
	if err := s.checkSeats(ctx, userID, items.Seats); err != nil {
		return "", err
	}
	account, err := s.ensureAccount(ctx, userID)
	if err != nil {
		return "", err
	}
	txn, err := s.provider.CreateCheckout(ctx, account.ProviderCustomerID, userID, items)
	if err != nil {
		return "", err
	}
	if err := s.record(ctx, domain.AuditEntry{
		Action:     domain.AuditBillingCheckoutStarted,
		TargetType: domain.AuditTargetUser,
		TargetID:   userID.Base36(),
		Metadata: map[string]any{
			"seats":          items.Seats,
			"storage_packs":  items.StoragePacks,
			"interval":       items.Interval,
			"transaction_id": txn,
			"customer_id":    account.ProviderCustomerID,
		},
	}); err != nil {
		return "", err
	}
	return txn, nil
}

// ChangeSubscription replaces the live subscription's seats, storage packs and
// interval, then applies the provider's answer right away.
func (s *Service) ChangeSubscription(ctx context.Context, userID snow.ID, items Items) (Subscription, error) {
	live, err := s.repo.LiveSubscription(ctx, userID)
	if err != nil {
		return Subscription{}, err
	}
	if live == nil {
		return Subscription{}, domain.NewErrorNotFound("no active subscription")
	}
	if items.Interval == "" {
		items.Interval = live.Interval
	}
	items, err = items.Validate()
	if err != nil {
		return Subscription{}, err
	}
	if err := s.checkSeats(ctx, userID, items.Seats); err != nil {
		return Subscription{}, err
	}
	quota := s.cfg.Entitlement(&Subscription{Status: StatusActive, Seats: items.Seats, StoragePacks: items.StoragePacks}).QuotaBytes
	if quota > 0 {
		usage, err := s.ledger.OwnerStorage(ctx, userID)
		if err != nil {
			return Subscription{}, err
		}
		if usage.UsedBytes > quota {
			return Subscription{}, domain.NewErrorConflict(fmt.Sprintf(
				"the new plan allows %d bytes but %d bytes are in use; delete data or keep more storage", quota, usage.UsedBytes))
		}
	}
	sub, err := s.provider.UpdateSubscription(ctx, live.ProviderSubscriptionID, items)
	if err != nil {
		return Subscription{}, err
	}
	sub.UserID = userID
	if err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.Apply(ctx, sub); err != nil {
			return err
		}
		return s.record(ctx, domain.AuditEntry{
			Action:     domain.AuditBillingSubscriptionChanged,
			TargetType: domain.AuditTargetSubscription,
			TargetID:   live.ProviderSubscriptionID,
			Metadata: map[string]any{
				"owner_id": userID.Base36(),
				"old":      map[string]any{"seats": live.Seats, "storage_packs": live.StoragePacks, "interval": live.Interval},
				"new":      map[string]any{"seats": items.Seats, "storage_packs": items.StoragePacks, "interval": items.Interval},
			},
		})
	}); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

// Portal returns a provider customer-portal link for the user.
func (s *Service) Portal(ctx context.Context, userID snow.ID) (string, error) {
	account, err := s.repo.Account(ctx, userID)
	if err != nil {
		return "", err
	}
	if account == nil {
		return "", domain.NewErrorNotFound("no billing account")
	}
	live, err := s.repo.LiveSubscription(ctx, userID)
	if err != nil {
		return "", err
	}
	subID := ""
	if live != nil {
		subID = live.ProviderSubscriptionID
	}
	return s.provider.PortalURL(ctx, account.ProviderCustomerID, subID)
}

// Summary is the billing part of GET /billing/me.
func (s *Service) Summary(ctx context.Context, userID snow.ID) (*storageusage.BillingSummary, error) {
	live, err := s.repo.LiveSubscription(ctx, userID)
	if err != nil {
		return nil, err
	}
	used, err := s.repo.CountSeats(ctx, userID)
	if err != nil {
		return nil, err
	}
	entitlement := s.cfg.Entitlement(live)
	summary := &storageusage.BillingSummary{Plan: PlanFree, SeatsUsed: used, SeatLimit: entitlement.SeatLimit}
	if live != nil {
		summary.Plan = live.Plan
		summary.Status = live.Status
		summary.Interval = live.Interval
		summary.Seats = live.Seats
		summary.StoragePacks = live.StoragePacks
		summary.CurrentPeriodEnd = live.CurrentPeriodEnd
		summary.CancelAtPeriodEnd = live.CancelAtPeriodEnd
	}
	return summary, nil
}

// Sync re-applies every provider subscription of the user's customer.
func (s *Service) Sync(ctx context.Context, userID snow.ID) (int, error) {
	account, err := s.repo.Account(ctx, userID)
	if err != nil {
		return 0, err
	}
	if account == nil {
		return 0, domain.NewErrorNotFound("no billing account")
	}
	subs, err := s.provider.ListSubscriptions(ctx, account.ProviderCustomerID)
	if err != nil {
		return 0, err
	}
	for _, sub := range subs {
		sub.UserID = userID
		if err := s.Apply(ctx, sub); err != nil {
			return 0, err
		}
	}
	if err := s.record(ctx, domain.AuditEntry{
		Action:     domain.AuditBillingSync,
		TargetType: domain.AuditTargetUser,
		TargetID:   userID.Base36(),
		Metadata:   map[string]any{"synced": len(subs)},
	}); err != nil {
		return 0, err
	}
	return len(subs), nil
}

func (s *Service) checkSeats(ctx context.Context, userID snow.ID, seats int64) error {
	used, err := s.repo.CountSeats(ctx, userID)
	if err != nil {
		return err
	}
	if seats < used {
		return domain.NewErrorConflict(fmt.Sprintf("%d seats are in use; buy at least that many", used))
	}
	return nil
}

func (s *Service) ensureAccount(ctx context.Context, userID snow.ID) (*domain.BillingAccount, error) {
	account, err := s.repo.Account(ctx, userID)
	if err != nil || account != nil {
		return account, err
	}
	user, err := s.repo.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.Deleted {
		return nil, domain.NewErrorNotFound("user not found")
	}
	customerID, err := s.provider.EnsureCustomer(ctx, userID, user.Email, user.Name)
	if err != nil {
		return nil, err
	}
	if err := s.repo.InsertAccount(ctx, domain.BillingAccount{
		UserID: userID, Provider: s.provider.Name(), ProviderCustomerID: customerID,
	}); err != nil {
		return nil, err
	}
	account, err = s.repo.Account(ctx, userID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, domain.NewErrorConflict("the billing customer is linked to another account")
	}
	return account, nil
}
