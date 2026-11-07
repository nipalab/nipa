package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const payer = snow.ID(42)

func liveSub() *Subscription {
	end := at(30)
	return &Subscription{ProviderSubscriptionID: "sub_live", UserID: payer, Plan: PlanTeam, Status: StatusActive,
		Interval: IntervalYear, Seats: 4, StoragePacks: 1, CurrentPeriodEnd: &end, ProviderUpdatedAt: at(1)}
}

func TestItemsValidate(t *testing.T) {
	items, err := Items{Seats: 1, Interval: " Monthly "}.Validate()
	require.NoError(t, err)
	require.Equal(t, IntervalMonth, items.Interval)
	items, err = Items{Seats: 1, Interval: "yearly"}.Validate()
	require.NoError(t, err)
	require.Equal(t, IntervalYear, items.Interval)

	for _, bad := range []Items{{Seats: 1, Interval: "week"}, {Seats: 0, Interval: "month"}, {Seats: 1, StoragePacks: -1, Interval: "year"}} {
		_, err := bad.Validate()
		require.Equal(t, 400, errorCode(err))
	}
}

func TestCheckout_CreatesCustomerOnce(t *testing.T) {
	svc, repo, _, provider := newProviderService()
	repo.seats = 2
	repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Name: "Pat", Email: "pat@example.com"}}
	provider.customerID = "ctm_1"
	provider.checkoutID = "txn_1"

	txn, err := svc.Checkout(context.Background(), payer, Items{Seats: 2, StoragePacks: 1, Interval: "month"})
	require.NoError(t, err)
	require.Equal(t, "txn_1", txn)
	require.Equal(t, &domain.BillingAccount{UserID: payer, Provider: "stub", ProviderCustomerID: "ctm_1"}, repo.accounts[payer])
	require.Equal(t, []Items{{Seats: 2, StoragePacks: 1, Interval: IntervalMonth}}, provider.checkouts)

	_, err = svc.Checkout(context.Background(), payer, Items{Seats: 3, Interval: "year"})
	require.NoError(t, err)
	require.Equal(t, []string{"ensure_customer", "checkout", "checkout"}, provider.calls, "the stored account is reused")
}

func TestCheckout_Refusals(t *testing.T) {
	ctx := context.Background()

	svc, repo, _, provider := newProviderService()
	_, err := svc.Checkout(ctx, payer, Items{Seats: 1, Interval: "day"})
	require.Equal(t, 400, errorCode(err))

	repo.live = liveSub()
	_, err = svc.Checkout(ctx, payer, Items{Seats: 5, Interval: "month"})
	require.Equal(t, 409, errorCode(err), "a live subscription blocks a second checkout")

	repo.live = nil
	repo.seats = 3
	_, err = svc.Checkout(ctx, payer, Items{Seats: 2, Interval: "month"})
	require.Equal(t, 409, errorCode(err), "fewer seats than in use")
	require.Empty(t, provider.calls)

	_, err = svc.Checkout(ctx, payer, Items{Seats: 3, Interval: "month"})
	require.Equal(t, 404, errorCode(err), "unknown user")

	repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Deleted: true}}
	_, err = svc.Checkout(ctx, payer, Items{Seats: 3, Interval: "month"})
	require.Equal(t, 404, errorCode(err), "deleted user")
	require.Empty(t, provider.calls)
}

func TestCheckout_CustomerOwnedByAnotherUser(t *testing.T) {
	svc, repo, _, provider := newProviderService()
	repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Email: "pat@example.com"}}
	repo.accounts = map[snow.ID]*domain.BillingAccount{7: {UserID: 7, Provider: "stub", ProviderCustomerID: "ctm_shared"}}
	provider.customerID = "ctm_shared"

	_, err := svc.Checkout(context.Background(), payer, Items{Seats: 1, Interval: "month"})
	require.Equal(t, 409, errorCode(err))
	require.NotContains(t, provider.calls, "checkout")
}

func TestCheckout_Errors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(*stubRepo, *stubProvider){
		"live":     func(r *stubRepo, _ *stubProvider) { r.liveErr = boom },
		"count":    func(r *stubRepo, _ *stubProvider) { r.countErr = boom },
		"account":  func(r *stubRepo, _ *stubProvider) { r.accountErr = boom },
		"user":     func(r *stubRepo, _ *stubProvider) { r.userErr = boom },
		"customer": func(_ *stubRepo, p *stubProvider) { p.customerErr = boom },
		"insert":   func(r *stubRepo, _ *stubProvider) { r.insertAccErr = boom },
		"checkout": func(_ *stubRepo, p *stubProvider) { p.checkoutErr = boom },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, _, provider := newProviderService()
			repo.users = map[snow.ID]*domain.BillingUser{payer: {ID: payer, Email: "pat@example.com"}}
			provider.customerID = "ctm_1"
			mutate(repo, provider)
			_, err := svc.Checkout(context.Background(), payer, Items{Seats: 1, Interval: "month"})
			require.ErrorIs(t, err, boom)
		})
	}
}

func TestChangeSubscription_AppliesProviderAnswer(t *testing.T) {
	svc, repo, ledger, provider := newProviderService()
	repo.live = liveSub()
	repo.seats = 3
	ledger.used = 900
	provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, Plan: PlanTeam,
		Interval: IntervalYear, Seats: 3, ProviderUpdatedAt: at(2)}

	sub, err := svc.ChangeSubscription(context.Background(), payer, Items{Seats: 3})
	require.NoError(t, err)
	require.Equal(t, payer, sub.UserID, "the caller owns the result")
	require.Equal(t, []Items{{Seats: 3, Interval: IntervalYear}}, provider.updates, "the interval defaults to the live one")
	require.Len(t, repo.upserted, 1)
	require.Equal(t, payer, repo.upserted[0].UserID)
	require.Contains(t, ledger.quotas, payer)
}

func TestChangeSubscription_Refusals(t *testing.T) {
	ctx := context.Background()
	svc, repo, ledger, provider := newProviderService()
	provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, Seats: 4, ProviderUpdatedAt: at(2)}

	_, err := svc.ChangeSubscription(ctx, payer, Items{Seats: 3, Interval: "month"})
	require.Equal(t, 404, errorCode(err))

	repo.live = liveSub()
	_, err = svc.ChangeSubscription(ctx, payer, Items{Seats: 3, Interval: "fortnight"})
	require.Equal(t, 400, errorCode(err))

	repo.seats = 4
	_, err = svc.ChangeSubscription(ctx, payer, Items{Seats: 3})
	require.Equal(t, 409, errorCode(err), "fewer seats than in use")

	ledger.used = 4*testConfig.SeatIncludedBytes + 1
	_, err = svc.ChangeSubscription(ctx, payer, Items{Seats: 4})
	require.NoError(t, err, "the free quota floor still covers the usage")

	ledger.used = testConfig.FreeQuotaBytes + 1
	_, err = svc.ChangeSubscription(ctx, payer, Items{Seats: 4})
	require.Equal(t, 409, errorCode(err), "usage above the new quota")
	require.Len(t, provider.updates, 1)

	unlimited := New(repo, ledger, &stubTx{}, Config{FreeSeats: 1}).WithProvider(provider)
	_, err = unlimited.ChangeSubscription(ctx, payer, Items{Seats: 4})
	require.NoError(t, err, "a zero free quota is unlimited, so usage never blocks")
}

func TestChangeSubscription_Errors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(*stubRepo, *stubLedger, *stubProvider){
		"live":   func(r *stubRepo, _ *stubLedger, _ *stubProvider) { r.liveErr = boom },
		"count":  func(r *stubRepo, _ *stubLedger, _ *stubProvider) { r.countErr = boom },
		"usage":  func(_ *stubRepo, l *stubLedger, _ *stubProvider) { l.usageErr = boom },
		"update": func(_ *stubRepo, _ *stubLedger, p *stubProvider) { p.updateErr = boom },
		"apply":  func(r *stubRepo, _ *stubLedger, _ *stubProvider) { r.upsertErr = boom },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, ledger, provider := newProviderService()
			repo.live = liveSub()
			provider.updated = Subscription{ProviderSubscriptionID: "sub_live", Status: StatusActive, ProviderUpdatedAt: at(2)}
			mutate(repo, ledger, provider)
			if name == "live" {
				repo.live = nil
			}
			_, err := svc.ChangeSubscription(context.Background(), payer, Items{Seats: 4, Interval: "month"})
			require.ErrorIs(t, err, boom)
		})
	}
}

func TestPortal(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, provider := newProviderService()
	provider.portal = "https://portal"

	_, err := svc.Portal(ctx, payer)
	require.Equal(t, 404, errorCode(err))

	repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, ProviderCustomerID: "ctm_1"}}
	link, err := svc.Portal(ctx, payer)
	require.NoError(t, err)
	require.Equal(t, "https://portal", link)

	repo.live = liveSub()
	_, err = svc.Portal(ctx, payer)
	require.NoError(t, err)
	require.Equal(t, []string{"", "sub_live"}, provider.portalSubs)

	repo.liveErr = errors.New("boom")
	_, err = svc.Portal(ctx, payer)
	require.Error(t, err)
	repo.accountErr = errors.New("boom")
	_, err = svc.Portal(ctx, payer)
	require.Error(t, err)
}

func TestSummary(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _ := newProviderService()
	repo.seats = 2

	summary, err := svc.Summary(ctx, payer)
	require.NoError(t, err)
	require.Equal(t, PlanFree, summary.Plan)
	require.Empty(t, summary.Status)
	require.Equal(t, int64(2), summary.SeatsUsed)
	require.Equal(t, testConfig.FreeSeats, summary.SeatLimit)
	require.Zero(t, summary.Seats)

	repo.live = liveSub()
	repo.live.CancelAtPeriodEnd = true
	summary, err = svc.Summary(ctx, payer)
	require.NoError(t, err)
	require.Equal(t, PlanTeam, summary.Plan)
	require.Equal(t, StatusActive, summary.Status)
	require.Equal(t, IntervalYear, summary.Interval)
	require.Equal(t, int64(4), summary.SeatLimit)
	require.Equal(t, repo.live.Seats, summary.Seats)
	require.Equal(t, int64(1), summary.StoragePacks)
	require.Equal(t, repo.live.CurrentPeriodEnd, summary.CurrentPeriodEnd)
	require.True(t, summary.CancelAtPeriodEnd)

	repo.countErr = errors.New("boom")
	_, err = svc.Summary(ctx, payer)
	require.Error(t, err)
	repo.liveErr = errors.New("boom")
	_, err = svc.Summary(ctx, payer)
	require.Error(t, err)
}

func TestSync(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, provider := newProviderService()

	_, err := svc.Sync(ctx, payer)
	require.Equal(t, 404, errorCode(err))

	repo.accounts = map[snow.ID]*domain.BillingAccount{payer: {UserID: payer, ProviderCustomerID: "ctm_1"}}
	provider.listed = []Subscription{
		{ProviderSubscriptionID: "sub_old", Status: StatusCanceled, ProviderUpdatedAt: at(1)},
		{ProviderSubscriptionID: "sub_new", Status: StatusActive, Seats: 5, ProviderUpdatedAt: at(2)},
	}
	count, err := svc.Sync(ctx, payer)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Len(t, repo.upserted, 2)
	for _, sub := range repo.upserted {
		require.Equal(t, payer, sub.UserID)
	}

	repo.upsertErr = errors.New("boom")
	_, err = svc.Sync(ctx, payer)
	require.Error(t, err)
	provider.listErr = errors.New("boom")
	_, err = svc.Sync(ctx, payer)
	require.Error(t, err)
	repo.accountErr = errors.New("boom")
	_, err = svc.Sync(ctx, payer)
	require.Error(t, err)
}
