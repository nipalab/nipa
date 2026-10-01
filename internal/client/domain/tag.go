package domain

import "time"

// Tag is a named pointer to a commit with an optional release annotation.
type Tag struct {
	ID        string
	Name      string
	CommitID  string
	Message   string
	UserID    string
	CreatedAt time.Time
}
