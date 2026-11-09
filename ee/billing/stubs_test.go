package billing

import (
	"context"
	"time"

	"github.com/nipalab/nipa/ee/storageusage"
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
	stale     bool

	accounts     map[snow.ID]*domain.BillingAccount
	accountErr   error
	insertAccErr error
	users        map[snow.ID]*domain.BillingUser
	userErr      error
	events       map[string]*domain.BillingEvent
	eventErr     error
	markErr      error
	owners       []snow.ID
	ownersErr    error
	ownerPages   int
}

func (r *stubRepo) QuotaOwners(_ context.Context, after snow.ID, limit int64) ([]snow.ID, error) {
	r.ownerPages++
	if r.ownersErr != nil {
		return nil, r.ownersErr
	}
	var page []snow.ID
	for _, id := range r.owners {
		if id > after && int64(len(page)) < limit {
			page = append(page, id)
		}
	}
	return page, nil
}

func (r *stubRepo) Account(_ context.Context, user snow.ID) (*domain.BillingAccount, error) {
	r.calls = append(r.calls, "account")
	if r.accountErr != nil {
		return nil, r.accountErr
	}
	return r.accounts[user], nil
}

func (r *stubRepo) AccountByCustomer(_ context.Context, provider, customerID string) (*domain.BillingAccount, error) {
	r.calls = append(r.calls, "account_by_customer")
	if r.accountErr != nil {
		return nil, r.accountErr
	}
	for _, account := range r.accounts {
		if account.Provider == provider && account.ProviderCustomerID == customerID {
			return account, nil
		}
	}
	return nil, nil
}

func (r *stubRepo) InsertAccount(_ context.Context, account domain.BillingAccount) error {
	r.calls = append(r.calls, "insert_account")
	if r.insertAccErr != nil {
		return r.insertAccErr
	}
	if r.accounts == nil {
		r.accounts = map[snow.ID]*domain.BillingAccount{}
	}
	for _, existing := range r.accounts {
		if existing.ProviderCustomerID == account.ProviderCustomerID {
			return nil
		}
	}
	if _, ok := r.accounts[account.UserID]; !ok {
		r.accounts[account.UserID] = &account
	}
	return nil
}

func (r *stubRepo) User(_ context.Context, id snow.ID) (*domain.BillingUser, error) {
	r.calls = append(r.calls, "user")
	if r.userErr != nil {
		return nil, r.userErr
	}
	user, ok := r.users[id]
	if !ok {
		return nil, domain.NewErrorNotFound("record not found")
	}
	return user, nil
}

func (r *stubRepo) InsertEvent(_ context.Context, event domain.BillingEvent) (bool, error) {
	r.calls = append(r.calls, "insert_event")
	if r.eventErr != nil {
		return false, r.eventErr
	}
	if r.events == nil {
		r.events = map[string]*domain.BillingEvent{}
	}
	if _, ok := r.events[event.EventID]; ok {
		return false, nil
	}
	r.events[event.EventID] = &event
	return true, nil
}

func (r *stubRepo) Event(_ context.Context, eventID string) (*domain.BillingEvent, error) {
	r.calls = append(r.calls, "event")
	event, ok := r.events[eventID]
	if !ok {
		return nil, domain.NewErrorNotFound("record not found")
	}
	copied := *event
	return &copied, nil
}

func (r *stubRepo) MarkEventProcessed(_ context.Context, eventID string) error {
	r.calls = append(r.calls, "processed")
	if r.markErr != nil {
		return r.markErr
	}
	now := time.Now()
	r.events[eventID].ProcessedAt = &now
	r.events[eventID].Error = ""
	return nil
}

func (r *stubRepo) MarkEventFailed(_ context.Context, eventID, reason string) error {
	r.calls = append(r.calls, "failed")
	if r.markErr != nil {
		return r.markErr
	}
	r.events[eventID].ProcessedAt = nil
	r.events[eventID].Error = reason
	return nil
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
	if r.stale {
		return false, nil
	}
	r.upserted = append(r.upserted, sub)
	return true, nil
}

type stubLedger struct {
	owner    snow.ID
	ownerErr error
	quotas   map[snow.ID]int64
	quotaErr error
	used     int64
	usageErr error
	computed *int64
	compErr  error
	stored   map[snow.ID]*int64
	deleted  map[snow.ID]bool
	failFor  map[snow.ID]error
}

func (l *stubLedger) ComputedQuota(_ context.Context, userID snow.ID) (*int64, error) {
	if quota, ok := l.stored[userID]; ok {
		return quota, l.compErr
	}
	return l.computed, l.compErr
}

func (l *stubLedger) OwnerStorage(_ context.Context, userID snow.ID) (*storageusage.OwnerStorage, error) {
	if l.usageErr != nil {
		return nil, l.usageErr
	}
	return &storageusage.OwnerStorage{UserID: userID, UsedBytes: l.used}, nil
}

func (l *stubLedger) BillingOwner(context.Context, snow.ID) (snow.ID, error) {
	return l.owner, l.ownerErr
}

func (l *stubLedger) SetComputedQuota(_ context.Context, userID snow.ID, quota int64) error {
	if l.quotaErr != nil {
		return l.quotaErr
	}
	if err := l.failFor[userID]; err != nil {
		return err
	}
	if l.deleted[userID] {
		return domain.NewErrorNotFound("user not found")
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
