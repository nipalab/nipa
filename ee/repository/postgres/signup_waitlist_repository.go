package postgres

import (
	"context"
	"database/sql"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

// SignupWaitlistRepository stores the addresses that asked to sign up while
// registration was closed.
type SignupWaitlistRepository struct {
	queries *sqlcPostgres.Queries
}

func NewSignupWaitlistRepository(db *sql.DB) *SignupWaitlistRepository {
	return &SignupWaitlistRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

// Join adds the address; it reports false when the address is already listed.
func (r *SignupWaitlistRepository) Join(ctx context.Context, entry domain.WaitlistEntry) (bool, error) {
	n, err := r.queries.SignupWaitlistJoin(ctx, sqlcPostgres.SignupWaitlistJoinParams{
		ID:     entry.ID.Int64(),
		Email:  normalizeInviteEmail(entry.Email),
		Name:   entry.Name,
		Source: entry.Source,
	})
	if err != nil {
		return false, handleError(err)
	}
	return n > 0, nil
}

// List pages the waitlist by id; pending nil lists every entry.
func (r *SignupWaitlistRepository) List(ctx context.Context, after snow.ID, pending *bool, limit int) ([]*domain.WaitlistEntry, error) {
	var filter sql.NullBool
	if pending != nil {
		filter = sql.NullBool{Bool: *pending, Valid: true}
	}
	rows, err := r.queries.SignupWaitlistList(ctx, sqlcPostgres.SignupWaitlistListParams{
		After:   after.Int64(),
		Pending: filter,
		Limit:   int64(limit),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return toWaitlistEntries(rows), nil
}

func (r *SignupWaitlistRepository) Delete(ctx context.Context, id snow.ID) (bool, error) {
	n, err := r.queries.SignupWaitlistDelete(ctx, id.Int64())
	if err != nil {
		return false, handleError(err)
	}
	return n > 0, nil
}

// Pending locks the oldest entries not yet notified whose address has no
// account; run it inside a transaction.
func (r *SignupWaitlistRepository) Pending(ctx context.Context, limit int) ([]*domain.WaitlistEntry, error) {
	rows, err := r.queries.SignupWaitlistPending(ctx, int64(limit))
	if err != nil {
		return nil, handleError(err)
	}
	return toWaitlistEntries(rows), nil
}

func (r *SignupWaitlistRepository) MarkNotified(ctx context.Context, ids []snow.ID, at time.Time) error {
	raw := make([]int64, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, id.Int64())
	}
	return handleError(r.queries.SignupWaitlistMarkNotified(ctx, sqlcPostgres.SignupWaitlistMarkNotifiedParams{
		NotifiedAt: sql.NullTime{Time: at, Valid: true},
		Ids:        raw,
	}))
}

func toWaitlistEntries(rows []sqlcPostgres.SignupWaitlist) []*domain.WaitlistEntry {
	entries := make([]*domain.WaitlistEntry, 0, len(rows))
	for _, row := range rows {
		entry := &domain.WaitlistEntry{
			ID:        snow.ID(row.ID),
			Email:     row.Email,
			Name:      row.Name,
			Source:    row.Source,
			CreatedAt: row.CreatedAt,
		}
		if row.NotifiedAt.Valid {
			entry.NotifiedAt = &row.NotifiedAt.Time
		}
		entries = append(entries, entry)
	}
	return entries
}
