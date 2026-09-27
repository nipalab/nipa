package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// WebhookRepository persists project webhook endpoints and the delivery log
// that tracks every attempt, so failed deliveries survive a restart and can be
// replayed.
type WebhookRepository struct {
	db      *sql.DB
	queries *sqlcSqlite.Queries
}

func NewWebhookRepository(db *sql.DB) *WebhookRepository {
	return &WebhookRepository{db: db, queries: sqlcSqlite.New(db)}
}

func (r *WebhookRepository) Create(ctx context.Context, webhook domain.Webhook) (*domain.Webhook, error) {
	row, err := r.queries.WebhookCreate(ctx, sqlcSqlite.WebhookCreateParams{
		ID:          webhook.ID.Int64(),
		ProjectID:   webhook.ProjectID.Int64(),
		Name:        webhook.Name,
		Url:         webhook.URL,
		Secret:      webhook.Secret,
		Events:      encodeWebhookEvents(webhook.Events),
		PathPrefix:  webhook.PathPrefix,
		IsActive:    webhook.IsActive,
		InsecureTls: webhook.InsecureTLS,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookToDomain(row), nil
}

func (r *WebhookRepository) Get(ctx context.Context, projectID, id snow.ID) (*domain.Webhook, error) {
	row, err := r.queries.WebhookGet(ctx, sqlcSqlite.WebhookGetParams{
		ProjectID: projectID.Int64(),
		ID:        id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookToDomain(row), nil
}

func (r *WebhookRepository) ListByProject(ctx context.Context, projectID snow.ID) ([]*domain.Webhook, error) {
	rows, err := r.queries.WebhookListByProject(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return webhooksToDomain(rows), nil
}

func (r *WebhookRepository) ListActiveByProject(ctx context.Context, projectID snow.ID) ([]*domain.Webhook, error) {
	rows, err := r.queries.WebhookListActiveByProject(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	return webhooksToDomain(rows), nil
}

func (r *WebhookRepository) Update(ctx context.Context, webhook domain.Webhook) (*domain.Webhook, error) {
	row, err := r.queries.WebhookUpdate(ctx, sqlcSqlite.WebhookUpdateParams{
		Name:        webhook.Name,
		Url:         webhook.URL,
		Events:      encodeWebhookEvents(webhook.Events),
		PathPrefix:  webhook.PathPrefix,
		IsActive:    webhook.IsActive,
		InsecureTls: webhook.InsecureTLS,
		ProjectID:   webhook.ProjectID.Int64(),
		ID:          webhook.ID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookToDomain(row), nil
}

func (r *WebhookRepository) RotateSecret(ctx context.Context, projectID, id snow.ID, secret string) (*domain.Webhook, error) {
	row, err := r.queries.WebhookRotateSecret(ctx, sqlcSqlite.WebhookRotateSecretParams{
		Secret:    secret,
		ProjectID: projectID.Int64(),
		ID:        id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookToDomain(row), nil
}

// Delete removes the webhook and its delivery log. The log is dropped first
// because the server's SQLite connections do not enforce foreign keys.
func (r *WebhookRepository) Delete(ctx context.Context, projectID, id snow.ID) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return handleError(err)
	}
	defer func() { _ = tx.Rollback() }()

	q := sqlcSqlite.New(tx)
	if _, err := q.WebhookDeliveryDeleteByWebhook(ctx, id.Int64()); err != nil {
		return handleError(err)
	}
	rows, err := q.WebhookDelete(ctx, sqlcSqlite.WebhookDeleteParams{
		ProjectID: projectID.Int64(),
		ID:        id.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if rows == 0 {
		return domain.NewErrorRecordNotFound()
	}
	return handleError(tx.Commit())
}

func (r *WebhookRepository) CreateDelivery(ctx context.Context, delivery domain.WebhookDelivery) (*domain.WebhookDelivery, error) {
	state := delivery.State
	if state == "" {
		state = domain.WebhookDeliveryPending
	}
	row, err := r.queries.WebhookDeliveryCreate(ctx, sqlcSqlite.WebhookDeliveryCreateParams{
		ID:          delivery.ID.Int64(),
		WebhookID:   delivery.WebhookID.Int64(),
		EventType:   delivery.EventType,
		Payload:     delivery.Payload,
		State:       state,
		NextRetryAt: timePtrToNullTime(delivery.NextRetryAt),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveryToDomain(row), nil
}

func (r *WebhookRepository) GetDelivery(ctx context.Context, webhookID, id snow.ID) (*domain.WebhookDelivery, error) {
	row, err := r.queries.WebhookDeliveryGet(ctx, sqlcSqlite.WebhookDeliveryGetParams{
		WebhookID: webhookID.Int64(),
		ID:        id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveryToDomain(row), nil
}

func (r *WebhookRepository) ListDeliveries(ctx context.Context, webhookID snow.ID, limit, offset int64) ([]*domain.WebhookDelivery, error) {
	rows, err := r.queries.WebhookDeliveryList(ctx, sqlcSqlite.WebhookDeliveryListParams{
		WebhookID: webhookID.Int64(),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveriesToDomain(rows), nil
}

func (r *WebhookRepository) ListPendingRetries(ctx context.Context, now time.Time, limit int64) ([]*domain.WebhookDelivery, error) {
	rows, err := r.queries.WebhookDeliveryListPendingRetry(ctx, sqlcSqlite.WebhookDeliveryListPendingRetryParams{
		Now:   sql.NullTime{Time: now, Valid: true},
		Limit: limit,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveriesToDomain(rows), nil
}

func (r *WebhookRepository) MarkDelivered(ctx context.Context, id snow.ID, attempt int64, responseStatus *int64, at time.Time) (*domain.WebhookDelivery, error) {
	row, err := r.queries.WebhookDeliveryMarkDelivered(ctx, sqlcSqlite.WebhookDeliveryMarkDeliveredParams{
		Attempt:        attempt,
		ResponseStatus: int64PtrToNull(responseStatus),
		DeliveredAt:    sql.NullTime{Time: at, Valid: true},
		ID:             id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveryToDomain(row), nil
}

func (r *WebhookRepository) Reschedule(ctx context.Context, id snow.ID, attempt int64, responseStatus *int64, lastError string, nextRetryAt time.Time) (*domain.WebhookDelivery, error) {
	row, err := r.queries.WebhookDeliveryReschedule(ctx, sqlcSqlite.WebhookDeliveryRescheduleParams{
		Attempt:        attempt,
		ResponseStatus: int64PtrToNull(responseStatus),
		LastError:      lastError,
		NextRetryAt:    sql.NullTime{Time: nextRetryAt, Valid: true},
		ID:             id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveryToDomain(row), nil
}

func (r *WebhookRepository) MarkFailed(ctx context.Context, id snow.ID, attempt int64, responseStatus *int64, lastError string) (*domain.WebhookDelivery, error) {
	row, err := r.queries.WebhookDeliveryMarkFailed(ctx, sqlcSqlite.WebhookDeliveryMarkFailedParams{
		Attempt:        attempt,
		ResponseStatus: int64PtrToNull(responseStatus),
		LastError:      lastError,
		ID:             id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveryToDomain(row), nil
}

func (r *WebhookRepository) Requeue(ctx context.Context, webhookID, id snow.ID, at time.Time) (*domain.WebhookDelivery, error) {
	row, err := r.queries.WebhookDeliveryRequeue(ctx, sqlcSqlite.WebhookDeliveryRequeueParams{
		NextRetryAt: sql.NullTime{Time: at, Valid: true},
		WebhookID:   webhookID.Int64(),
		ID:          id.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return webhookDeliveryToDomain(row), nil
}

// DeleteOldDeliveries trims the delivery log to the newest keep rows, leaving
// pending rows alone so a scheduled retry can never be swept away.
func (r *WebhookRepository) DeleteOldDeliveries(ctx context.Context, webhookID snow.ID, keep int64) (int64, error) {
	rows, err := r.queries.WebhookDeliveryDeleteOld(ctx, sqlcSqlite.WebhookDeliveryDeleteOldParams{
		WebhookID: webhookID.Int64(),
		Keep:      keep,
	})
	if err != nil {
		return 0, handleError(err)
	}
	return rows, nil
}

func webhooksToDomain(rows []sqlcSqlite.Webhook) []*domain.Webhook {
	webhooks := make([]*domain.Webhook, 0, len(rows))
	for _, row := range rows {
		webhooks = append(webhooks, webhookToDomain(row))
	}
	return webhooks
}

func webhookToDomain(row sqlcSqlite.Webhook) *domain.Webhook {
	return &domain.Webhook{
		ID:          snow.ID(row.ID),
		ProjectID:   snow.ID(row.ProjectID),
		Name:        row.Name,
		URL:         row.Url,
		Secret:      row.Secret,
		Events:      decodeWebhookEvents(row.Events),
		PathPrefix:  row.PathPrefix,
		IsActive:    row.IsActive,
		InsecureTLS: row.InsecureTls,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func webhookDeliveriesToDomain(rows []sqlcSqlite.WebhookDelivery) []*domain.WebhookDelivery {
	deliveries := make([]*domain.WebhookDelivery, 0, len(rows))
	for _, row := range rows {
		deliveries = append(deliveries, webhookDeliveryToDomain(row))
	}
	return deliveries
}

func webhookDeliveryToDomain(row sqlcSqlite.WebhookDelivery) *domain.WebhookDelivery {
	return &domain.WebhookDelivery{
		ID:             snow.ID(row.ID),
		WebhookID:      snow.ID(row.WebhookID),
		EventType:      row.EventType,
		Payload:        row.Payload,
		State:          row.State,
		Attempt:        row.Attempt,
		ResponseStatus: nullInt64Ptr(row.ResponseStatus),
		LastError:      row.LastError,
		NextRetryAt:    nullTimePtr(row.NextRetryAt),
		DeliveredAt:    nullTimePtr(row.DeliveredAt),
		CreatedAt:      row.CreatedAt,
	}
}

func encodeWebhookEvents(events []string) string {
	return strings.Join(events, ",")
}

func decodeWebhookEvents(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func int64PtrToNull(i *int64) sql.NullInt64 {
	if i == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *i, Valid: true}
}
