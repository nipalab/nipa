package postgres

import (
	"context"
	"database/sql"
	"time"

	sqlcPostgres "github.com/nipalab/nipa/ee/repository/postgres/sqlc"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
	"gopkg.in/typ.v4/slices"
)

type TagRepository struct {
	queries *sqlcPostgres.Queries
}

func NewTagRepository(db *sql.DB) *TagRepository {
	return &TagRepository{queries: sqlcPostgres.New(db)}
}

func (r *TagRepository) ListTags(ctx context.Context, projectID snow.ID, limit int, createdBefore *time.Time, lastID snow.ID) ([]*domain.Tag, error) {
	rows, err := r.queries.TagList(ctx, sqlcPostgres.TagListParams{
		ProjectID:     projectID.Int64(),
		Limit:         int64(limit),
		LastCreatedAt: timePtrToNullTime(createdBefore),
		LastID:        sql.NullInt64{Int64: lastID.Int64(), Valid: lastID != 0},
	})
	if err != nil {
		return nil, handleError(err)
	}
	return slices.Map(rows, func(t sqlcPostgres.Tag) *domain.Tag {
		return tagToDomain(t)
	}), nil
}

func (r *TagRepository) GetTagByName(ctx context.Context, projectID snow.ID, name string) (*domain.Tag, error) {
	row, err := r.queries.TagGetByName(ctx, sqlcPostgres.TagGetByNameParams{
		ProjectID: projectID.Int64(),
		Key:       name,
	})
	if err != nil {
		return nil, handleError(err)
	}
	return tagToDomain(row), nil
}

func (r *TagRepository) CreateTag(ctx context.Context, tag domain.Tag) (*domain.Tag, error) {
	row, err := r.queries.TagCreate(ctx, sqlcPostgres.TagCreateParams{
		ID:        tag.ID.Int64(),
		ProjectID: tag.ProjectID.Int64(),
		Name:      tag.Name,
		Key:       tag.Name,
		CommitID:  tag.CommitID.Int64(),
		Message:   tag.Message,
		UserID:    tag.UserID.Int64(),
	})
	if err != nil {
		return nil, handleError(err)
	}
	return tagToDomain(row), nil
}

func (r *TagRepository) DeleteTag(ctx context.Context, projectID, tagID snow.ID) error {
	affected, err := r.queries.TagDelete(ctx, sqlcPostgres.TagDeleteParams{
		ProjectID: projectID.Int64(),
		ID:        tagID.Int64(),
	})
	if err != nil {
		return handleError(err)
	}
	if affected == 0 {
		return domain.NewErrorRecordNotFound()
	}
	return nil
}

func tagToDomain(t sqlcPostgres.Tag) *domain.Tag {
	return &domain.Tag{
		ID:        snow.ID(t.ID),
		ProjectID: snow.ID(t.ProjectID),
		Name:      t.Name,
		CommitID:  snow.ID(t.CommitID),
		Message:   t.Message,
		UserID:    snow.ID(t.UserID),
		CreatedAt: t.CreatedAt,
		UpdatedAt: t.UpdatedAt,
	}
}
