package domain

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestError_ErrorReturnsMessage(t *testing.T) {
	require.Equal(t, "record not found", NewErrorRecordNotFound().Error())
	require.Equal(t, "no permission", NewErrorNoPermission().Error())
	require.Equal(t, "database error", NewErrorDatabase("boom").Error())
	require.Equal(t, "bad request", NewErrorUser("bad request").Error())
	require.Equal(t, "internal server error", NewErrorInternalServer("boom").Error())
}

func TestError_Unwrap(t *testing.T) {
	cause := errors.New("sql: no rows in result set")
	err := &Error{Code: 404, Message: "record not found", Cause: cause}
	require.True(t, errors.Is(err, cause))
}

func TestError_String(t *testing.T) {
	err := NewErrorInternalServer("boom")
	err.Message = "oops"
	require.Equal(t, "Error{Code: 500, Message: oops, InternalMessage: boom}", err.String())
}

func TestIsErrorNotFound(t *testing.T) {
	require.False(t, IsErrorNotFound(nil))
	require.False(t, IsErrorNotFound(errors.New("plain")))
	require.False(t, IsErrorNotFound(NewErrorUser("not found")))
	require.True(t, IsErrorNotFound(NewErrorRecordNotFound()))
	require.True(t, IsErrorNotFound(NewErrorNotFound("project not found")))
}

func TestIsErrorNotFound_Wrapped(t *testing.T) {
	require.True(t, IsErrorNotFound(fmt.Errorf("wrapped: %w", NewErrorRecordNotFound())))
}

func TestIsErrorNoPermission(t *testing.T) {
	require.False(t, IsErrorNoPermission(nil))
	require.False(t, IsErrorNoPermission(errors.New("plain")))
	require.False(t, IsErrorNoPermission(NewErrorUser("forbidden")))
	require.True(t, IsErrorNoPermission(NewErrorNoPermission()))
}

func TestNewErrorConstructors(t *testing.T) {
	notFound := NewErrorRecordNotFound()
	require.Equal(t, 404, notFound.Code)
	require.Equal(t, "record not found", notFound.Message)
	require.Empty(t, notFound.InternalMessage)

	dbErr := NewErrorDatabase("query failed")
	require.Equal(t, 500, dbErr.Code)
	require.Equal(t, "database error", dbErr.Message)
	require.Equal(t, "query failed", dbErr.InternalMessage)

	userErr := NewErrorUser("invalid input")
	require.Equal(t, 400, userErr.Code)
	require.Equal(t, "invalid input", userErr.Message)

	permErr := NewErrorNoPermission()
	require.Equal(t, 403, permErr.Code)
	require.Equal(t, "no permission", permErr.Message)

	internalErr := NewErrorInternalServer("panic")
	require.Equal(t, 500, internalErr.Code)
	require.Equal(t, "internal server error", internalErr.Message)
	require.Equal(t, "panic", internalErr.InternalMessage)
}
