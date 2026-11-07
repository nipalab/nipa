package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/snow"
)

func TestApply_StoresAndRecomputesQuota(t *testing.T) {
	svc, repo, ledger, tx := newStubService()
	owner := snow.ID(42)
	sub := Subscription{ProviderSubscriptionID: "sub_1", UserID: owner, Status: StatusActive, Seats: 5, StoragePacks: 2, ProviderUpdatedAt: at(1)}
	repo.live = &sub

	require.NoError(t, svc.Apply(context.Background(), sub))
	require.Equal(t, 1, tx.began)
	require.Equal(t, []string{"lock", "upsert", "live"}, repo.calls, "the owner lock is taken before anything is written")
	require.Equal(t, owner, repo.locked[0])
	require.Equal(t, []Subscription{sub}, repo.upserted)
	require.Equal(t, int64(5*100+2*5000), ledger.quotas[owner])
}

func TestApply_NoLiveSubscriptionWritesTheFreeQuota(t *testing.T) {
	svc, _, ledger, _ := newStubService()
	owner := snow.ID(42)

	require.NoError(t, svc.Apply(context.Background(), Subscription{
		ProviderSubscriptionID: "sub_1", UserID: owner, Status: StatusCanceled, ProviderUpdatedAt: at(1),
	}))
	require.Equal(t, testConfig.FreeQuotaBytes, ledger.quotas[owner])

	entitlement, err := svc.Entitlement(context.Background(), owner)
	require.NoError(t, err)
	require.Equal(t, testConfig.Free(), entitlement)
}

func TestApply_Validation(t *testing.T) {
	svc, repo, _, tx := newStubService()
	for _, sub := range []Subscription{
		{UserID: 1, ProviderUpdatedAt: at(1)},
		{ProviderSubscriptionID: " ", UserID: 1, ProviderUpdatedAt: at(1)},
		{ProviderSubscriptionID: "sub", ProviderUpdatedAt: at(1)},
		{ProviderSubscriptionID: "sub", UserID: 1},
	} {
		require.Equal(t, 400, errorCode(svc.Apply(context.Background(), sub)))
	}
	require.Zero(t, tx.began)
	require.Empty(t, repo.calls)
}

func TestApply_Errors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]func(*stubRepo, *stubLedger, *stubTx){
		"transaction": func(_ *stubRepo, _ *stubLedger, tx *stubTx) { tx.err = boom },
		"lock":        func(r *stubRepo, _ *stubLedger, _ *stubTx) { r.lockErr = boom },
		"upsert":      func(r *stubRepo, _ *stubLedger, _ *stubTx) { r.upsertErr = boom },
		"live":        func(r *stubRepo, _ *stubLedger, _ *stubTx) { r.liveErr = boom },
		"quota":       func(_ *stubRepo, l *stubLedger, _ *stubTx) { l.quotaErr = boom },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, ledger, tx := newStubService()
			mutate(repo, ledger, tx)
			err := svc.Apply(context.Background(), Subscription{ProviderSubscriptionID: "sub", UserID: 1, Status: StatusActive, ProviderUpdatedAt: at(1)})
			require.ErrorIs(t, err, boom)
		})
	}
}
