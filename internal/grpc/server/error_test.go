package server

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/domain"
)

func TestHandleError_Nil(t *testing.T) {
	require.NoError(t, handleError(nil))
}

func TestHandleError_NonDomainError(t *testing.T) {
	err := handleError(errors.New("something broke"))
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, "something broke", status.Convert(err).Message())
}

func TestHandleError_Domain400(t *testing.T) {
	err := handleError(&domain.Error{Code: 400, Message: "bad request"})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, "bad request", status.Convert(err).Message())
}

func TestHandleError_Domain401(t *testing.T) {
	err := handleError(&domain.Error{Code: 401, Message: "unauthorized"})
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.Equal(t, "unauthorized", status.Convert(err).Message())
}

func TestHandleError_Domain403(t *testing.T) {
	err := handleError(&domain.Error{Code: 403, Message: "no permission"})
	require.Error(t, err)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, "no permission", status.Convert(err).Message())
}

func TestHandleError_Domain404(t *testing.T) {
	err := handleError(&domain.Error{Code: 404, Message: "record not found"})
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
	require.Equal(t, "record not found", status.Convert(err).Message())
}

func TestHandleError_Domain500(t *testing.T) {
	err := handleError(&domain.Error{Code: 500, Message: "internal server error"})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, "internal server error", status.Convert(err).Message())
}

func TestHandleError_DomainOther(t *testing.T) {
	err := handleError(&domain.Error{Code: 999, Message: "weird code"})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, "weird code", status.Convert(err).Message())
}

func TestHandleError_WrappedDomainError(t *testing.T) {
	err := handleError(fmt.Errorf("boom: %w", &domain.Error{Code: 404, Message: "record not found", Cause: errors.New("sql: no rows")}))
	require.Error(t, err)
	require.Equal(t, codes.NotFound, status.Code(err))
}
