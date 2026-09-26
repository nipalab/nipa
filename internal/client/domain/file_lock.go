package domain

import "time"

// FileLock is an exclusive claim on a binary path held by one user.
type FileLock struct {
	ID                 string
	Path               string
	Branch             string
	Global             bool
	HeldBy             string
	HeldByName         string
	MergeRequestNumber *int64
	AcquiredAt         time.Time
}
