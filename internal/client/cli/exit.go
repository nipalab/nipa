package cli

import (
	"errors"

	"github.com/nipalab/nipa/internal/client/domain"
)

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

// ExitCode maps an error to the process exit code: 0 success, 1 generic
// failure, 2 a lock or precondition blocks the operation (409), 127 not found.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	var domErr *domain.Error
	if errors.As(err, &domErr) {
		switch domErr.Code {
		case 404:
			return 127
		case 409:
			return 2
		}
	}
	return 1
}
