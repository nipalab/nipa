package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
)

func TestExitCode(t *testing.T) {
	require.Equal(t, 0, ExitCode(nil))
	require.Equal(t, 1, ExitCode(errors.New("boom")))
	require.Equal(t, 1, ExitCode(domain.NewUserError("bad input")))
	require.Equal(t, 1, ExitCode(&domain.Error{Code: 400, Message: "bad input"}))
	require.Equal(t, 2, ExitCode(&domain.Error{Code: 409, Message: "locked"}))
	require.Equal(t, 127, ExitCode(domain.NewNotFoundError("missing")))
	require.Equal(t, 7, ExitCode(&ExitError{Code: 7, Err: errors.New("seven")}))
}

func TestExitError_Unwrap(t *testing.T) {
	inner := errors.New("inner")
	err := &ExitError{Code: 2, Err: inner}
	require.EqualError(t, err, "inner")
	require.ErrorIs(t, err, inner)
}
