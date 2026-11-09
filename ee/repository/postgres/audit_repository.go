package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
)

// AuditRepository stores audit log entries. Inserts join the caller's
// transaction so an entry commits with the change it describes.
type AuditRepository struct {
	queries *sqlcPostgres.Queries
}

func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

func (r *AuditRepository) Insert(ctx context.Context, entry domain.AuditEntry) error {
	metadata, err := marshalAuditMetadata(entry.Metadata)
	if err != nil {
		return err
	}
	return handleError(r.queries.AuditLogInsert(ctx, sqlcPostgres.AuditLogInsertParams{
		ID:          entry.ID.Int64(),
		OccurredAt:  entry.OccurredAt.UTC(),
		ActorType:   string(entry.ActorType),
		ActorUserID: nullSnowID(entry.ActorUserID),
		ActorLabel:  entry.ActorLabel,
		Action:      entry.Action,
		OrgID:       nullSnowID(entry.OrgID),
		ProjectID:   nullSnowID(entry.ProjectID),
		TargetType:  entry.TargetType,
		TargetID:    entry.TargetID,
		Metadata:    metadata,
		RequestID:   entry.RequestID,
		ClientIp:    entry.ClientIP,
		UserAgent:   entry.UserAgent,
	}))
}

const auditIDSkew = time.Minute

// List returns entries newest first, narrowed by the filter.
func (r *AuditRepository) List(ctx context.Context, filter domain.AuditFilter) ([]*domain.AuditEntry, error) {
	params := sqlcPostgres.AuditLogListParams{
		ActorUserID: nullSnowID(filter.ActorUserID),
		OrgID:       nullSnowID(filter.OrgID),
		ProjectID:   nullSnowID(filter.ProjectID),
		Since:       timePtrToNullTime(filter.Since),
		Until:       timePtrToNullTime(filter.Until),
		After:       nullSnowID(filter.After),
		Limit:       filter.Limit,
	}
	if filter.ActionPrefix != "" {
		params.ActionPrefix = sql.NullString{String: filter.ActionPrefix, Valid: true}
	}
	if filter.TargetID != "" {
		params.TargetID = sql.NullString{String: filter.TargetID, Valid: true}
	}
	if filter.Since != nil {
		params.MinID = sql.NullInt64{Int64: snow.FloorAt(filter.Since.Add(-auditIDSkew)).Int64(), Valid: true}
	}
	if filter.Until != nil {
		params.MaxID = sql.NullInt64{Int64: snow.FloorAt(filter.Until.Add(auditIDSkew)).Int64(), Valid: true}
	}
	rows, err := r.queries.AuditLogList(ctx, params)
	if err != nil {
		return nil, handleError(err)
	}
	entries := make([]*domain.AuditEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := toDomainAuditEntry(row)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func marshalAuditMetadata(metadata map[string]any) ([]byte, error) {
	if metadata == nil {
		return []byte("{}"), nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil, domain.NewErrorInternalServer("encode audit metadata: " + err.Error())
	}
	return data, nil
}

func toDomainAuditEntry(row sqlcPostgres.AuditLog) (*domain.AuditEntry, error) {
	metadata := map[string]any{}
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			return nil, domain.NewErrorDatabase("decode audit metadata: " + err.Error())
		}
	}
	return &domain.AuditEntry{
		ID:          snow.ID(row.ID),
		OccurredAt:  row.OccurredAt.UTC(),
		ActorType:   domain.ActorType(row.ActorType),
		ActorUserID: snowIDPtr(row.ActorUserID),
		ActorLabel:  row.ActorLabel,
		Action:      row.Action,
		OrgID:       snowIDPtr(row.OrgID),
		ProjectID:   snowIDPtr(row.ProjectID),
		TargetType:  row.TargetType,
		TargetID:    row.TargetID,
		Metadata:    metadata,
		RequestID:   row.RequestID,
		ClientIP:    row.ClientIp,
		UserAgent:   row.UserAgent,
	}, nil
}
