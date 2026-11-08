package billing

import (
	"context"
	"errors"
	"log/slog"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

var subscriptionEvents = map[string]bool{
	"subscription.created":   true,
	"subscription.updated":   true,
	"subscription.trialing":  true,
	"subscription.activated": true,
	"subscription.past_due":  true,
	"subscription.paused":    true,
	"subscription.resumed":   true,
	"subscription.canceled":  true,
}

// HandleEvent stores a verified webhook event and applies it once. Events
// that can never apply (unknown user, unmappable data) are marked failed and
// return nil so the provider stops retrying; transient errors are returned.
func (s *Service) HandleEvent(ctx context.Context, event Event) error {
	inserted, err := s.repo.InsertEvent(ctx, domain.BillingEvent{
		EventID: event.ID, Type: event.Type, OccurredAt: event.OccurredAt, Payload: event.Payload,
	})
	if err != nil {
		return err
	}
	if !inserted {
		stored, err := s.repo.Event(ctx, event.ID)
		if err != nil {
			return err
		}
		if stored.ProcessedAt != nil || stored.Error != "" {
			return nil
		}
	}
	return s.process(ctx, event)
}

// Replay re-runs a stored event, e.g. after the user it names was fixed.
func (s *Service) Replay(ctx context.Context, eventID string) (*domain.BillingEvent, error) {
	stored, err := s.repo.Event(ctx, eventID)
	if err != nil {
		return nil, err
	}
	event, err := s.provider.DecodeEvent(stored.Payload)
	if err != nil {
		if markErr := s.repo.MarkEventFailed(ctx, eventID, err.Error()); markErr != nil {
			return nil, markErr
		}
	} else {
		event.ID = stored.EventID
		if err := s.process(ctx, event); err != nil {
			return nil, err
		}
	}
	replayed, err := s.repo.Event(ctx, eventID)
	if err != nil {
		return nil, err
	}
	outcome := "processed"
	if replayed.ProcessedAt == nil {
		outcome = "failed"
	}
	if err := s.record(ctx, domain.AuditEntry{
		Action:     domain.AuditBillingEventReplayed,
		TargetType: domain.AuditTargetBillingEvent,
		TargetID:   replayed.EventID,
		Metadata: map[string]any{
			"event_id":   replayed.EventID,
			"event_type": replayed.Type,
			"outcome":    outcome,
			"error":      replayed.Error,
		},
	}); err != nil {
		return nil, err
	}
	return replayed, nil
}

func (s *Service) process(ctx context.Context, event Event) error {
	if !subscriptionEvents[event.Type] {
		return s.repo.MarkEventProcessed(ctx, event.ID)
	}
	carried, err := s.provider.EventSubscription(event)
	if err != nil {
		return s.settle(ctx, event, err)
	}
	sub := carried.Subscription
	sub.UserID, err = s.eventUser(ctx, sub.UserID, carried.CustomerID)
	if err != nil {
		return s.settle(ctx, event, err)
	}
	return s.settle(ctx, event, s.apply(ctx, sub, &event))
}

// settle records the outcome: success and permanent failures are final,
// anything else is returned so the delivery is retried.
func (s *Service) settle(ctx context.Context, event Event, err error) error {
	if err == nil {
		return s.repo.MarkEventProcessed(ctx, event.ID)
	}
	if !permanent(err) {
		return err
	}
	slog.Warn("billing event failed", "event_id", event.ID, "type", event.Type, "error", err)
	return s.repo.MarkEventFailed(ctx, event.ID, err.Error())
}

func permanent(err error) bool {
	var domErr *domain.Error
	return errors.As(err, &domErr) && domErr.Code >= 400 && domErr.Code < 500
}

// eventUser resolves the event's user from the billing account that holds the
// provider customer id. custom_data.user_id is only a cross-check, so a payer
// cannot attach a subscription to another user. An unknown customer, a
// mismatch or a deleted user is a 4xx outcome.
func (s *Service) eventUser(ctx context.Context, userID snow.ID, customerID string) (snow.ID, error) {
	if customerID == "" {
		return 0, domain.NewErrorNotFound("event names no known customer")
	}
	account, err := s.repo.AccountByCustomer(ctx, s.provider.Name(), customerID)
	if err != nil {
		return 0, err
	}
	if account == nil {
		return 0, domain.NewErrorNotFound("event customer has no billing account")
	}
	if userID != 0 && userID != account.UserID {
		return 0, domain.NewErrorUser("event user does not match the customer's billing account")
	}
	user, err := s.repo.User(ctx, account.UserID)
	if err != nil {
		return 0, err
	}
	if user.Deleted {
		return 0, domain.NewErrorNotFound("event user is deleted")
	}
	return account.UserID, nil
}
