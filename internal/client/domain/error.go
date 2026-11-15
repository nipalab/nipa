package domain

import (
	"errors"
	"fmt"
	"time"
)

type Error struct {
	Code        int
	Message     string
	Hint        string
	Action      string
	Cause       error
	Maintenance *MaintenanceInfo
}

// MaintenanceInfo describes the maintenance window that refused a request.
type MaintenanceInfo struct {
	Scope    string     `json:"scope"`
	Mode     string     `json:"mode"`
	Message  string     `json:"message,omitempty"`
	StartsAt time.Time  `json:"starts_at,omitzero"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	Org      string     `json:"org,omitempty"`
	Project  string     `json:"project,omitempty"`
}

// IsMaintenance reports whether err is a request refused by server
// maintenance.
func IsMaintenance(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Maintenance != nil
}

func (e *Error) Unwrap() error {
	return e.Cause
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
