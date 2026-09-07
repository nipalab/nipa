package domain

import (
	"errors"
	"fmt"
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

func NewErrorNoPermission() *Error {
	return &Error{
		Code:    403,
		Message: "no permission",
	}
}

func NewErrorInternalServer(internalMessage string) *Error {
	return &Error{
		Code:            500,
		Message:         "internal server error",
		InternalMessage: internalMessage,
	}
}
