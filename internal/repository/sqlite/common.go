package sqlite

import (
	"database/sql"
	"errors"
	"time"

	"github.com/nipalab/nipa/internal/domain"
)

func handleError(err error) error {
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.Error{
				Code:    404,
				Message: "record not found",
				Cause:   err,
			}
		}
		return &domain.Error{
			Code:            500,
			Message:         "database error",
			InternalMessage: err.Error(),
			Cause:           err,
		}
	}
	return nil
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

func timePtrToNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{Valid: false}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
