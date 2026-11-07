package billing

import (
	"context"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type stubRepo struct {
	calls     []string
	locked    []snow.ID
	seat      bool
	seats     int64
	live      *domain.BillingSubscription
	upserted  []domain.BillingSubscription
	lockErr   error
	seatErr   error
	countErr  error
	liveErr   error
	upsertErr error
}

func (r *stubRepo) LockOwner(_ context.Context, owner snow.ID) error {
	r.calls = append(r.calls, "lock")
	r.locked = append(r.locked, owner)
	return r.lockErr
}

func (r *stubRepo) CountSeats(context.Context, snow.ID) (int64, error) {
	r.calls = append(r.calls, "count")
	return r.seats, r.countErr
}

func (r *stubRepo) IsSeat(context.Context, snow.ID, snow.ID) (bool, error) {
	r.calls = append(r.calls, "is_seat")
	return r.seat, r.seatErr
}

func (r *stubRepo) LiveSubscription(context.Context, snow.ID) (*domain.BillingSubscription, error) {
	r.calls = append(r.calls, "live")
	return r.live, r.liveErr
}

func (r *stubRepo) UpsertSubscription(_ context.Context, sub domain.BillingSubscription) (bool, error) {
	r.calls = append(r.calls, "upsert")
	if r.upsertErr != nil {
		return false, r.upsertErr
	}
	r.upserted = append(r.upserted, sub)
	return true, nil
}

type stubLedger struct {
	owner    snow.ID
	ownerErr error
	quotas   map[snow.ID]int64
	quotaErr error
}

func (l *stubLedger) BillingOwner(context.Context, snow.ID) (snow.ID, error) {
	return l.owner, l.ownerErr
}

func (l *stubLedger) SetComputedQuota(_ context.Context, userID snow.ID, quota int64) error {
	if l.quotaErr != nil {
		return l.quotaErr
	}
	if l.quotas == nil {
		l.quotas = map[snow.ID]int64{}
	}
	l.quotas[userID] = quota
	return nil
}

type txKey struct{}

type stubTx struct {
	began int
	err   error
}

func (t *stubTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	t.began++
	if t.err != nil {
		return t.err
	}
	return fn(context.WithValue(ctx, txKey{}, true))
}

func inTx(ctx context.Context) bool {
	v, _ := ctx.Value(txKey{}).(bool)
	return v
}

var testConfig = Config{FreeSeats: 3, FreeQuotaBytes: 1000, SeatIncludedBytes: 100, StoragePackBytes: 5000}

func newStubService() (*Service, *stubRepo, *stubLedger, *stubTx) {
	repo := &stubRepo{}
	ledger := &stubLedger{owner: 7}
	tx := &stubTx{}
	return New(repo, ledger, tx, testConfig), repo, ledger, tx
}

func at(minute int) time.Time {
	return time.Date(2026, 11, 7, 12, minute, 0, 0, time.UTC)
}
