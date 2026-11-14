package sqlite

import (
	"context"
	"database/sql"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	sqlcSqlite "github.com/nipalab/nipa/internal/repository/sqlc/sqlite"
	"github.com/nipalab/nipa/internal/snow"
)

// Maintenance stores the server window and one window per project. Windows
// of deleted projects or organizations are removed by triggers.
type Maintenance struct {
	queries *sqlcSqlite.Queries
}

func NewMaintenanceRepository(db *sql.DB) *Maintenance {
	return &Maintenance{queries: sqlcSqlite.New(dbtx.New(db))}
}

func maintenanceWindow(row sqlcSqlite.MaintenanceWindow) *domain.MaintenanceWindow {
	w := &domain.MaintenanceWindow{
		ID:        snow.ID(row.ID),
		Mode:      row.Mode,
		Message:   row.Message,
		StartsAt:  row.StartsAt,
		EndsAt:    nullTimePtr(row.EndsAt),
		Notify:    row.Notify,
		CreatedBy: snow.ID(row.CreatedBy),
		UpdatedAt: row.UpdatedAt,
	}
	if row.ProjectID.Valid {
		id := snow.ID(row.ProjectID.Int64)
		w.ProjectID = &id
	}
	return w
}

func maintenanceListWindow(row sqlcSqlite.MaintenanceListRow) *domain.MaintenanceWindow {
	w := maintenanceWindow(sqlcSqlite.MaintenanceWindow{
		ID: row.ID, ProjectID: row.ProjectID, Mode: row.Mode, Message: row.Message,
		StartsAt: row.StartsAt, EndsAt: row.EndsAt, Notify: row.Notify,
		CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt,
	})
	w.OrgSlug, w.ProjectSlug = row.OrgSlug, row.ProjectSlug
	return w
}

func (m *Maintenance) List(ctx context.Context) ([]domain.MaintenanceWindow, error) {
	rows, err := m.queries.MaintenanceList(ctx)
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]domain.MaintenanceWindow, 0, len(rows))
	for _, row := range rows {
		out = append(out, *maintenanceListWindow(row))
	}
	return out, nil
}

func (m *Maintenance) GetServer(ctx context.Context) (*domain.MaintenanceWindow, error) {
	row, err := m.queries.MaintenanceGetServer(ctx)
	if err != nil {
		return nil, handleError(err)
	}
	return maintenanceWindow(row), nil
}

func (m *Maintenance) GetProject(ctx context.Context, projectID snow.ID) (*domain.MaintenanceWindow, error) {
	row, err := m.queries.MaintenanceGetProject(ctx, sql.NullInt64{Int64: projectID.Int64(), Valid: true})
	if err != nil {
		return nil, handleError(err)
	}
	return maintenanceListWindow(sqlcSqlite.MaintenanceListRow(row)), nil
}

func (m *Maintenance) UpsertServer(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
	row, err := m.queries.MaintenanceUpsertServer(ctx, sqlcSqlite.MaintenanceUpsertServerParams{
		ID:        w.ID.Int64(),
		Mode:      w.Mode,
		Message:   w.Message,
		StartsAt:  w.StartsAt,
		EndsAt:    timePtrToNullTime(w.EndsAt),
		Notify:    w.Notify,
		CreatedBy: w.CreatedBy.Int64(),
		UpdatedAt: w.UpdatedAt,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return maintenanceWindow(row), nil
}

func (m *Maintenance) UpsertProject(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
	if w.ProjectID == nil {
		return nil, domain.NewErrorUser("project window without a project")
	}
	row, err := m.queries.MaintenanceUpsertProject(ctx, sqlcSqlite.MaintenanceUpsertProjectParams{
		ID:        w.ID.Int64(),
		ProjectID: nullSnowID(w.ProjectID),
		Mode:      w.Mode,
		Message:   w.Message,
		StartsAt:  w.StartsAt,
		EndsAt:    timePtrToNullTime(w.EndsAt),
		Notify:    w.Notify,
		CreatedBy: w.CreatedBy.Int64(),
		UpdatedAt: w.UpdatedAt,
	})
	if err != nil {
		return nil, handleError(err)
	}
	out := maintenanceWindow(row)
	out.OrgSlug, out.ProjectSlug = w.OrgSlug, w.ProjectSlug
	return out, nil
}

func (m *Maintenance) Delete(ctx context.Context, id snow.ID) (bool, error) {
	n, err := m.queries.MaintenanceDelete(ctx, id.Int64())
	return n > 0, handleError(err)
}

func (m *Maintenance) RecipientsServer(ctx context.Context) ([]domain.MaintenanceRecipient, error) {
	rows, err := m.queries.MaintenanceRecipientsServer(ctx)
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]domain.MaintenanceRecipient, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.MaintenanceRecipient{UserID: snow.ID(row.ID), Name: row.Name, Email: row.Email})
	}
	return out, nil
}

func (m *Maintenance) RecipientsProject(ctx context.Context, projectID snow.ID) ([]domain.MaintenanceRecipient, error) {
	rows, err := m.queries.MaintenanceRecipientsProject(ctx, projectID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]domain.MaintenanceRecipient, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.MaintenanceRecipient{UserID: snow.ID(row.ID), Name: row.Name, Email: row.Email})
	}
	return out, nil
}
