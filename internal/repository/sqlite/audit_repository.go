package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// AuditRepository stores audit log entries. Inserts join the caller's
// transaction so an entry commits with the change it describes.
type AuditRepository struct {
	queries *sqlcSqlite.Queries
}

func NewAuditRepository(db *sql.DB) *AuditRepository {
	return &AuditRepository{queries: sqlcSqlite.New(dbtx.New(db))}
}

func (r *AuditRepository) Insert(ctx context.Context, entry domain.AuditEntry) error {
	metadata, err := marshalAuditMetadata(entry.Metadata)
	if err != nil {
		return err
	}
	return handleError(r.queries.AuditLogInsert(ctx, sqlcSqlite.AuditLogInsertParams{
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
		Metadata:    string(metadata),
		RequestID:   entry.RequestID,
		ClientIp:    entry.ClientIP,
		UserAgent:   entry.UserAgent,
	}))
}

const auditIDSkew = time.Minute

// List returns entries newest first, narrowed by the filter.
func (r *AuditRepository) List(ctx context.Context, filter domain.AuditFilter) ([]*domain.AuditEntry, error) {
	params := sqlcSqlite.AuditLogListParams{Limit: filter.Limit}
	if filter.ActorUserID != nil {
		params.ActorUserID = filter.ActorUserID.Int64()
	}
	if filter.OrgID != nil {
		params.OrgID = filter.OrgID.Int64()
	}
	if filter.ProjectID != nil {
		params.ProjectID = filter.ProjectID.Int64()
	}
	if filter.ActionPrefix != "" {
		params.ActionPrefix = filter.ActionPrefix
	}
	if filter.TargetID != "" {
		params.TargetID = filter.TargetID
	}
	if filter.Since != nil {
		params.Since = filter.Since.UTC()
		params.MinID = snow.FloorAt(filter.Since.Add(-auditIDSkew)).Int64()
	}
	if filter.Until != nil {
		params.Until = filter.Until.UTC()
		params.MaxID = snow.FloorAt(filter.Until.Add(auditIDSkew)).Int64()
	}
	if filter.After != nil {
		params.After = filter.After.Int64()
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

func toDomainAuditEntry(row sqlcSqlite.AuditLog) (*domain.AuditEntry, error) {
	metadata := map[string]any{}
	if row.Metadata != "" {
		if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
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
