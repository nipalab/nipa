package domain

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxOriginBytes caps the length of a commit origin.
const MaxOriginBytes = 512

// Origin identifies the source-system commit an imported commit was made from:
// <system>:<source>:<native-id>[:<qualifier>].
type Origin struct {
	System    string
	Source    string
	NativeID  string
	Qualifier string
}

type originSystem struct {
	validID   func(string) bool
	qualifier bool
}

var originSystems = map[string]originSystem{
	"git": {validID: isHexID},
	"hg":  {validID: isHexID},
	"svn": {validID: isDecimalID, qualifier: true},
	"p4":  {validID: isDecimalID, qualifier: true},
}

// ParseOrigin validates an origin against the registered systems.
func ParseOrigin(s string) (Origin, error) {
	invalid := func(reason string) (Origin, error) {
		return Origin{}, NewErrorUser(fmt.Sprintf("invalid origin %q: %s", s, reason))
	}
	if s == "" {
		return invalid("must not be empty")
	}
	if len(s) > MaxOriginBytes {
		return Origin{}, NewErrorUser(fmt.Sprintf("invalid origin: longer than %d bytes", MaxOriginBytes))
	}
	if !utf8.ValidString(s) {
		return invalid("not valid UTF-8")
	}
	parts := strings.SplitN(s, ":", 4)
	if len(parts) < 3 {
		return invalid("want <system>:<source>:<native-id>[:<qualifier>]")
	}
	o := Origin{System: parts[0], Source: parts[1], NativeID: parts[2]}
	hasQualifier := len(parts) == 4
	if hasQualifier {
		o.Qualifier = parts[3]
	}
	for _, part := range parts[:3] {
		if strings.IndexFunc(part, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return invalid("whitespace or control characters outside the qualifier")
		}
	}
	if strings.IndexFunc(o.Qualifier, unicode.IsControl) >= 0 {
		return invalid("control characters in the qualifier")
	}
	system, ok := originSystems[o.System]
	if !ok {
		return invalid(fmt.Sprintf("unknown system %q", o.System))
	}
	if o.Source == "" {
		return invalid("source must not be empty")
	}
	if !system.validID(o.NativeID) {
		return invalid(fmt.Sprintf("invalid %s id %q", o.System, o.NativeID))
	}
	switch {
	case system.qualifier && o.Qualifier == "":
		return invalid(fmt.Sprintf("%s origins need a qualifier", o.System))
	case !system.qualifier && hasQualifier:
		return invalid(fmt.Sprintf("%s origins take no qualifier", o.System))
	}
	return o, nil
}

func isHexID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func isDecimalID(s string) bool {
	if s == "" || len(s) > 19 || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var originSystemNames = map[string]string{
	"git": "Git",
	"hg":  "Mercurial",
	"svn": "SVN",
	"p4":  "Perforce",
}

// OriginSystemName is the display name of an origin's source system.
func OriginSystemName(origin string) string {
	system, _, ok := splitOrigin(origin)
	if !ok {
		return ""
	}
	if name, known := originSystemNames[system]; known {
		return name
	}
	return system
}

// OriginShortID is the short display id of an origin: 7 hex characters for
// Git and Mercurial, r<n> for SVN, CL <n> for Perforce.
func OriginShortID(origin string) string {
	system, id, ok := splitOrigin(origin)
	if !ok {
		return ""
	}
	switch system {
	case "git", "hg":
		if len(id) > 7 {
			return id[:7]
		}
	case "svn":
		return "r" + id
	case "p4":
		return "CL " + id
	}
	return id
}

func splitOrigin(origin string) (system, id string, ok bool) {
	parts := strings.SplitN(origin, ":", 4)
	if len(parts) < 3 || parts[0] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[2], true
}
