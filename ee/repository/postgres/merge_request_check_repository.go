package postgres

import (
	"context"
	"database/sql"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/snow"
)

type MergeRequestCheckRepository struct {
	queries *sqlcPostgres.Queries
}

func NewMergeRequestCheckRepository(db *sql.DB) *MergeRequestCheckRepository {
	return &MergeRequestCheckRepository{queries: sqlcPostgres.New(dbtx.New(db))}
}

func (r *MergeRequestCheckRepository) Upsert(ctx context.Context, check domain.MergeRequestCheck) (*domain.MergeRequestCheck, error) {
	row, err := r.queries.MergeRequestCheckUpsert(ctx, sqlcPostgres.MergeRequestCheckUpsertParams{
		ID:             check.ID.Int64(),
		MergeRequestID: check.MergeRequestID,
		HeadCommitID:   check.HeadCommitID.Int64(),
		Name:           check.Name,
		State:          check.State,
		DetailsUrl:     check.DetailsURL,
		ReporterID:     check.Reporter.UserID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return &domain.MergeRequestCheck{
		ID:             snow.ID(row.ID),
		MergeRequestID: row.MergeRequestID,
		HeadCommitID:   snow.ID(row.HeadCommitID),
		Name:           row.Name,
		State:          row.State,
		DetailsURL:     row.DetailsUrl,
		Reporter:       domain.ReviewActor{UserID: snow.ID(row.ReporterID)},
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}, nil
}

func (r *MergeRequestCheckRepository) List(ctx context.Context, mergeRequestID int64, headCommitID snow.ID) ([]*domain.MergeRequestCheck, error) {
	rows, err := r.queries.MergeRequestCheckList(ctx, sqlcPostgres.MergeRequestCheckListParams{
		MergeRequestID: mergeRequestID,
		HeadCommitID:   headCommitID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	checks := make([]*domain.MergeRequestCheck, 0, len(rows))
	for _, row := range rows {
		checks = append(checks, &domain.MergeRequestCheck{
			ID:             snow.ID(row.ID),
			MergeRequestID: row.MergeRequestID,
			HeadCommitID:   snow.ID(row.HeadCommitID),
			Name:           row.Name,
			State:          row.State,
			DetailsURL:     row.DetailsUrl,
			Reporter: domain.ReviewActor{
				UserID:   snow.ID(row.ReporterID),
				Name:     row.ReporterName,
				PhotoURL: row.ReporterPhotoUrl.String,
			},
			CreatedAt: row.CreatedAt,
			UpdatedAt: row.UpdatedAt,
		})
	}
	return checks, nil
}
