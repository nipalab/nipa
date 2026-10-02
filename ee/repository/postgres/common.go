// Package postgres implements the Nipa Enterprise Edition server repositories
// on top of postgres.
//
// Enterprise Edition: see ee/LICENSE.
package postgres

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func handleError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.Error{
			Code:    404,
			Message: "record not found",
			Cause:   err,
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return &domain.Error{
				Code:    409,
				Message: "record already exists",
				Cause:   err,
			}
		}
	}
	return &domain.Error{
		Code:            500,
		Message:         "database error",
		InternalMessage: err.Error(),
		Cause:           err,
	}
}

func nullTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func nullInt64Ptr(i sql.NullInt64) *int64 {
	if !i.Valid {
		return nil
	}
	return &i.Int64
}

func nullSnowID(id *snow.ID) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: id.Int64(), Valid: true}
}

func nullInt64SnowIDPtr(i sql.NullInt64) *snow.ID {
	if !i.Valid {
		return nil
	}
	value := snow.ID(i.Int64)
	return &value
}

func snowIDPtr(id sql.NullInt64) *snow.ID {
	if !id.Valid {
		return nil
	}
	value := snow.ID(id.Int64)
	return &value
}

func timePtrToNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{Valid: false}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
