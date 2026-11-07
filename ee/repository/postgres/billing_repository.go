package postgres

import (
	"context"
	"database/sql"
	"errors"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

// billingAdvisoryNamespace keeps the per-owner billing lock apart from the
// storage ledger (raw owner id) and the file-lock serializer keys.
const billingAdvisoryNamespace int64 = 0x4e495053454154

// BillingRepository stores subscriptions and answers seat questions. Queries
// run through the transaction carried in the context.
type BillingRepository struct {
	queries *sqlcPostgres.Queries
}

func NewBillingRepository(db *sql.DB) *BillingRepository {
	return &BillingRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

// LockOwner takes the owner's transaction-scoped billing lock. It must run
// inside a transaction carried by ctx.
func (r *BillingRepository) LockOwner(ctx context.Context, owner snow.ID) error {
	return handleError(r.queries.LockSeatOwner(ctx, owner.Int64()^billingAdvisoryNamespace))
}

// CountSeats counts the distinct non-deleted users with an access path into
// the owner's non-deleted orgs, the owner included.
func (r *BillingRepository) CountSeats(ctx context.Context, owner snow.ID) (int64, error) {
	seats, err := r.queries.CountOwnerSeats(ctx, owner.Int64())
	if err != nil {
		return 0, handleError(err)
	}
	return seats, nil
}

// IsSeat reports whether user already counts as one of the owner's seats.
func (r *BillingRepository) IsSeat(ctx context.Context, owner, user snow.ID) (bool, error) {
	seat, err := r.queries.IsOwnerSeat(ctx, sqlcPostgres.IsOwnerSeatParams{UserID: user.Int64(), OwnerID: owner.Int64()})
	if err != nil {
		return false, handleError(err)
	}
	return seat, nil
}

// LiveSubscription returns the user's newest active, trialing or past_due
// subscription, or nil when there is none.
func (r *BillingRepository) LiveSubscription(ctx context.Context, user snow.ID) (*domain.BillingSubscription, error) {
	row, err := r.queries.GetLiveSubscription(ctx, user.Int64())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.BillingSubscription{
		ProviderSubscriptionID: row.ProviderSubscriptionID,
		UserID:                 snow.ID(row.UserID),
		Plan:                   row.Plan,
		Status:                 row.Status,
		Interval:               row.Interval,
		Seats:                  row.Seats,
		StoragePacks:           row.StoragePacks,
		CurrentPeriodEnd:       nullTimePtr(row.CurrentPeriodEnd),
		CancelAtPeriodEnd:      row.CancelAtPeriodEnd,
		ProviderUpdatedAt:      row.ProviderUpdatedAt,
	}, nil
}

// UpsertSubscription stores the subscription unless a newer provider update
// is already stored. It reports whether the row was written.
func (r *BillingRepository) UpsertSubscription(ctx context.Context, sub domain.BillingSubscription) (bool, error) {
	var periodEnd sql.NullTime
	if sub.CurrentPeriodEnd != nil {
		periodEnd = sql.NullTime{Time: *sub.CurrentPeriodEnd, Valid: true}
	}
	rows, err := r.queries.UpsertSubscription(ctx, sqlcPostgres.UpsertSubscriptionParams{
		ProviderSubscriptionID: sub.ProviderSubscriptionID,
		UserID:                 sub.UserID.Int64(),
		Plan:                   sub.Plan,
		Status:                 sub.Status,
		Interval:               sub.Interval,
		Seats:                  sub.Seats,
		StoragePacks:           sub.StoragePacks,
		CurrentPeriodEnd:       periodEnd,
		CancelAtPeriodEnd:      sub.CancelAtPeriodEnd,
		ProviderUpdatedAt:      sub.ProviderUpdatedAt,
	})
	if err != nil {
		return false, handleError(err)
	}
	return rows > 0, nil
}

// Account returns the user's billing account, or nil when there is none.
func (r *BillingRepository) Account(ctx context.Context, user snow.ID) (*domain.BillingAccount, error) {
	row, err := r.queries.GetBillingAccount(ctx, user.Int64())
	return toBillingAccount(row, err)
}

// AccountByCustomer returns the account holding a provider customer id, or
// nil when no user has it.
func (r *BillingRepository) AccountByCustomer(ctx context.Context, provider, customerID string) (*domain.BillingAccount, error) {
	row, err := r.queries.GetBillingAccountByCustomer(ctx, sqlcPostgres.GetBillingAccountByCustomerParams{
		Provider:           provider,
		ProviderCustomerID: customerID,
	})
	return toBillingAccount(row, err)
}

// InsertAccount stores the account unless the user or the customer id is
// already linked; the caller reads the account back to see which won.
func (r *BillingRepository) InsertAccount(ctx context.Context, account domain.BillingAccount) error {
	return handleError(r.queries.InsertBillingAccount(ctx, sqlcPostgres.InsertBillingAccountParams{
		UserID:             account.UserID.Int64(),
		Provider:           account.Provider,
		ProviderCustomerID: account.ProviderCustomerID,
	}))
}

// User returns the account data billing needs; a missing user is a 404.
func (r *BillingRepository) User(ctx context.Context, id snow.ID) (*domain.BillingUser, error) {
	row, err := r.queries.GetBillingUser(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.BillingUser{ID: snow.ID(row.ID), Name: row.Name, Email: row.Email, Deleted: row.Deleted}, nil
}

// InsertEvent stores a provider event and reports whether it was new.
func (r *BillingRepository) InsertEvent(ctx context.Context, event domain.BillingEvent) (bool, error) {
	rows, err := r.queries.InsertBillingEvent(ctx, sqlcPostgres.InsertBillingEventParams{
		EventID:    event.EventID,
		Type:       event.Type,
		OccurredAt: event.OccurredAt,
		Payload:    event.Payload,
	})
	if err != nil {
		return false, handleError(err)
	}
	return rows > 0, nil
}

// Event returns a stored event; a missing one is a 404.
func (r *BillingRepository) Event(ctx context.Context, eventID string) (*domain.BillingEvent, error) {
	row, err := r.queries.GetBillingEvent(ctx, eventID)
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.BillingEvent{
		EventID:     row.EventID,
		Type:        row.Type,
		OccurredAt:  row.OccurredAt,
		Payload:     row.Payload,
		ProcessedAt: nullTimePtr(row.ProcessedAt),
		Error:       row.Error.String,
	}, nil
}

// MarkEventProcessed records a successful run and clears an earlier error.
func (r *BillingRepository) MarkEventProcessed(ctx context.Context, eventID string) error {
	return handleError(r.queries.MarkBillingEventProcessed(ctx, eventID))
}

// MarkEventFailed records why an event could not be applied.
func (r *BillingRepository) MarkEventFailed(ctx context.Context, eventID, reason string) error {
	return handleError(r.queries.MarkBillingEventFailed(ctx, sqlcPostgres.MarkBillingEventFailedParams{
		Error:   sql.NullString{String: reason, Valid: true},
		EventID: eventID,
	}))
}

func toBillingAccount(row sqlcPostgres.BillingAccount, err error) (*domain.BillingAccount, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.BillingAccount{
		UserID:             snow.ID(row.UserID),
		Provider:           row.Provider,
		ProviderCustomerID: row.ProviderCustomerID,
	}, nil
}
