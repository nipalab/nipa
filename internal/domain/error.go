package domain

import (
	"errors"
	"fmt"
	"math"
	"time"
)

type Error struct {
	Code            int    // the code uses the HTTP just to make it simpler
	Message         string // message show to the user
	InternalMessage string // uses for internal logging
	Cause           error  // underlying error, if any
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func (e *Error) String() string {
	return fmt.Sprintf("Error{Code: %d, Message: %s, InternalMessage: %s}", e.Code, e.Message, e.InternalMessage)
}

func IsErrorNotFound(err error) bool {
	return isDomainError(err, 404)
}

func IsErrorNoPermission(err error) bool {
	return isDomainError(err, 403)
}

func IsErrorConflict(err error) bool {
	return isDomainError(err, 409)
}

// IsServerError reports whether err is a server-side failure: a 5xx domain
// error or any error that is not a domain error.
func IsServerError(err error) bool {
	if err == nil {
		return false
	}
	var e *Error
	return !errors.As(err, &e) || e.Code >= 500
}

func isDomainError(err error, code int) bool {
	if err == nil {
		return false
	}
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == code
}

func NewErrorRecordNotFound() *Error {
	return &Error{
		Code:    404,
		Message: "record not found",
	}
}

func NewErrorNotFound(message string) *Error {
	return &Error{
		Code:    404,
		Message: message,
	}
}

func NewErrorDatabase(internalMessage string) *Error {
	return &Error{
		Code:            500,
		Message:         "database error",
		InternalMessage: internalMessage,
	}
}

func NewErrorUser(message string) *Error {
	return &Error{
		Code:    400,
		Message: message,
	}
}

func NewErrorUnauthorized(message string) *Error {
	return &Error{
		Code:    401,
		Message: message,
	}
}

func NewErrorNoPermission() *Error {
	return &Error{
		Code:    403,
		Message: "no permission",
	}
}

func NewErrorForbidden(message string) *Error {
	return &Error{
		Code:    403,
		Message: message,
	}
}

func NewErrorConflict(message string) *Error {
	return &Error{
		Code:    409,
		Message: message,
	}
}

func NewErrorServiceUnavailable(message string) *Error {
	return &Error{
		Code:    503,
		Message: message,
	}
}

func NewErrorQuotaExceeded(message string) *Error {
	return &Error{
		Code:    402,
		Message: message,
	}
}

const RateLimitMessage = "rate limit exceeded"

func NewErrorTooManyRequests(retryAfter time.Duration) *Error {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	return &Error{
		Code:    429,
		Message: fmt.Sprintf("%s, retry in %ds", RateLimitMessage, secs),
	}
}

func NewErrorInternalServer(internalMessage string) *Error {
	return &Error{
		Code:            500,
		Message:         "internal server error",
		InternalMessage: internalMessage,
	}
}
