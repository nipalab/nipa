package domain

import "fmt"

type Error struct {
	Code    int
	Message string
	Hint    string
	Action  string
}

func (e *Error) WithHint(hint, action string) *Error {
	e.Hint = hint
	e.Action = action
	return e
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) String() string {
	return fmt.Sprintf("Code: %d, Message: %s", e.Code, e.Message)
}

func NewUserError(message string) *Error {
	return &Error{
		Code:    400,
		Message: message,
	}
}

func NewNotFoundError(message string) *Error {
	return &Error{
		Code:    404,
		Message: message,
	}
}

func NewTokenError(message string) *Error {
	return &Error{
		Code:    401,
		Message: message,
	}
}
