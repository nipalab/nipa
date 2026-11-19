package billing

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func ownStorageService() (*Service, *stubRepo, *stubProvider) {
	svc, repo, _, provider := newProviderService()
	svc.cfg.OwnStorage = true
	return svc, repo, provider
}

func liveCalls(repo *stubRepo) int {
	n := 0
	for _, call := range repo.calls {
		if call == "live" {
			n++
		}
	}
	return n
}

func TestOwnStorageActive_LiveSubscription(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		live *Subscription
		want bool
	}{
		"none":       {nil, false},
		"active":     {&Subscription{Status: StatusActive, OwnStorage: true}, true},
		"trialing":   {&Subscription{Status: StatusTrialing, OwnStorage: true}, true},
		"past due":   {&Subscription{Status: StatusPastDue, OwnStorage: true}, true},
		"canceled":   {&Subscription{Status: StatusCanceled, OwnStorage: true}, false},
		"no add-on":  {&Subscription{Status: StatusActive}, false},
		"paused add": {&Subscription{Status: StatusPaused, OwnStorage: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			svc, repo, _ := ownStorageService()
			repo.live = tc.live
			active, err := svc.OwnStorageActive(ctx, payer)
			require.NoError(t, err)
			require.Equal(t, tc.want, active)
		})
	}
}

func TestOwnStorageActive_CachesPerOwner(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := ownStorageService()
	now := at(0)
	svc.own = newOwnStorageCache(func() time.Time { return now })
	repo.live = &Subscription{Status: StatusActive, OwnStorage: true}

	active, err := svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.True(t, active)
	repo.live = nil
	active, err = svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.True(t, active, "served from the cache")
	require.Equal(t, 1, liveCalls(repo))

	active, err = svc.OwnStorageActive(ctx, snow.ID(99))
	require.NoError(t, err)
	require.False(t, active, "another owner has its own entry")

	now = now.Add(ownStorageTTL)
	active, err = svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.False(t, active, "expired after the TTL")
	require.Equal(t, 3, liveCalls(repo))
}

func TestOwnStorageActive_ErrorsAreNotCached(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := ownStorageService()
	repo.liveErr = errors.New("db down")
	_, err := svc.OwnStorageActive(ctx, payer)
	require.EqualError(t, err, "db down")

	repo.liveErr = nil
	repo.live = &Subscription{Status: StatusActive, OwnStorage: true}
	active, err := svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.True(t, active)
}

func TestOwnStorageActive_ApplyForgetsOwner(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := ownStorageService()
	repo.live = &Subscription{Status: StatusActive, OwnStorage: true}
	active, err := svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.True(t, active)

	repo.live = &Subscription{Status: StatusCanceled, OwnStorage: true}
	require.NoError(t, svc.Apply(ctx, Subscription{ProviderSubscriptionID: "sub_1", UserID: payer, Status: StatusCanceled, OwnStorage: true, ProviderUpdatedAt: at(3)}))
	require.True(t, repo.upserted[0].OwnStorage, "own_storage is stored")
	active, err = svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.False(t, active, "the applied subscription is read again")
}

func TestOwnStorageCache_StaleReadAfterForgetIsDropped(t *testing.T) {
	cache := newOwnStorageCache(time.Now)
	_, ok, gen := cache.get(payer)
	require.False(t, ok)
	cache.forget(payer)
	cache.put(payer, true, gen)
	_, ok, _ = cache.get(payer)
	require.False(t, ok, "a read that started before a forget is not stored")
}

func TestOwnStorageCache_PrunesExpiredWhenFull(t *testing.T) {
	now := at(0)
	cache := newOwnStorageCache(func() time.Time { return now })
	for i := range ownStorageMaxCache {
		cache.put(snow.ID(i+1), true, 0)
	}
	now = now.Add(ownStorageTTL)
	cache.put(payer, true, 0)
	require.Len(t, cache.entries, 1)
}

func TestCheckout_OwnStorage(t *testing.T) {
	ctx := claimCtx(payer)
	svc, repo, provider := ownStorageService()
	rec := &captureRecorder{}
	svc.WithAudit(rec)
	repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Email: "pat@example.com"}}
	provider.customerID = "ctm_1"
	provider.checkoutID = "txn_1"

	_, err := svc.Checkout(ctx, payer, Items{OwnStorage: true, Interval: "year"}, "")
	require.NoError(t, err)
	require.Equal(t, []Items{{OwnStorage: true, Interval: IntervalYear}}, provider.checkouts)
	require.Equal(t, true, rec.entries[0].Metadata["own_storage"])

	svc.cfg.OwnStorage = false
	_, err = svc.Checkout(ctx, payer, Items{Seats: 1, OwnStorage: true, Interval: "year"}, "")
	require.Equal(t, 400, errorCode(err), "not offered without prices")
}

func TestChangeSubscription_OwnStorage(t *testing.T) {
	ctx := context.Background()
	svc, repo, provider := ownStorageService()
	repo.live = liveSub()
	repo.live.OwnStorage = true
	provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, Interval: IntervalYear, Seats: 4, ProviderUpdatedAt: at(2)}

	_, err := svc.ChangeSubscription(ctx, payer, Items{Seats: 4, keepOwnStorage: true})
	require.NoError(t, err)
	require.Equal(t, Items{Seats: 4, OwnStorage: true, Interval: IntervalYear}, provider.updates[0], "kept when absent")

	_, err = svc.ChangeSubscription(ctx, payer, Items{Seats: 4})
	require.NoError(t, err, "dropping the add-on is allowed while projects use own storage")
	require.Equal(t, Items{Seats: 4, Interval: IntervalYear}, provider.updates[1])

	repo.live.OwnStorage = false
	_, err = svc.ChangeSubscription(ctx, payer, Items{OwnStorage: true})
	require.NoError(t, err, "the add-on alone is a valid plan")
	require.Equal(t, Items{OwnStorage: true, Interval: IntervalYear}, provider.updates[2])
}

func TestChangeSubscription_ForgetsCachedOwnStorage(t *testing.T) {
	ctx := context.Background()
	svc, repo, provider := ownStorageService()
	repo.live = liveSub()
	repo.live.OwnStorage = true
	active, err := svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.True(t, active)

	provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, Interval: IntervalYear, Seats: 4, ProviderUpdatedAt: at(2)}
	_, err = svc.ChangeSubscription(ctx, payer, Items{Seats: 4})
	require.NoError(t, err)
	repo.live.OwnStorage = false
	active, err = svc.OwnStorageActive(ctx, payer)
	require.NoError(t, err)
	require.False(t, active)
}

func TestHTTP_OwnStorageItems(t *testing.T) {
	h := newHTTPHarness(t)
	h.svc.cfg.OwnStorage = true
	h.repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Email: "pat@example.com"}}
	h.provider.customerID = "ctm_1"
	h.provider.checkoutID = "txn_1"

	status, body := h.do(http.MethodPost, "/api/v1/billing/checkout", "payer", `{"own_storage":true,"interval":"month"}`, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []Items{{OwnStorage: true, Interval: IntervalMonth}}, h.provider.checkouts)

	h.repo.live = liveSub()
	h.repo.live.OwnStorage = true
	h.provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, Seats: 6, ProviderUpdatedAt: at(5)}
	status, body = h.do(http.MethodPatch, "/api/v1/billing/subscription", "payer", `{"seats":6}`, nil)
	require.Equal(t, http.StatusOK, status, body)
	status, body = h.do(http.MethodPatch, "/api/v1/billing/subscription", "payer", `{"seats":6,"own_storage":false}`, nil)
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, []Items{
		{Seats: 6, OwnStorage: true, Interval: IntervalYear},
		{Seats: 6, Interval: IntervalYear},
	}, h.provider.updates)
}
