package postgres

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/suite"

	"github.com/nipalab/nipa/internal/domain"
)

type CommonSuite struct {
	suite.Suite
}

func TestCommonSuite(t *testing.T) {
	suite.Run(t, new(CommonSuite))
}

func (s *CommonSuite) TestHandleError_Nil() {
	s.NoError(handleError(nil))
}

func (s *CommonSuite) TestHandleError_RecordNotFound() {
	err := handleError(sql.ErrNoRows)
	s.Require().Error(err)

	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(404, domErr.Code)
	s.Equal("record not found", domErr.Message)
}

func (s *CommonSuite) TestHandleError_UniqueViolation() {
	err := handleError(&pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"})
	s.Require().Error(err)

	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(409, domErr.Code)
	s.Equal("record already exists", domErr.Message)
}

func (s *CommonSuite) TestHandleError_DatabaseError() {
	err := handleError(errors.New("connection refused"))
	s.Require().Error(err)

	var domErr *domain.Error
	s.Require().ErrorAs(err, &domErr)
	s.Equal(500, domErr.Code)
	s.Equal("database error", domErr.Message)
	s.Equal("connection refused", domErr.InternalMessage)
}

func (s *CommonSuite) TestNullTimePtr_Valid() {
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	got := nullTimePtr(sql.NullTime{Time: ts, Valid: true})
	s.Require().NotNil(got)
	s.Equal(ts, *got)
}

func (s *CommonSuite) TestNullTimePtr_Invalid() {
	s.Nil(nullTimePtr(sql.NullTime{}))
}

func (s *CommonSuite) TestNullInt64Ptr_Valid() {
	got := nullInt64Ptr(sql.NullInt64{Int64: 42, Valid: true})
	s.Require().NotNil(got)
	s.Equal(int64(42), *got)
}

func (s *CommonSuite) TestNullInt64Ptr_Invalid() {
	s.Nil(nullInt64Ptr(sql.NullInt64{}))
}

func (s *CommonSuite) TestTimePtrToNullTime_NonNil() {
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC)
	got := timePtrToNullTime(&ts)
	s.True(got.Valid)
	s.Equal(ts, got.Time)
}

func (s *CommonSuite) TestTimePtrToNullTime_Nil() {
	s.False(timePtrToNullTime(nil).Valid)
}
