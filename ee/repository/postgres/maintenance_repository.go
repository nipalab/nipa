package postgres

import (
	"context"
	"database/sql"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

// Maintenance stores the server window and one window per org and project.
// Windows of deleted orgs or projects are removed by triggers.
type Maintenance struct {
	queries *sqlcPostgres.Queries
}

func NewMaintenanceRepository(db *sql.DB) *Maintenance {
	return &Maintenance{queries: sqlcPostgres.New(dbtx.New(db))}
}

func nullSnowIDPtr(v sql.NullInt64) *snow.ID {
	if !v.Valid {
		return nil
	}
	id := snow.ID(v.Int64)
	return &id
}

func maintenanceWindow(row sqlcPostgres.MaintenanceWindow) *domain.MaintenanceWindow {
	return &domain.MaintenanceWindow{
		ID:        snow.ID(row.ID),
		OrgID:     nullSnowIDPtr(row.OrgID),
		ProjectID: nullSnowIDPtr(row.ProjectID),
		Mode:      row.Mode,
		Message:   row.Message,
		StartsAt:  row.StartsAt,
		EndsAt:    nullTimePtr(row.EndsAt),
		Notify:    row.Notify,
		CreatedBy: snow.ID(row.CreatedBy),
		UpdatedAt: row.UpdatedAt,
	}
}

func maintenanceListWindow(row sqlcPostgres.MaintenanceListRow) *domain.MaintenanceWindow {
	w := maintenanceWindow(sqlcPostgres.MaintenanceWindow{
		ID: row.ID, OrgID: row.OrgID, ProjectID: row.ProjectID, Mode: row.Mode, Message: row.Message,
		StartsAt: row.StartsAt, EndsAt: row.EndsAt, Notify: row.Notify,
		CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt,
	})
	w.OrgSlug, w.ProjectSlug = row.OrgSlug, row.ProjectSlug
	return w
}

func maintenanceRecipient(id int64, name, email string) domain.MaintenanceRecipient {
	return domain.MaintenanceRecipient{UserID: snow.ID(id), Name: name, Email: email}
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

func (m *Maintenance) GetOrg(ctx context.Context, orgID snow.ID) (*domain.MaintenanceWindow, error) {
	row, err := m.queries.MaintenanceGetOrg(ctx, nullSnowID(&orgID))
	if err != nil {
		return nil, handleError(err)
	}
	return maintenanceListWindow(sqlcPostgres.MaintenanceListRow{
		ID: row.ID, OrgID: row.OrgID, ProjectID: row.ProjectID, Mode: row.Mode, Message: row.Message,
		StartsAt: row.StartsAt, EndsAt: row.EndsAt, Notify: row.Notify,
		CreatedBy: row.CreatedBy, UpdatedAt: row.UpdatedAt, OrgSlug: row.OrgSlug,
	}), nil
}

func (m *Maintenance) GetProject(ctx context.Context, projectID snow.ID) (*domain.MaintenanceWindow, error) {
	row, err := m.queries.MaintenanceGetProject(ctx, nullSnowID(&projectID))
	if err != nil {
		return nil, handleError(err)
	}
	return maintenanceListWindow(sqlcPostgres.MaintenanceListRow(row)), nil
}

func (m *Maintenance) UpsertServer(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
	row, err := m.queries.MaintenanceUpsertServer(ctx, sqlcPostgres.MaintenanceUpsertServerParams{
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

func (m *Maintenance) UpsertOrg(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
	if w.OrgID == nil {
		return nil, domain.NewErrorUser("org window without an org")
	}
	row, err := m.queries.MaintenanceUpsertOrg(ctx, sqlcPostgres.MaintenanceUpsertOrgParams{
		ID:        w.ID.Int64(),
		OrgID:     nullSnowID(w.OrgID),
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
	out.OrgSlug = w.OrgSlug
	return out, nil
}

func (m *Maintenance) UpsertProject(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error) {
	if w.ProjectID == nil {
		return nil, domain.NewErrorUser("project window without a project")
	}
	row, err := m.queries.MaintenanceUpsertProject(ctx, sqlcPostgres.MaintenanceUpsertProjectParams{
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
		out = append(out, maintenanceRecipient(row.ID, row.Name, row.Email))
	}
	return out, nil
}

func (m *Maintenance) RecipientsOrg(ctx context.Context, orgID snow.ID) ([]domain.MaintenanceRecipient, error) {
	rows, err := m.queries.MaintenanceRecipientsOrg(ctx, orgID.Int64())
	if err != nil {
		return nil, handleError(err)
	}
	out := make([]domain.MaintenanceRecipient, 0, len(rows))
	for _, row := range rows {
		out = append(out, maintenanceRecipient(row.ID, row.Name, row.Email))
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
		out = append(out, maintenanceRecipient(row.ID, row.Name, row.Email))
	}
	return out, nil
}
