package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
)

func TestToStatusError(t *testing.T) {
	require.NoError(t, toStatusError(nil))
	require.Equal(t, codes.Canceled, status.Code(toStatusError(context.Canceled)))
	require.Equal(t, codes.DeadlineExceeded, status.Code(toStatusError(context.DeadlineExceeded)))
	require.Equal(t, codes.InvalidArgument, status.Code(toStatusError(clientDomain.NewUserError("bad"))))
	require.Equal(t, codes.Unauthenticated, status.Code(toStatusError(clientDomain.NewTokenError("no"))))
	require.Equal(t, codes.NotFound, status.Code(toStatusError(clientDomain.NewNotFoundError("missing"))))
	require.Equal(t, codes.PermissionDenied, status.Code(toStatusError(&clientDomain.Error{Code: 403, Message: "denied"})))
	require.Equal(t, codes.FailedPrecondition, status.Code(toStatusError(&clientDomain.Error{Code: 409, Message: "conflict"})))
	require.Equal(t, codes.Unknown, status.Code(toStatusError(errors.New("boom"))))

	original := status.Error(codes.NotFound, "server said no")
	require.Equal(t, original, toStatusError(original), "existing status errors pass through untouched")
}
