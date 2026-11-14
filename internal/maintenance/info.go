package maintenance

import (
	"time"
)

// Info is the wire shape of a maintenance rejection shared by gRPC error
// details and the REST body.
type Info struct {
	Scope    Scope      `json:"scope"`
	Mode     Mode       `json:"mode"`
	Message  string     `json:"message"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	Org      string     `json:"org,omitempty"`
	Project  string     `json:"project,omitempty"`
}

func NewInfo(w Window, scope Scope) Info {
	info := Info{Scope: scope, Mode: w.Mode, Message: w.Message, StartsAt: w.StartsAt, EndsAt: w.EndsAt}
	switch scope {
	case ScopeProject:
		info.Org, info.Project = w.Org, w.Key()
	case ScopeOrg:
		info.Org = w.Org
	}
	return info
}

// Metadata flattens the info into string pairs (gRPC ErrorInfo metadata).
func (i Info) Metadata() map[string]string {
	md := map[string]string{
		"scope":     string(i.Scope),
		"mode":      string(i.Mode),
		"message":   i.Message,
		"starts_at": i.StartsAt.UTC().Format(time.RFC3339),
	}
	if i.EndsAt != nil {
		md["ends_at"] = i.EndsAt.UTC().Format(time.RFC3339)
	}
	if i.Org != "" {
		md["org"] = i.Org
	}
	if i.Project != "" {
		md["project"] = i.Project
	}
	return md
}

func InfoFromMetadata(md map[string]string) Info {
	info := Info{
		Scope:   Scope(md["scope"]),
		Mode:    Mode(md["mode"]),
		Message: md["message"],
		Org:     md["org"],
		Project: md["project"],
	}
	if t, err := time.Parse(time.RFC3339, md["starts_at"]); err == nil {
		info.StartsAt = t
	}
	if t, err := time.Parse(time.RFC3339, md["ends_at"]); err == nil {
		info.EndsAt = &t
	}
	return info
}

// RetryAfter is the time left until EndsAt, zero when unknown or past.
func (i Info) RetryAfter(now time.Time) time.Duration {
	if i.EndsAt == nil || !i.EndsAt.After(now) {
		return 0
	}
	return i.EndsAt.Sub(now)
}
