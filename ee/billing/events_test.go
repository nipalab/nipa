package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func activatedEvent(id string) Event {
	return Event{ID: id, Type: "subscription.activated", OccurredAt: at(0), Payload: []byte(`{"id":"` + id + `","type":"subscription.activated"}`)}
}

func eventService(userID snow.ID, customerID string) (*Service, *stubRepo, *stubLedger, *stubProvider) {
	svc, repo, ledger, provider := newProviderService()
	repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer}}
	provider.carried = &EventSubscription{
		Subscription: Subscription{ProviderSubscriptionID: "sub_1", UserID: userID, Status: StatusActive, Seats: 5, ProviderUpdatedAt: at(1)},
		CustomerID:   customerID,
	}
	return svc, repo, ledger, provider
}

func TestHandleEvent_AppliesOnce(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := eventService(payer, "ctm_1")

	require.NoError(t, svc.HandleEvent(ctx, activatedEvent("evt_1")))
	require.Len(t, repo.upserted, 1)
	require.Equal(t, payer, repo.upserted[0].UserID)
	require.NotNil(t, repo.events["evt_1"].ProcessedAt)

	require.NoError(t, svc.HandleEvent(ctx, activatedEvent("evt_1")))
	require.Len(t, repo.upserted, 1, "a duplicate delivery is a no-op")
}

func TestHandleEvent_RetriesAnUnfinishedDuplicate(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := eventService(payer, "")
	repo.upsertErr = errors.New("connection reset")

	require.Error(t, svc.HandleEvent(ctx, activatedEvent("evt_1")), "a transient error asks for a retry")
	require.Nil(t, repo.events["evt_1"].ProcessedAt)
	require.Empty(t, repo.events["evt_1"].Error)

	repo.upsertErr = nil
	require.NoError(t, svc.HandleEvent(ctx, activatedEvent("evt_1")))
	require.Len(t, repo.upserted, 1, "the retried delivery is applied")
}

func TestHandleEvent_FallsBackToCustomer(t *testing.T) {
	svc, repo, _, _ := eventService(0, "ctm_1")
	repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, Provider: "stub", ProviderCustomerID: "ctm_1"}}

	require.NoError(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")))
	require.Equal(t, payer, repo.upserted[0].UserID)
}

func TestHandleEvent_PermanentFailuresAreStored(t *testing.T) {
	cases := map[string]func(*stubRepo, *stubProvider){
		"no user":       func(_ *stubRepo, p *stubProvider) { p.carried.Subscription.UserID = 0 },
		"unknown cust":  func(_ *stubRepo, p *stubProvider) { p.carried.Subscription.UserID = 0; p.carried.CustomerID = "ctm_x" },
		"unknown user":  func(_ *stubRepo, p *stubProvider) { p.carried.Subscription.UserID = 99 },
		"deleted user":  func(r *stubRepo, _ *stubProvider) { r.users[payer].Deleted = true },
		"unmappable":    func(_ *stubRepo, p *stubProvider) { p.carriedErr = domain.NewErrorUser("unknown price") },
		"invalid apply": func(_ *stubRepo, p *stubProvider) { p.carried.Subscription.ProviderSubscriptionID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, _, provider := eventService(payer, "")
			mutate(repo, provider)
			require.NoError(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")))
			require.Empty(t, repo.upserted)
			require.NotEmpty(t, repo.events["evt_1"].Error)
			require.Nil(t, repo.events["evt_1"].ProcessedAt)

			require.NoError(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")), "a failed event is not retried")
		})
	}
}

func TestHandleEvent_UnhandledTypesAreStoredAsProcessed(t *testing.T) {
	svc, repo, _, _ := eventService(payer, "")
	event := Event{ID: "evt_t", Type: "transaction.completed", OccurredAt: at(0), Payload: []byte(`{}`)}
	require.NoError(t, svc.HandleEvent(context.Background(), event))
	require.Empty(t, repo.upserted)
	require.NotNil(t, repo.events["evt_t"].ProcessedAt)
}

func TestHandleEvent_TransientErrors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(*stubRepo){
		"insert":  func(r *stubRepo) { r.eventErr = boom },
		"user":    func(r *stubRepo) { r.userErr = boom },
		"mark":    func(r *stubRepo) { r.markErr = boom },
		"account": func(r *stubRepo) { r.accountErr = boom },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			customer := ""
			userID := payer
			if name == "account" {
				customer, userID = "ctm_1", 0
			}
			svc, repo, _, _ := eventService(userID, customer)
			mutate(repo)
			require.ErrorIs(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")), boom)
		})
	}
}

func TestHandleEvent_UnfinishedDuplicateMarkError(t *testing.T) {
	svc, repo, _, _ := eventService(payer, "")
	repo.events = map[string]*domain.BillingEvent{"evt_1": {EventID: "evt_1"}}
	repo.markErr = errors.New("boom")
	require.Error(t, svc.HandleEvent(context.Background(), activatedEvent("evt_1")))
	require.Len(t, repo.upserted, 1, "an unfinished duplicate is processed again")
}

func TestReplay(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, provider := eventService(payer, "")
	provider.carried.Subscription.UserID = 99

	require.NoError(t, svc.HandleEvent(ctx, activatedEvent("evt_1")))
	require.NotEmpty(t, repo.events["evt_1"].Error)

	provider.carried.Subscription.UserID = payer
	stored, err := svc.Replay(ctx, "evt_1")
	require.NoError(t, err)
	require.NotNil(t, stored.ProcessedAt)
	require.Empty(t, stored.Error)
	require.Len(t, repo.upserted, 1)

	_, err = svc.Replay(ctx, "evt_missing")
	require.Equal(t, 404, errorCode(err))

	repo.events["evt_bad"] = &domain.BillingEvent{EventID: "evt_bad", Payload: []byte(`garbage`)}
	stored, err = svc.Replay(ctx, "evt_bad")
	require.NoError(t, err)
	require.NotEmpty(t, stored.Error)

	repo.upsertErr = errors.New("boom")
	_, err = svc.Replay(ctx, "evt_1")
	require.Error(t, err)

	repo.markErr = errors.New("boom")
	_, err = svc.Replay(ctx, "evt_bad")
	require.Error(t, err)
}
