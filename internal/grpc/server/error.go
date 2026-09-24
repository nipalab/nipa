package server

import (
	"errors"
	"log/slog"

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
	if e.Code >= 500 {
		slog.Error("request failed", "code", e.Code, "message", e.Message, "internal", e.InternalMessage)
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
	case 409:
		return status.Error(codes.FailedPrecondition, e.Message)
	default:
		return status.Error(codes.Internal, e.Message)
	}
}
