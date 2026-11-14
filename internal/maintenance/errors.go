package maintenance

import (
	"errors"
	"fmt"
)

const Reason = "MAINTENANCE"

// Error is a request refused by maintenance.
type Error struct {
	Info Info
}

func NewError(w Window, scope Scope) *Error {
	return &Error{Info: NewInfo(w, scope)}
}

func (e *Error) Error() string {
	subject := "server"
	switch e.Info.Scope {
	case ScopeProject:
		subject = "project " + e.Info.Project
	case ScopeOrg:
		subject = "organization " + e.Info.Org
	}
	msg := fmt.Sprintf("%s is in maintenance (%s)", subject, e.Info.Mode.Label())
	if e.Info.Message != "" {
		msg += ": " + e.Info.Message
	}
	return msg
}

func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
