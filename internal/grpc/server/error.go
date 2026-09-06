package server

import (
	"errors"

	"github.com/nipalab/nipa/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func handleError(err error) error {
	if err == nil {
		return nil
	}

	var e *domain.Error
	if !errors.As(err, &e) {
		return status.Error(codes.Internal, err.Error())
	}

	switch e.Code {
	case 400:
		return status.Error(codes.InvalidArgument, e.Message)
	case 401:
		return status.Error(codes.Unauthenticated, e.Message)
	case 403:
		return status.Error(codes.PermissionDenied, e.Message)
	case 404:
		return status.Error(codes.NotFound, e.Message)
	default:
		return status.Error(codes.Internal, e.Message)
	}
}
