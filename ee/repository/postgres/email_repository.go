package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
)

type EmailRepository struct {
	queries *sqlcPostgres.Queries
	tx      *dbtx.Transactor
}

func NewEmailRepository(db *sql.DB) *EmailRepository {
	return &EmailRepository{
		queries: sqlcPostgres.New(dbtx.New(db)),
		tx:      dbtx.NewTransactor(db),
	}
}

// Enqueue persists pending deliveries in one transaction.
func (r *EmailRepository) Enqueue(ctx context.Context, deliveries []*domain.EmailDelivery) error {
	return r.tx.WithinTx(ctx, func(ctx context.Context) error {
		for _, delivery := range deliveries {
			_, err := r.queries.EmailDeliveryCreate(ctx, sqlcPostgres.EmailDeliveryCreateParams{
				ID:            delivery.ID.Int64(),
				Event:         delivery.Event,
				ProjectID:     delivery.ProjectID.Int64(),
				UserID:        delivery.UserID.Int64(),
				Email:         delivery.Email,
				Subject:       delivery.Subject,
				Body:          delivery.Body,
				State:         delivery.State,
				NextAttemptAt: timePtrToNullTime(delivery.NextAttemptAt),
			})
			if err != nil {
				return handleError(err)
			}
		}
		return nil
	})
}

func (r *EmailRepository) Get(ctx context.Context, id snow.ID) (*domain.EmailDelivery, error) {
	row, err := r.queries.EmailDeliveryGet(ctx, id.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return toDomainEmailDelivery(row), nil
}

// ClaimDue reclaims stale sending rows and claims the due pending rows for
// delivery, bumping their attempt counter.
func (r *EmailRepository) ClaimDue(ctx context.Context, now, staleBefore time.Time, limit int64) ([]*domain.EmailDelivery, error) {
	if _, err := r.queries.EmailDeliveryReclaimStale(ctx, timePtrToNullTime(&staleBefore)); err != nil {
		return nil, handleError(err)
	}
	rows, err := r.queries.EmailDeliveryClaimDue(ctx, sqlcPostgres.EmailDeliveryClaimDueParams{
		Now:   timePtrToNullTime(&now),
		Limit: limit,
	})
	if err != nil {
		return nil, handleError(err)
	}
	deliveries := make([]*domain.EmailDelivery, 0, len(rows))
	for _, row := range rows {
		deliveries = append(deliveries, toDomainEmailDelivery(row))
	}
	return deliveries, nil
}

func (r *EmailRepository) MarkDelivered(ctx context.Context, id snow.ID, at time.Time) error {
	err := r.queries.EmailDeliveryMarkDelivered(ctx, sqlcPostgres.EmailDeliveryMarkDeliveredParams{
		DeliveredAt: timePtrToNullTime(&at),
		ID:          id.Int64(),
	})
	return handleError(err)
}

func (r *EmailRepository) ScheduleRetry(ctx context.Context, id snow.ID, lastError string, next time.Time) error {
	err := r.queries.EmailDeliveryScheduleRetry(ctx, sqlcPostgres.EmailDeliveryScheduleRetryParams{
		LastError:     lastError,
		NextAttemptAt: timePtrToNullTime(&next),
		ID:            id.Int64(),
	})
	return handleError(err)
}

func (r *EmailRepository) MarkFailed(ctx context.Context, id snow.ID, lastError string) error {
	err := r.queries.EmailDeliveryMarkFailed(ctx, sqlcPostgres.EmailDeliveryMarkFailedParams{
		LastError: lastError,
		ID:        id.Int64(),
	})
	return handleError(err)
}

func (r *EmailRepository) SweepRetention(ctx context.Context, before time.Time) (int64, error) {
	deleted, err := r.queries.EmailDeliverySweep(ctx, before)
	if err != nil {
		return 0, handleError(err)
	}
	return deleted, nil
}

func toDomainEmailDelivery(row sqlcPostgres.EmailDelivery) *domain.EmailDelivery {
	return &domain.EmailDelivery{
		ID:            snow.ID(row.ID),
		Event:         row.Event,
		ProjectID:     snow.ID(row.ProjectID),
		UserID:        snow.ID(row.UserID),
		Email:         row.Email,
		Subject:       row.Subject,
		Body:          row.Body,
		State:         row.State,
		Attempts:      row.Attempts,
		NextAttemptAt: nullTimePtr(row.NextAttemptAt),
		LastError:     row.LastError,
		ClaimedAt:     nullTimePtr(row.ClaimedAt),
		DeliveredAt:   nullTimePtr(row.DeliveredAt),
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}
