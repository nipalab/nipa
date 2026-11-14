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
	ExpiresAt          *time.Time
	// Requests are the release requests teammates made for this lock.
	Requests []FileLockRequest
	// Warning reports a local follow-up (pinning, capturing the old version)
	// that failed after the server granted the lock.
	Warning string
}

// FileLockRequest is a teammate's request to release a lock. RequestedAt is
// the last time the holder was notified.
type FileLockRequest struct {
	ID            string
	RequesterID   string
	RequesterName string
	Message       string
	RequestedAt   time.Time
}
