package daemon

import (
	"errors"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// toStatusError maps client domain errors back onto gRPC codes, mirroring the
// client transport's server-code mapping so GUI clients see the same classes.
func toStatusError(err error) error {
	if err == nil {
		return nil
	}
	var domErr *clientDomain.Error
	if errors.As(err, &domErr) {
		switch domErr.Code {
		case 400:
			return status.Error(codes.InvalidArgument, domErr.Message)
		case 401:
			return status.Error(codes.Unauthenticated, domErr.Message)
		case 403:
			return status.Error(codes.PermissionDenied, domErr.Message)
		case 404:
			return status.Error(codes.NotFound, domErr.Message)
		case 409:
			return status.Error(codes.FailedPrecondition, domErr.Message)
		}
	}
	return status.Error(codes.Unknown, err.Error())
}
