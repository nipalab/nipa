package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdmit_ExistingSeatSkipsTheCount(t *testing.T) {
	svc, repo, _, tx := newStubService()
	repo.seat = true
	repo.seats = 99
	inserted := false

	require.NoError(t, svc.Admit(context.Background(), 1, 2, func(ctx context.Context) error {
		inserted = true
		require.True(t, inTx(ctx), "the insert joins the gate transaction")
		return nil
	}))
	require.True(t, inserted)
	require.Equal(t, 1, tx.began)
	require.Equal(t, []string{"lock", "is_seat"}, repo.calls)
	require.Equal(t, int64(7), repo.locked[0].Int64(), "the lock is the org's billing owner")
}

func TestAdmit_FreeSeat(t *testing.T) {
	svc, repo, _, _ := newStubService()
	repo.seats = 2
	inserted := false

	require.NoError(t, svc.Admit(context.Background(), 1, 2, func(context.Context) error {
		inserted = true
		return nil
	}))
	require.True(t, inserted)
	require.Equal(t, []string{"lock", "is_seat", "count", "live"}, repo.calls)
}

func TestAdmit_RefusesAtTheFreeLimit(t *testing.T) {
	svc, repo, _, _ := newStubService()
	repo.seats = 3

	err := svc.Admit(context.Background(), 1, 2, func(context.Context) error {
		t.Fatal("insert must not run over the limit")
		return nil
	})
	require.Equal(t, 402, errorCode(err), "got %v", err)
	require.ErrorContains(t, err, "seat limit reached: 3 of 3")
}

func TestAdmit_PaidLimit(t *testing.T) {
	svc, repo, _, _ := newStubService()
	repo.live = &Subscription{Status: StatusActive, Seats: 5}

	repo.seats = 4
	require.NoError(t, svc.Admit(context.Background(), 1, 2, func(context.Context) error { return nil }))

	repo.seats = 5
	err := svc.Admit(context.Background(), 1, 2, func(context.Context) error { return nil })
	require.ErrorContains(t, err, "seat limit reached: 5 of 5")
}

func TestAdmit_Errors(t *testing.T) {
	boom := errors.New("boom")
	insertErr := errors.New("insert failed")
	cases := map[string]func(*stubRepo, *stubLedger, *stubTx){
		"transaction": func(_ *stubRepo, _ *stubLedger, tx *stubTx) { tx.err = boom },
		"owner":       func(_ *stubRepo, l *stubLedger, _ *stubTx) { l.ownerErr = boom },
		"lock":        func(r *stubRepo, _ *stubLedger, _ *stubTx) { r.lockErr = boom },
		"is seat":     func(r *stubRepo, _ *stubLedger, _ *stubTx) { r.seatErr = boom },
		"count":       func(r *stubRepo, _ *stubLedger, _ *stubTx) { r.countErr = boom },
		"live":        func(r *stubRepo, _ *stubLedger, _ *stubTx) { r.liveErr = boom },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			svc, repo, ledger, tx := newStubService()
			mutate(repo, ledger, tx)
			err := svc.Admit(context.Background(), 1, 2, func(context.Context) error {
				t.Fatal("insert must not run after a failed check")
				return nil
			})
			require.ErrorIs(t, err, boom)
		})
	}

	svc, _, _, _ := newStubService()
	err := svc.Admit(context.Background(), 1, 2, func(context.Context) error { return insertErr })
	require.ErrorIs(t, err, insertErr, "insert errors are returned unchanged")
}
