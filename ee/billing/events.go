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
	return s.repo.Event(ctx, eventID)
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
	return s.settle(ctx, event, s.Apply(ctx, sub))
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

// eventUser resolves the event's user from custom_data, falling back to the
// account holding the provider customer id. Missing or deleted users are a 404.
func (s *Service) eventUser(ctx context.Context, userID snow.ID, customerID string) (snow.ID, error) {
	if userID == 0 && customerID != "" {
		account, err := s.repo.AccountByCustomer(ctx, s.provider.Name(), customerID)
		if err != nil {
			return 0, err
		}
		if account != nil {
			userID = account.UserID
		}
	}
	if userID == 0 {
		return 0, domain.NewErrorNotFound("event names no known user")
	}
	user, err := s.repo.User(ctx, userID)
	if err != nil {
		return 0, err
	}
	if user.Deleted {
		return 0, domain.NewErrorNotFound("event user is deleted")
	}
	return userID, nil
}
