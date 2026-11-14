// Package maintenance holds the server and project maintenance state: modes,
// windows, the request rule table and the in-memory snapshot requests read.
package maintenance

import (
	"fmt"
	"net/http"
)

type Mode string

const (
	Off      Mode = "off"
	ReadOnly Mode = "read_only"
	Full     Mode = "full"
	Frozen   Mode = "frozen"
)

func (m Mode) rank() int {
	switch m {
	case ReadOnly:
		return 1
	case Full:
		return 2
	case Frozen:
		return 3
	default:
		return 0
	}
}

// Valid reports whether m is a mode a window can hold.
func (m Mode) Valid() bool {
	return m == ReadOnly || m == Full || m == Frozen
}

// Label is the mode in plain words for user-facing messages.
func (m Mode) Label() string {
	switch m {
	case ReadOnly:
		return "read-only"
	case Full:
		return "closed"
	case Frozen:
		return "frozen"
	default:
		return "off"
	}
}

func ParseMode(s string) (Mode, error) {
	m := Mode(s)
	if !m.Valid() {
		return Off, fmt.Errorf("unknown maintenance mode %q", s)
	}
	return m, nil
}

// Stricter returns the stricter of two modes: frozen > full > read_only > off.
func Stricter(a, b Mode) Mode {
	if b.rank() > a.rank() {
		return b
	}
	if a.rank() == 0 {
		return Off
	}
	return a
}

type Class string

const (
	ClassPublic  Class = "public"
	ClassAuth    Class = "auth"
	ClassControl Class = "control"
	ClassRead    Class = "read"
	ClassWrite   Class = "write"
)

// Allowed applies the rule table without a method: control requests are
// treated as writes in frozen mode.
func Allowed(class Class, mode Mode) bool {
	return AllowedMethod(class, mode, "")
}

// AllowedMethod applies the rule table. method only matters for control
// requests in frozen mode, where GET and HEAD stay open.
func AllowedMethod(class Class, mode Mode, method string) bool {
	if mode.rank() == 0 {
		return true
	}
	switch class {
	case ClassPublic:
		return true
	case ClassAuth:
		return mode != Frozen
	case ClassControl:
		return mode != Frozen || method == http.MethodGet || method == http.MethodHead
	case ClassRead:
		return mode != Full
	default:
		return false
	}
}
